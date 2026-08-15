package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Wpnx330/pharos-cli/internal/canonical"
	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/daemon"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/manifest"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

// removeForce holds the value of the --force flag.
var removeForce bool

var removeCmd = &cobra.Command{
	Use:     "remove <name>",
	Aliases: []string{"rm", "uninstall"},
	Short:   "Remove an installed MCP server",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		removed := false

		// 0. Dependency protection — block removal if other installed
		// packages declare this one as a dependency, unless --force.
		storeDir, err := os.UserHomeDir()
		if err == nil {
			pharosStore := filepath.Join(storeDir, ".pharos", "store")
			lockPath, lockErr := lockfile.DefaultPath()
			if lockErr == nil {
				if err := checkDependencies(pharosStore, lockPath, name, removeForce); err != nil {
					fmt.Fprintln(os.Stderr, ui.Error.Render("Error:"), err)
					os.Exit(1)
				}
			}
		}

		// 0a. If the daemon is running and managing this server,
		// send a reload signal so it reconciles (removes the proxy listener).
		if daemonStatus, derr := daemon.Status(); derr == nil && daemonStatus.Running {
			_ = daemon.ReloadDaemon(daemonStatus.PID)
		}

		// 1. Remove from store (~/.pharos/store/{name}/)
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
// server map, using the specified format ("mcpServers", "array", or
// "hermes-yaml").
func rewriteClientConfigFormat(path, format string, servers map[string]json.RawMessage) error {
	if format == "array" {
		return rewriteArrayConfig(path, servers)
	}
	if format == "hermes-yaml" {
		return rewriteHermesConfig(path, servers)
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
	return clientconfig.SafeWriteConfig(path, out, "mcpServers")
}

// rewriteHermesConfig writes the servers map back to a Hermes config.yaml
// in YAML format with a top-level mcp_servers: key. All existing
// top-level keys (provider settings, etc.) are preserved.
func rewriteHermesConfig(path string, servers map[string]json.RawMessage) error {
	// Read existing config to preserve non-mcp_servers keys.
	var root map[string]any
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	if root == nil {
		root = make(map[string]any)
	}

	// Build the mcp_servers map from the raw JSON entries.
	yamlServers := make(map[string]any)
	for name, raw := range servers {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil {
			yamlServers[name] = m
		}
	}
	root["mcp_servers"] = yamlServers

	out, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return clientconfig.SafeWriteConfig(path, out, "hermes-yaml")
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
	return clientconfig.SafeWriteConfig(path, out, "mcpServers")
}

// installedMeta is a local representation of .pharos-installed.json that
// includes dependency information if the metadata file carries it.
type installedMeta struct {
	Name         string                 `json:"name"`
	Version      string                 `json:"version"`
	Dependencies []manifest.Dependency  `json:"dependencies,omitempty"`
}

// findDependents returns the sorted names of installed packages that
// declare target as a dependency. It scans the store directory directly
// (not the lockfile) so it catches packages even if the lockfile is stale
// or missing. For each package directory it reads the version
// subdirectories and checks both .pharos-installed.json and pharos.json.
func findDependents(storeDir, lockPath, target string) ([]string, error) {
	entries, err := os.ReadDir(storeDir)
	if err != nil {
		return nil, err
	}
	var dependents []string
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == target {
			continue
		}
		// Check all version subdirectories of this package.
		pkgPath := filepath.Join(storeDir, entry.Name())
		versions, err := os.ReadDir(pkgPath)
		if err != nil {
			continue
		}
		for _, v := range versions {
			if !v.IsDir() {
				continue
			}
			if packageDependsOn(storeDir, entry.Name(), v.Name(), target) {
				dependents = append(dependents, entry.Name())
				break // one version match is enough
			}
		}
	}
	sort.Strings(dependents)
	return dependents, nil
}

// packageDependsOn reports whether pkgName@version declares target as a
// dependency. It checks both the .pharos-installed.json metadata file
// and the pharos.json manifest in the package's store directory.
func packageDependsOn(storeDir, pkgName, version, target string) bool {
	pkgDir := filepath.Join(storeDir, pkgName, version)

	// Check .pharos-installed.json for a dependencies field.
	if deps := depsFromInstalledMeta(pkgDir); deps != nil {
		for _, d := range deps {
			if d.Name == target {
				return true
			}
		}
	}

	// Check pharos.json manifest (extracted from the tarball at install).
	if m, err := manifest.Load(pkgDir); err == nil {
		for _, d := range m.Dependencies {
			if d.Name == target {
				return true
			}
		}
	}

	return false
}

// depsFromInstalledMeta reads the .pharos-installed.json file from pkgDir
// and returns its dependencies list, or nil if the file is missing or
// cannot be parsed.
func depsFromInstalledMeta(pkgDir string) []manifest.Dependency {
	path := filepath.Join(pkgDir, ".pharos-installed.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var meta installedMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil
	}
	return meta.Dependencies
}

// checkDependencies returns an error if removal of target should be
// blocked because other installed packages depend on it. When force is
// true the check is bypassed and a warning is printed to stderr instead.
func checkDependencies(storeDir, lockPath, target string, force bool) error {
	dependents, err := findDependents(storeDir, lockPath, target)
	if err != nil {
		// Don't block removal on lockfile read errors — just proceed.
		return nil
	}
	if len(dependents) == 0 {
		return nil
	}
	if force {
		fmt.Fprintf(os.Stderr, "%s  %s is a required dependency of %s — removing anyway\n",
			ui.Warning.Render("⚠"), target, strings.Join(dependents, ", "))
		return nil
	}
	return fmt.Errorf("cannot remove %s: it is a required dependency of %s",
		target, strings.Join(dependents, ", "))
}

func init() {
	rootCmd.AddCommand(removeCmd)
	removeCmd.Flags().BoolVarP(&removeForce, "force", "f", false,
		"Force removal even if other packages depend on it")
}
