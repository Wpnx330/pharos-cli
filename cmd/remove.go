package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var removeCmd = &cobra.Command{
	Use:     "remove <name>",
	Aliases: []string{"rm", "uninstall"},
	Short:   "Remove an installed MCP server",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		removed := false

		// 1. Remove from store (~/.pharos/store/{name}/)
		storeDir, err := os.UserHomeDir()
		if err == nil {
			pkgDir := filepath.Join(storeDir, ".pharos", "store", name)
			if _, err := os.Stat(pkgDir); err == nil {
				if err := os.RemoveAll(pkgDir); err != nil {
					fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to remove from store:"), err)
					return
				}
				removed = true
				fmt.Printf("%s  %s\n", ui.Success.Render("✓ Removed from store:"), pkgDir)
			}
		}

		// 2. Remove from all detected client configs
		for _, c := range clientconfig.Detect() {
			if !c.Existing {
				continue
			}
			rawServers, err := clientconfig.ReadServers(c.Path)
			if err != nil {
				continue
			}
			if _, exists := rawServers[name]; !exists {
				continue
			}
			// Delete the entry and rewrite
			delete(rawServers, name)
			if err := rewriteClientConfig(c.Path, rawServers); err != nil {
				fmt.Fprintf(os.Stderr, "%s  %s: %v\n", ui.Error.Render("Failed to update config:"), c.Name, err)
				continue
			}
			removed = true
			fmt.Printf("%s  %s (%s)\n", ui.Success.Render("✓ Removed from config:"), name, c.Name)
		}

		// 3. Remove from lockfile
		lockPath, err := lockfile.DefaultPath()
		if err == nil {
			lf, err := lockfile.Load(lockPath)
			if err == nil && lf.Has(name) {
				lf.Remove(name)
				if err := lf.Save(lockPath); err != nil {
					fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to update lockfile:"), err)
				} else {
					removed = true
					fmt.Printf("%s  %s\n", ui.Success.Render("✓ Removed from lockfile:"), lockPath)
				}
			}
		}

		if !removed {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Server not found:"), name)
			os.Exit(1)
		}

		fmt.Printf("\n%s  %s\n", ui.Success.Render("✓ Removed:"), ui.PackageName.Render(name))
	},
}

// rewriteClientConfig rewrites a client config file with the given server map.
func rewriteClientConfig(path string, servers map[string]json.RawMessage) error {
	type configFile struct {
		McpServers map[string]json.RawMessage `json:"mcpServers"`
	}
	cfg := configFile{McpServers: servers}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(path, out, 0o644)
}

func init() {
	rootCmd.AddCommand(removeCmd)
}
