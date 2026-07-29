package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/install"
	"github.com/Wpnx330/pharos-cli/internal/manifest"
	"github.com/Wpnx330/pharos-cli/internal/runtime"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var (
	listRunning bool
	listSort    string
)

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List locally installed packages",
	Run: func(cmd *cobra.Command, args []string) {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot determine home directory:"), err)
			return
		}
		mgr := install.NewManager(filepath.Join(home, ".pharos", "store"))
		pkgs, err := mgr.List()
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to list packages:"), err)
			return
		}
		if len(pkgs) == 0 {
			fmt.Println(ui.Muted.Render("No packages installed."))
			return
		}

		// Build enriched rows with runtime status
		type entry struct {
			pkg    install.InstalledPackage
			status runtime.ProcessStatus
			size   int64
		}
		entries := make([]entry, 0, len(pkgs))

		for _, p := range pkgs {
			// Read the manifest to get transport and port info
			var port int
			manifestPath := filepath.Join(p.Location, "pharos.json")
			if data, err := os.ReadFile(manifestPath); err == nil {
				if m, err := manifest.Parse(data); err == nil {
					if m.Transport == "http-sse" || m.Transport == "http" {
						port = runtime.ExtractPort(m.RunCommand())
					}
				}
			}

			st := runtime.ProbeStatus(p.Name, port)

			// Filter: --running flag
			if listRunning && !st.Running {
				continue
			}

			// Disk size
			var size int64
			if p.Location != "" {
				size = dirSize(p.Location)
			}

			entries = append(entries, entry{pkg: p, status: st, size: size})
		}

		if len(entries) == 0 {
			fmt.Println(ui.Muted.Render("No running servers."))
			return
		}

		// Sort
		sort.Slice(entries, func(i, j int) bool {
			switch listSort {
			case "size":
				return entries[i].size > entries[j].size
			case "port":
				return entries[i].status.Port < entries[j].status.Port
			case "memory":
				return entries[i].status.Memory > entries[j].status.Memory
			case "uptime":
				return entries[i].status.Uptime > entries[j].status.Uptime
			default: // "name"
				return entries[i].pkg.Name < entries[j].pkg.Name
			}
		})

		// Render table
		cols := []ui.TableColumn{
			{Title: "NAME", Width: 22},
			{Title: "VERSION", Width: 10},
			{Title: "TRANSPORT", Width: 11},
			{Title: "STATUS", Width: 10},
			{Title: "PORT", Width: 7},
			{Title: "SIZE", Width: 9},
			{Title: "MEMORY", Width: 9},
			{Title: "UPTIME", Width: 9},
		}
		var rows []ui.TableRow
		for _, e := range entries {
			name := ui.PackageName.Render(e.pkg.Name)
			version := e.pkg.Version
			transport := e.pkg.Transport

			var statusStr, portStr, memStr, uptimeStr string
			if e.status.Running {
				statusStr = ui.Success.Render("running")
				if e.status.Port > 0 {
					portStr = fmt.Sprintf("%d", e.status.Port)
				}
				if e.status.Memory > 0 {
					memStr = ui.FormatBytes(e.status.Memory)
				}
				uptimeStr = e.status.Uptime
			} else {
				statusStr = ui.Muted.Render("stopped")
			}

			sizeStr := ui.FormatBytes(e.size)
			if e.size == 0 {
				sizeStr = ui.Muted.Render("—")
			}

			if e.pkg.Transport == "stdio" || e.pkg.Transport == "" {
				portStr = ui.Muted.Render("—")
			}

			rows = append(rows, ui.TableRow{
				name, version, transport, statusStr, portStr, sizeStr, memStr, uptimeStr,
			})
		}
		fmt.Print(ui.RenderTable(cols, rows))
	},
}

func init() {
	listCmd.Flags().BoolVar(&listRunning, "running", false, "show only running servers")
	listCmd.Flags().StringVar(&listSort, "sort", "name", "sort by: name, size, port, memory, uptime")
	rootCmd.AddCommand(listCmd)
}

// dirSize calculates the total size of all files in a directory (recursively).
func dirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}

// Ensure strings import is used (for future filter extensions)
var _ = strings.TrimSpace
