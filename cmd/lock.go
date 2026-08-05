package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/api"
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

	if len(m.Dependencies) == 0 {
		fmt.Printf("%s\n", ui.Muted.Render("No dependencies declared in pharos.json. Nothing to lock."))
		return
	}

	_, client := loadConfig()

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

	// TODO: Write the resolved versions to pharos.lock using the lockfile package.
	// This requires extending lockfile.LockEntry to support dependencies or
	// adding a "dependencies" section to the lockfile format.
	fmt.Printf("\n%s  Lockfile update not yet implemented. Resolved versions printed above.\n",
		ui.Muted.Render("Note:"))
}
