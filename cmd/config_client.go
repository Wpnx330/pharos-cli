package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var addClientFormat string

// addClientCmd registers a custom MCP client.
var addClientCmd = &cobra.Command{
	Use:   "add-client <id> --path <path> [--format mcpServers|array]",
	Short: "Register a custom MCP client for auto-detection",
	Long: ui.Label.Render("pharos config add-client") + ` registers a custom MCP client
so that ` + "`pharos install`" + ` can write server configs to it.

Custom clients are stored in ~/.pharos/config.json under "custom_clients".

  --path     (required) absolute path to the client's MCP config file
  --format   config format: "mcpServers" (default) or "array"

The "mcpServers" format writes {"mcpServers": {...}} (Claude Desktop /
Cursor style). The "array" format writes a flat JSON array of server
entries, each with a "name" field.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		path := cmd.Flag("path").Value.String()

		if path == "" {
			fmt.Fprintln(os.Stderr, ui.Error.Render("✗ --path is required"))
			os.Exit(1)
		}

		// Validate that path is absolute.
		if !filepath.IsAbs(path) {
			fmt.Fprintf(os.Stderr, "%s  --path must be an absolute path (got %q)\n",
				ui.Error.Render("✗"), path)
			os.Exit(1)
		}

		// Normalize the format.
		format := addClientFormat
		if format == "" {
			format = clientconfig.FormatMcpServers
		}
		if format != clientconfig.FormatMcpServers && format != clientconfig.FormatArray {
			fmt.Fprintf(os.Stderr, "%s  --format must be %q or %q (got %q)\n",
				ui.Error.Render("✗"), clientconfig.FormatMcpServers, clientconfig.FormatArray, format)
			os.Exit(1)
		}

		cfg, _ := loadConfig()
		if err := cfg.AddCustomClient(id, path, format); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("✗ "+err.Error()))
			os.Exit(1)
		}
		if err := cfg.Save(); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("✗ Failed to save config:"), err)
			os.Exit(1)
		}

		fmt.Printf("%s  Registered custom client '%s' at %s\n",
			ui.Success.Render("✓"), id, path)
	},
}

// removeClientCmd removes a custom MCP client registration.
var removeClientCmd = &cobra.Command{
	Use:   "remove-client <id>",
	Short: "Remove a registered custom MCP client",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		cfg, _ := loadConfig()
		if !cfg.RemoveCustomClient(id) {
			fmt.Printf("%s  No custom client '%s' found\n",
				ui.Error.Render("✗"), id)
			os.Exit(1)
		}
		if err := cfg.Save(); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("✗ Failed to save config:"), err)
			os.Exit(1)
		}
		fmt.Printf("%s  Removed custom client '%s'\n",
			ui.Success.Render("✓"), id)
	},
}

// listClientsCmd lists all known MCP clients: built-in (with detection
// status) and custom.
var listClientsCmd = &cobra.Command{
	Use:   "list-clients",
	Short: "List all known MCP clients (built-in and custom)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		cfg, _ := loadConfig()

		var b strings.Builder

		// --- Built-in clients ---
		b.WriteString(ui.Label.Render("BUILT-IN") + "\n")
		for _, c := range clientconfig.CandidatePaths() {
			status := ui.Muted.Render("not found")
			if _, err := os.Stat(c.Path); err == nil {
				status = ui.Success.Render("detected")
			} else if dirExistsClient(filepath.Dir(c.Path)) {
				status = ui.Muted.Render("dir found")
			}
			b.WriteString(fmt.Sprintf("  %-18s %s    %s\n",
				c.Name, c.Path, status))
		}

		// --- Custom clients ---
		b.WriteString("\n" + ui.Label.Render("CUSTOM") + "\n")
		if len(cfg.CustomClients) == 0 {
			b.WriteString("  " + ui.Muted.Render("(none — use 'pharos config add-client' to register one)") + "\n")
		} else {
			for _, cc := range cfg.CustomClients {
				format := cc.Format
				if format == "" {
					format = clientconfig.FormatMcpServers
				}
				status := ui.Muted.Render("not found")
				if _, err := os.Stat(cc.Path); err == nil {
					status = ui.Success.Render("detected")
				}
				b.WriteString(fmt.Sprintf("  %-18s %s    %s\n",
					cc.ID, cc.Path, format+"  "+status))
			}
		}

		fmt.Print(b.String())
	},
}

// dirExistsClient is a small helper mirroring clientconfig.dirExists
// without exporting it.
func dirExistsClient(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func init() {
	addClientCmd.Flags().StringVar(&addClientFormat, "format", "mcpServers",
		"config format: mcpServers or array")
	addClientCmd.Flags().String("path", "", "absolute path to the client's MCP config file (required)")

	configCmd.AddCommand(addClientCmd)
	configCmd.AddCommand(removeClientCmd)
	configCmd.AddCommand(listClientsCmd)
}
