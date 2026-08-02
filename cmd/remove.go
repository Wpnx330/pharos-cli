package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/canonical"
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

		// 2. Remove from canonical config (~/.pharos/mcp.json)
		if canonRemoved, err := canonical.RemoveServer(name); err != nil {
			fmt.Fprintf(os.Stderr, "%s  %v\n", ui.Error.Render("Warning: failed to update canonical config:"), err)
		} else if canonRemoved {
			removed = true
			fmt.Printf("%s\n", ui.Success.Render("✓ Removed from canonical config"))
		}

		// 3. Remove from all detected client configs
		for _, c := range clientconfig.Detect() {
			if !c.Existing {
				continue
			}
			rawServers, err := clientconfig.ReadServersFormat(c.Path, c.Format)
			if err != nil {
				continue
			}
			if _, exists := rawServers[name]; !exists {
				continue
			}
			// Delete the entry and rewrite using the client's format.
			delete(rawServers, name)
			if err := rewriteClientConfigFormat(c.Path, c.Format, rawServers); err != nil {
				fmt.Fprintf(os.Stderr, "%s  %s: %v\n", ui.Error.Render("Failed to update config:"), c.Name, err)
				continue
			}
			removed = true
			fmt.Printf("%s  %s (%s)\n", ui.Success.Render("✓ Removed from config:"), name, c.Name)
		}

		// 4. Remove from lockfile
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
// Deprecated: use rewriteClientConfigFormat for format-aware writing.
func rewriteClientConfig(path string, servers map[string]json.RawMessage) error {
	return rewriteClientConfigFormat(path, "mcpServers", servers)
}

// rewriteClientConfigFormat rewrites a client config file with the given
// server map, using the specified format ("mcpServers" or "array").
func rewriteClientConfigFormat(path, format string, servers map[string]json.RawMessage) error {
	if format == "array" {
		return rewriteArrayConfig(path, servers)
	}
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

// rewriteArrayConfig writes the servers map as a flat JSON array, each
// entry carrying a "name" field.
func rewriteArrayConfig(path string, servers map[string]json.RawMessage) error {
	type entry struct {
		Name    string          `json:"name"`
		Command string          `json:"command,omitempty"`
		Args    []string        `json:"args,omitempty"`
		Env     map[string]string `json:"env,omitempty"`
		URL     string          `json:"url,omitempty"`
		Type    string          `json:"type,omitempty"`
	}
	var entries []entry
	for name, raw := range servers {
		var m map[string]any
		e := entry{Name: name}
		if json.Unmarshal(raw, &m) == nil {
			if v, ok := m["command"].(string); ok {
				e.Command = v
			}
			if v, ok := m["url"].(string); ok {
				e.URL = v
			}
			if v, ok := m["type"].(string); ok {
				e.Type = v
			}
			if args, ok := m["args"].([]any); ok {
				for _, a := range args {
					if s, ok := a.(string); ok {
						e.Args = append(e.Args, s)
					}
				}
			}
			if env, ok := m["env"].(map[string]any); ok {
				e.Env = make(map[string]string)
				for k, v := range env {
					if s, ok := v.(string); ok {
						e.Env[k] = s
					}
				}
			}
		}
		entries = append(entries, e)
	}
	out, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(path, out, 0o644)
}

func init() {
	rootCmd.AddCommand(removeCmd)
}
