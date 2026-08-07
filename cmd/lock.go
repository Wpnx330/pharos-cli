package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/manifest"
	"github.com/Wpnx330/pharos-cli/internal/resolver"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var lockCmd = &cobra.Command{
	Use:   "lock",
	Short: "Resolve dependencies and write/update pharos.lock",
	Long: ui.Label.Render("pharos lock") + ` resolves all dependencies declared in pharos.json
to concrete versions and writes the result to pharos.lock.

This command:
  1. Reads pharos.json from the current directory
  2. Resolves each dependency (and its transitive deps) to a concrete version
  3. Detects circular dependencies and version conflicts
  4. Writes/updates pharos.lock with all resolved versions

Example:
  pharos lock    # resolve and write lockfile`,
	Args: cobra.NoArgs,
	Run:  runLock,
}

func init() {
	rootCmd.AddCommand(lockCmd)
}

func runLock(cmd *cobra.Command, args []string) {
	// Load pharos.json from the current directory.
	m, err := manifest.Load(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot read pharos.json:"), err)
		fmt.Fprintln(os.Stderr, ui.Muted.Render("Run 'pharos init' to create a manifest first."))
		return
	}

	cfg, client := loadConfig()

	if len(m.Dependencies) == 0 {
		// Even with no dependencies we still write a lockfile containing
		// the primary (root) package so downstream `pharos install --frozen`
		// has something to work from.
		lf := lockfile.New()
		lf.Set(m.Name, lockfile.ServerEntry{
			Version:     m.Version,
			Transport:   normalizeLockTransport(m.Transport),
			Resolved:    cfg.Registry,
			InstalledAt: time.Now().UTC(),
		})
		if err := writeLock(lf); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to write lockfile:"), err)
			return
		}
		fmt.Printf("%s\n", ui.Muted.Render("No dependencies declared in pharos.json; wrote lockfile for primary package only."))
		return
	}

	fmt.Printf("%s\n", ui.Label.Render("Resolving dependencies..."))
	r := resolver.New(client)

	// Convert manifest.Dependency → api.Dependency for the resolver.
	apiDeps := make([]api.Dependency, len(m.Dependencies))
	for i, d := range m.Dependencies {
		apiDeps[i] = api.Dependency{Name: d.Name, Version: d.Version}
	}

	result, err := r.ResolveAll(apiDeps)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Resolution failed:"), err)
		// Still print partial results if available.
		if result == nil {
			return
		}
	}

	// Report conflicts and circular deps.
	for _, c := range result.Conflicts {
		fmt.Fprintf(os.Stderr, "%s  %s: %s vs %s → %s (higher)\n",
			ui.Error.Render("Warning: version conflict"),
			c.Name, c.Existing, c.Requested, c.Resolution)
	}
	for _, cyc := range result.Circular {
		fmt.Fprintf(os.Stderr, "%s  circular dependency detected: %s\n",
			ui.Error.Render("Warning:"), cyc)
	}

	// Print resolved dependency tree.
	flat := resolver.FlatList(result.Flat)
	fmt.Printf("\n%s  %d packages:\n", ui.Label.Render("Resolved"), len(flat))
	for _, entry := range flat {
		fmt.Printf("  %s\n", ui.Muted.Render(entry))
	}

	// Build the lockfile from the resolver output.
	lf := buildLockfile(result, m, cfg.Registry)

	// Always include the primary (root) package itself.
	if m.Name != "" {
		lf.Set(m.Name, lockfile.ServerEntry{
			Version:     m.Version,
			Transport:   normalizeLockTransport(m.Transport),
			Resolved:    cfg.Registry,
			InstalledAt: time.Now().UTC(),
		})
	}

	if err := writeLock(lf); err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to write lockfile:"), err)
		return
	}

	fmt.Printf("\n%s  Lockfile written: %s  (%d packages)\n",
		ui.Label.Render("Done"), lockPathOf(lf), len(lf.Servers))
}

// lockPathOf is a small helper that resolves the lockfile path. It is
// only used for the confirmation message so the printed path matches
// what writeLock actually persisted to.
func lockPathOf(_ *lockfile.Lockfile) string {
	p, err := lockfile.DefaultPath()
	if err != nil {
		return "pharos.lock"
	}
	return p
}

// writeLock resolves the default lockfile path and persists the given
// lockfile there, overwriting any existing lockfile.
func writeLock(lf *lockfile.Lockfile) error {
	path, err := lockfile.DefaultPath()
	if err != nil {
		return fmt.Errorf("resolve lockfile path: %w", err)
	}
	return lf.Save(path)
}

// normalizeLockTransport coerces a manifest transport value into the
// canonical form stored in the lockfile ("stdio", "http", "http-sse",
// "sse"). Empty input defaults to "stdio".
func normalizeLockTransport(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	switch t {
	case "":
		return "stdio"
	case "http-sse", "sse", "https":
		return "http-sse"
	case "http":
		return "http"
	default:
		return t
	}
}

// buildLockfile converts a resolver.Result into a lockfile.Lockfile.
// For every resolved package it records the concrete version, the
// transport (looked up from the manifest dependency list when
// available, otherwise defaulting to "stdio"), and the registry URL
// the package was resolved from.
//
// The flat map is iterated in sorted order so the resulting lockfile
// is deterministic across runs.
func buildLockfile(result *resolver.Result, m *manifest.Manifest, registryURL string) *lockfile.Lockfile {
	lf := lockfile.New()

	// Index manifest dependencies by name so we can recover the
	// transport hint (if the manifest declared one) for top-level deps.
	manifestTransports := make(map[string]string, len(m.Dependencies))
	for _, d := range m.Dependencies {
		// manifest.Dependency only carries Name + Version constraint,
		// not transport — but we keep this map for future extension
		// and to mark which packages are top-level.
		manifestTransports[d.Name] = ""
	}

	// Sort package names for deterministic lockfile output.
	names := make([]string, 0, len(result.Flat))
	for name := range result.Flat {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		version := result.Flat[name]
		transport := "stdio"
		if t, ok := manifestTransports[name]; ok && t != "" {
			transport = normalizeLockTransport(t)
		}
		lf.Set(name, lockfile.ServerEntry{
			Version:     version,
			Transport:   transport,
			Resolved:    registryURL,
			InstalledAt: time.Now().UTC(),
		})
	}

	return lf
}
