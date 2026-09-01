package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var importAsUnmanaged bool
var importClient string
var importJSON bool

// importEntry is one server row in the import JSON report.
type importEntry struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Status  string `json:"status"` // "resolved" | "unresolved"
}

// importReport is the JSON shape of `import --json`.
type importReport struct {
	Lockfile   string        `json:"lockfile"`
	Resolved   int           `json:"resolved"`
	Unresolved int           `json:"unresolved"`
	Servers    []importEntry `json:"servers"`
}

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Import existing MCP server configs into a pharos.lock",
	Long: ui.Label.Render("pharos import") + ` reads MCP client configs (Claude Desktop, Cursor, VS Code, Windsurf, Gemini, Amazon Q, Cline, Roo Code, OpenCode, Hermes, Codex, Grok, Zed, Aider, generic)
and resolves each listed server against the PHAROS registry, populating
pharos.lock with resolved versions and integrity hashes.

Use --client <id> to import from a specific client only (claude-desktop, cursor, vscode, windsurf, gemini, amazonq, roo-code, cline, opencode, hermes, codex, grok, zed, aider, generic).
Use --as-unmanaged to track unresolved servers without dropping them.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, client := loadConfig()

		// Detect clients
		var clients []clientconfig.Client
		if importClient != "" {
			c := clientconfig.DetectByID(clientconfig.ClientID(importClient))
			if c == nil {
				fmt.Fprintf(os.Stderr, "%s  client %q not detected\n", ui.Error.Render("Error:"), importClient)
				return nil
			}
			clients = append(clients, *c)
		} else {
			clients = clientconfig.Detect()
		}

		if len(clients) == 0 {
			if JSONRequested() {
				return printImportJSON(&importReport{Servers: []importEntry{}})
			}
			fmt.Println(ui.Muted.Render("No MCP client configs found."))
			return nil
		}

		// Load or create lockfile
		lockPath, err := lockfile.DefaultPath()
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot determine lockfile path:"), err)
			return nil
		}
		lf, err := lockfile.Load(lockPath)
		if err != nil {
			lf = lockfile.New()
		}

		var resolved, unresolved int
		var reportLines []string
		report := &importReport{Lockfile: lockPath, Servers: []importEntry{}}

		for _, c := range clients {
			if !JSONRequested() {
				fmt.Printf("%s  %s (%s)\n", ui.Label.Render("Scanning:"), c.Name, c.Path)
			}
			rawServers, err := clientconfig.ReadServersFormat(c.Path, c.Format)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  %s  %v\n", ui.Error.Render("read error:"), err)
				continue
			}

			// Sort server names for deterministic output
			names := make([]string, 0, len(rawServers))
			for n := range rawServers {
				names = append(names, n)
			}
			sort.Strings(names)

			for _, name := range names {
				pkg, err := client.GetPackage(name)
				if err != nil {
					unresolved++
					report.Unresolved++
					report.Servers = append(report.Servers, importEntry{Name: name, Status: "unresolved"})
					reportLines = append(reportLines, fmt.Sprintf("  %s  %s — %s",
						ui.Muted.Render("?"),
						name,
						ui.Muted.Render("not found in registry")))
					if importAsUnmanaged {
						lf.Set(name, lockfile.ServerEntry{})
					}
					continue
				}
				resolved++
				version := ""
				if pkg.DistTags != nil {
					version = pkg.DistTags["latest"]
				}
				transport := ""
				if len(pkg.Versions) > 0 {
					transport = pkg.Versions[0].Manifest.Transport
				}
				integrity := ""
				if vd := pkg.FindVersion(version); vd != nil {
					integrity = vd.Manifest.Integrity
				}
				lf.Set(name, lockfile.ServerEntry{
					Version:   version,
					Integrity: integrity,
					Transport: transport,
				})
				report.Resolved++
				report.Servers = append(report.Servers, importEntry{Name: name, Version: version, Status: "resolved"})
				reportLines = append(reportLines, fmt.Sprintf("  %s  %s@%s",
					ui.Success.Render("✓"),
					ui.PackageName.Render(name),
					version))
			}
		}

		if err := lf.Save(lockPath); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to write lockfile:"), err)
			return nil
		}

		if JSONRequested() {
			return printImportJSON(report)
		}

		fmt.Printf("\n%s\n", strings.Join(reportLines, "\n"))
		fmt.Printf("\n%s  %d resolved, %d unresolved\n",
			ui.Success.Render("✓ Import complete."),
			resolved,
			unresolved)
		fmt.Printf("%s  %s\n", ui.Muted.Render("Lockfile:"), lockPath)
		if unresolved > 0 && !importAsUnmanaged {
			fmt.Printf("%s  %s\n", ui.Muted.Render("Tip:"), "use --as-unmanaged to track unresolved servers.")
		}
		return nil
	},
}

// printImportJSON emits the import report as JSON to stdout.
func printImportJSON(report *importReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func init() {
	importCmd.Flags().BoolVar(&importAsUnmanaged, "as-unmanaged", false, "track unresolved servers without dropping them")
	importCmd.Flags().StringVar(&importClient, "client", "", "import from a specific client only (claude-desktop, cursor, generic)")
	importCmd.Flags().BoolVar(&importJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(importCmd)
}
