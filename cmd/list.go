package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
			{Title: "NAME", Width: 22, MaxWidth: 0},
			{Title: "VERSION", Width: 10, MaxWidth: 10},
			{Title: "TRANSPORT", Width: 11, MaxWidth: 11},
			{Title: "STATUS", Width: 10, MaxWidth: 10},
			{Title: "PORT", Width: 7, MaxWidth: 7},
			{Title: "SIZE", Width: 9, MaxWidth: 9},
			{Title: "MEMORY", Width: 9, MaxWidth: 9},
			{Title: "UPTIME", Width: 9, MaxWidth: 9},
			{Title: "IDLE", Width: 9, MaxWidth: 9},
			{Title: "LAST ACTIVITY", Width: 14, MaxWidth: 14},
		}
		// Load daemon state for idle/last-activity columns.
		// The daemon writes ~/.pharos/daemon.json with per-server
		// lastActivity timestamps. If the daemon isn't running or the
		// file doesn't exist, both columns show "—".
		daemonState := loadDaemonState()

		var rows []ui.TableRow
		for _, e := range entries {
			name := ui.PackageName.Render(e.pkg.Name)
			version := e.pkg.Version
			transport := e.pkg.Transport

			var statusStr, portStr, memStr, uptimeStr string
			isStdio := e.pkg.Transport == "stdio" || e.pkg.Transport == ""
			if e.status.Running {
				statusStr = ui.Success.Render("running")
				if e.status.Port > 0 {
					portStr = fmt.Sprintf("%d", e.status.Port)
				}
				if e.status.Memory > 0 {
					memStr = ui.FormatBytes(e.status.Memory)
				}
				uptimeStr = e.status.Uptime
			} else if isStdio {
				// stdio servers don't have a standalone lifecycle — they're
				// spawned by MCP clients on demand. "idle" communicates that
				// the package is installed and ready, not broken or stopped.
				statusStr = ui.Muted.Render("idle")
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

			// Idle time and last activity from daemon state.
			// Only daemon-managed servers (http/sse/streamable-http
			// with the daemon running) have this data.
			// stdio and stopped servers show "—".
			var idleStr, lastActStr string
			if ds, ok := daemonState[e.pkg.Name]; ok && ds.LastActivity != "" {
				if t, err := time.Parse(time.RFC3339, ds.LastActivity); err == nil {
					idleStr = formatDuration(time.Since(t))
					lastActStr = formatTimeAgo(t)
				} else {
					idleStr = ui.Muted.Render("—")
					lastActStr = ui.Muted.Render("—")
				}
			} else {
				idleStr = ui.Muted.Render("—")
				lastActStr = ui.Muted.Render("—")
			}

			rows = append(rows, ui.TableRow{
				name, version, transport, statusStr, portStr, sizeStr, memStr, uptimeStr,
				idleStr, lastActStr,
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

// =============================================================
// Daemon state helpers
// =============================================================

// daemonServerState is the per-server entry in ~/.pharos/daemon.json.
// It mirrors the relevant fields from daemon.ServerStatus but is kept
// minimal so we can parse just what we need for the list table.
type daemonServerState struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	Port         int    `json:"port"`
	LastActivity string `json:"lastActivity"` // RFC 3339 timestamp
}

// daemonStateFile represents the on-disk structure of ~/.pharos/daemon.json.
type daemonStateFile struct {
	Running bool               `json:"running"`
	PID     int                `json:"pid"`
	Port    int                `json:"port"`
	Servers []daemonServerState `json:"servers"`
}

// loadDaemonState reads ~/.pharos/daemon.json and returns a map of
// server name → daemon state. If the file doesn't exist or can't be
// parsed, returns an empty map (all idle/activity columns show "—").
func loadDaemonState() map[string]daemonServerState {
	result := make(map[string]daemonServerState)

	home, err := os.UserHomeDir()
	if err != nil {
		return result
	}

	data, err := os.ReadFile(filepath.Join(home, ".pharos", "daemon.json"))
	if err != nil {
		return result
	}

	var dsf daemonStateFile
	if err := json.Unmarshal(data, &dsf); err != nil {
		return result
	}

	if !dsf.Running {
		return result
	}

	for _, s := range dsf.Servers {
		result[s.Name] = s
	}

	return result
}

// formatTimeAgo renders a time as a human-readable relative string
// (e.g., "just now", "5m ago", "2h ago").
func formatTimeAgo(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh ago", int(d.Hours()))
}

// formatDuration renders a duration as a compact human-readable string
// (e.g., "2m", "1h 32m", "3h").
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) - hours*60
	if mins == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh %dm", hours, mins)
}
