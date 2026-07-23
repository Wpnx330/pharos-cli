// Package semver resolves version specifiers (e.g. ^1.2.0, ~1.2.0, 1.x,
// latest, exact) against the set of versions published in the registry.
package semver

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// Resolve picks the best version from `available` that satisfies `spec`.
//
// spec may be:
//   - "" or "latest"          → highest semver in available (or distTag if given)
//   - "1.2.3"                 → exact match
//   - "^1.2.0"                → same major, >= 1.2.0 and < 2.0.0
//   - "~1.2.0"                → same major.minor, >= 1.2.0 and < 1.3.0
//   - "1.x" / "1.2.x" / "*"   → npm-style ranges
//   - ">=1.0.0 <2.0.0"        → arbitrary comparator sets
//
// distTags is a map like {"latest": "1.2.0"}; if spec is a dist-tag name
// (e.g. "latest", "beta") it resolves to the tagged version.
func Resolve(spec string, available []string, distTags map[string]string) (string, error) {
	if len(available) == 0 {
		return "", fmt.Errorf("no versions available")
	}

	// Empty spec → latest.
	if spec == "" {
		spec = "latest"
	}

	// dist-tag reference (latest, beta, …)
	if distTags != nil {
		if v, ok := distTags[spec]; ok {
			if !versionInList(v, available) {
				return "", fmt.Errorf("dist-tag %q points to %s which is not in the version list", spec, v)
			}
			return v, nil
		}
	}

	// "latest" with no dist-tags map → highest semver.
	if spec == "latest" {
		return Highest(available)
	}

	// Exact match short-circuit (and also handles non-semver tags).
	if versionInList(spec, available) {
		return spec, nil
	}

	// Build a constraint. semver/v3 understands ^, ~, x, *, and comparator sets.
	constraint, err := semver.NewConstraint(spec)
	if err != nil {
		return "", fmt.Errorf("invalid version spec %q: %w", spec, err)
	}

	// Sort available descending so the first match is the highest.
	versions := parseSorted(available)
	for _, v := range versions {
		if constraint.Check(v) {
			return v.Original(), nil
		}
	}

	return "", fmt.Errorf("no version matching %q in %v", spec, available)
}

// Highest returns the highest semver from the list, ignoring non-semver
// entries (they're sorted to the end lexicographically).
func Highest(available []string) (string, error) {
	versions := parseSorted(available)
	if len(versions) == 0 {
		return "", fmt.Errorf("no valid semver versions in %v", available)
	}
	return versions[0].Original(), nil
}

// parseSorted parses all available versions, drops invalid ones, and
// returns them sorted descending.
func parseSorted(available []string) []*semver.Version {
	var versions []*semver.Version
	for _, raw := range available {
		v, err := semver.NewVersion(raw)
		if err != nil {
			continue
		}
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].GreaterThan(versions[j])
	})
	return versions
}

func versionInList(v string, list []string) bool {
	for _, item := range list {
		if strings.TrimSpace(item) == strings.TrimSpace(v) {
			return true
		}
	}
	return false
}
