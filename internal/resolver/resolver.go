// Package resolver resolves Pharos package dependencies into a flat,
// concrete dependency tree. It walks the registry's manifest
// Dependencies field recursively, resolving semver constraints via the
// internal semver package, and detects circular dependencies and
// version conflicts along the way.
//
// The resolver uses the existing api.Client for all registry calls and
// the existing semver.Resolve for version matching — no new third-party
// dependencies are introduced.
package resolver

import (
	"fmt"
	"sort"
	"strings"

	mmsemver "github.com/Masterminds/semver/v3"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/semver"
)

// DependencyNode is one resolved package entry in a dependency tree.
// The Dependencies field mirrors the registry manifest's dependency
// list for the resolved version, so callers can walk the tree if needed.
type DependencyNode struct {
	Name         string
	Version      string
	Constraint   string
	Dependencies []DependencyNode
}

// Conflict records a version conflict: the same package was required
// at two different resolved versions. The higher version is kept.
type Conflict struct {
	Name       string
	Existing   string
	Requested  string
	Resolution string // the winning version (the higher one)
}

// Result is the outcome of resolving a dependency tree.
//
//   - Tree   is the recursive dependency tree rooted at the top-level
//     package(s) passed to Resolve / ResolveAll.
//   - Flat   is a name → resolved-version map with no duplicates. It is
//     the canonical "what version of each package will we install" set.
//   - Conflicts lists every version conflict encountered (if any).
//   - Circular lists every circular-dependency cycle detected (if any).
type Result struct {
	Tree      []DependencyNode
	Flat      map[string]string
	Conflicts []Conflict
	Circular  []string
}

// Resolver resolves dependency graphs against the Pharos registry.
type Resolver struct {
	Client *api.Client
	// resolved holds the concrete version chosen for each package
	// encountered so far, used for conflict detection.
	resolved map[string]string
	// conflicts accumulates version conflicts for reporting.
	conflicts []Conflict
	// circular accumulates circular-dependency warnings for reporting.
	circular []string
}

// New returns a Resolver backed by the given registry client.
func New(client *api.Client) *Resolver {
	return &Resolver{
		Client:   client,
		resolved: make(map[string]string),
	}
}

// Resolve resolves a single package and its full transitive dependency
// tree. It returns the tree (rooted at name) plus a flat map of every
// package that will be installed (name@version, including dependencies).
// Circular dependencies and version conflicts are collected rather than
// failing the entire resolution — callers can inspect Result.Conflicts
// and Result.Circular to decide how to handle them.
func (r *Resolver) Resolve(name, constraint string) (*Result, error) {
	if r.Client == nil {
		return nil, fmt.Errorf("no API client configured")
	}
	visiting := make(map[string]bool)
	node, err := r.resolve(name, constraint, visiting)
	result := &Result{
		Flat:      r.resolved,
		Conflicts: r.conflicts,
		Circular:  r.circular,
	}
	if err != nil {
		return result, err
	}
	result.Tree = []DependencyNode{*node}
	return result, nil
}

// ResolveAll resolves a list of top-level dependencies (e.g. from a
// pharos.json manifest) into a flat dependency set. Each dependency is
// resolved recursively. The returned Result.Flat contains every package
// (top-level + transitive) at its concrete version.
func (r *Resolver) ResolveAll(deps []api.Dependency) (*Result, error) {
	if r.Client == nil {
		return nil, fmt.Errorf("no API client configured")
	}
	visiting := make(map[string]bool)
	result := &Result{
		Flat:      r.resolved,
		Conflicts: r.conflicts,
		Circular:  r.circular,
	}
	for _, d := range deps {
		node, err := r.resolve(d.Name, d.Version, visiting)
		if err != nil {
			return result, err
		}
		result.Tree = append(result.Tree, *node)
	}
	return result, nil
}

// resolve is the recursive worker. It:
//  1. Detects circular dependencies via the visiting set.
//  2. Fetches the package from the registry.
//  3. Resolves the version constraint via semver.Resolve.
//  4. Records the resolved version, detecting conflicts (same package,
//     different version → keep higher, record a Conflict).
//  5. Reads the manifest's Dependencies and recurses.
func (r *Resolver) resolve(name, constraint string, visiting map[string]bool) (*DependencyNode, error) {
	// Circular-dependency detection.
	if visiting[name] {
		r.circular = append(r.circular, name)
		return &DependencyNode{
			Name:       name,
			Constraint: constraint,
		}, nil
	}

	// Fetch the package packument from the registry.
	pkg, err := r.Client.GetPackage(name)
	if err != nil {
		return nil, fmt.Errorf("fetch package %s: %w", name, err)
	}

	available := pkg.VersionStrings()
	if len(available) == 0 {
		return nil, fmt.Errorf("package %s has no published versions", name)
	}

	// Resolve the version constraint.
	best, err := semver.Resolve(constraint, available, pkg.DistTags)
	if err != nil {
		return nil, fmt.Errorf("resolve %s@%s: %w", name, constraint, err)
	}

	// Conflict detection: if we've already resolved this package to a
	// different version, keep the higher one and record the conflict.
	if prev, ok := r.resolved[name]; ok {
		if prev != best {
			winner := higherVersion(prev, best)
			r.resolved[name] = winner
			r.conflicts = append(r.conflicts, Conflict{
				Name:       name,
				Existing:   prev,
				Requested:  best,
				Resolution: winner,
			})
			best = winner
		}
	} else {
		r.resolved[name] = best
	}

	// Find the resolved version's manifest to read its dependencies.
	vd := pkg.FindVersion(best)
	node := &DependencyNode{
		Name:       name,
		Version:    best,
		Constraint: constraint,
	}
	if vd == nil {
		return node, nil
	}

	// Recurse into dependencies.
	visiting[name] = true
	defer delete(visiting, name)

	for _, dep := range vd.Manifest.Dependencies {
		child, err := r.resolve(dep.Name, dep.Version, visiting)
		if err != nil {
			return nil, err
		}
		node.Dependencies = append(node.Dependencies, *child)
	}

	return node, nil
}

// higherVersion returns whichever of a or b is the higher semver. If
// either fails to parse as a semver, it falls back to lexicographic
// comparison so the function always returns one of the two inputs.
func higherVersion(a, b string) string {
	va, errA := mmsemver.NewVersion(a)
	vb, errB := mmsemver.NewVersion(b)
	if errA != nil || errB != nil {
		if strings.Compare(a, b) >= 0 {
			return a
		}
		return b
	}
	if va.GreaterThan(vb) {
		return a
	}
	return b
}

// FlatList returns the resolved dependency map as a sorted slice of
// "name@version" strings, suitable for display.
func FlatList(flat map[string]string) []string {
	names := make([]string, 0, len(flat))
	for n := range flat {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = n + "@" + flat[n]
	}
	return out
}
