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
	"github.com/Wpnx330/pharos-cli/internal/runtime"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var (
	listRunning bool
	listSort    string
	listJSON    bool
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
		storeDir := filepath.Join(home, ".pharos", "store")
		mgr := install.NewManager(storeDir)
		pkgs, err := mgr.List()
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to list packages:"), err)
			return
		}
		if len(pkgs) == 0 {
			if listJSON {
				fmt.Println("[]")
				return
			}
			fmt.Println(ui.Muted.Render("No packages installed."))
			return
		}

		type entry struct {
			pkg    install.InstalledPackage
			launch packageLaunch
			row    listRow
			status runtime.ProcessStatus
			size   int64
		}
		entries := make([]entry, 0, len(pkgs))
		daemonState := loadDaemonState()

		for _, p := range pkgs {
			launch := loadPackageLaunch(storeDir, p)
			kind := inferLaunchKind(launch)
			port := 0
			if kind == kindLocalHTTP {
				port = kind2ListenPort(p.Name, launch, daemonState)
			}

			st := runtime.ProbeStatus(p.Name, port)
			if kind == kindRemote {
				// Never treat a publisher URL as a local process.
				st = runtime.ProcessStatus{}
			} else if kind == kindLocalHTTP {
				st = applyKind2DaemonRunning(p.Name, st, daemonState)
				if st.Running && st.Port == 0 && port > 0 {
					st.Port = port
				}
			}

			if listRunning && (kind == kindRemote || !st.Running) {
				continue
			}

			var size int64
			if kind != kindRemote && p.Location != "" {
				size = dirSize(p.Location)
			}

			row := buildListRow(p, launch, st, size, daemonState)
			entries = append(entries, entry{pkg: p, launch: launch, row: row, status: st, size: size})
		}

		if len(entries) == 0 {
			if listJSON {
				fmt.Println("[]")
				return
			}
			fmt.Println(ui.Muted.Render("No running servers."))
			return
		}

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

		rows := make([]listRow, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, e.row)
		}

		if listJSON {
			data, err := marshalListJSON(rows)
			if err != nil {
				fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to encode JSON:"), err)
				return
			}
			fmt.Println(string(data))
			return
		}

		cols := listTableColumns()

		var tableRows []ui.TableRow
		for _, r := range rows {
			statusStr := r.Status
			switch r.Status {
			case "running", "connected":
				statusStr = ui.Success.Render(r.Status)
			default:
				statusStr = ui.Muted.Render(r.Status)
			}
			portStr := r.Port
			if portStr == listDash || portStr == "" {
				portStr = ui.Muted.Render(listDash)
			}
			sizeStr := r.Size
			if sizeStr == listDash || sizeStr == "" {
				sizeStr = ui.Muted.Render(listDash)
			}
			memStr := r.Memory
			if memStr == listDash || memStr == "" {
				memStr = ui.Muted.Render(listDash)
			}
			uptimeStr := r.Uptime
			if uptimeStr == listDash || uptimeStr == "" {
				uptimeStr = ui.Muted.Render(listDash)
			}
			idleStr := r.Idle
			if idleStr == listDash || idleStr == "" {
				idleStr = ui.Muted.Render(listDash)
			}
			lastActStr := r.LastActivity
			if lastActStr == listDash || lastActStr == "" {
				lastActStr = ui.Muted.Render(listDash)
			}
			ep := r.Endpoint
			if ep == "" {
				ep = ui.Muted.Render(listDash)
			}
			tableRows = append(tableRows, ui.TableRow{
				ui.PackageName.Render(r.Name),
				r.Version,
				r.Transport,
				statusStr,
				portStr,
				sizeStr,
				memStr,
				uptimeStr,
				idleStr,
				lastActStr,
				ep,
			})
		}
		fmt.Print(ui.RenderTable(cols, tableRows))
	},
}

func init() {
	listCmd.Flags().BoolVar(&listRunning, "running", false, "show only running servers")
	listCmd.Flags().StringVar(&listSort, "sort", "name", "sort by: name, size, port, memory, uptime")
	listCmd.Flags().BoolVar(&listJSON, "json", false, "output as JSON (kind, status, endpoint, metrics)")
	rootCmd.AddCommand(listCmd)
}

// listTableColumns is the human table header. KIND is an internal
// classifier and must not appear here. ENDPOINT is last.
func listTableColumns() []ui.TableColumn {
	return []ui.TableColumn{
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
		{Title: "ENDPOINT", Width: 18, MaxWidth: 32},
	}
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

// =============================================================
// Daemon state helpers
// =============================================================

// daemonServerState is the per-server entry in ~/.pharos/daemon.json.
// It mirrors the relevant fields from daemon.ServerState but is kept
// minimal so we can parse just what we need for the list table.
// Port is the daemon *proxy* port, not the backing listen port.
type daemonServerState struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	PID          int    `json:"pid"`
	Port         int    `json:"port"`
	LastActivity string `json:"lastActivity"` // RFC 3339 timestamp
}

// loadDaemonState reads ~/.pharos/daemon.json and returns a map of
// server name → daemon state. If the file doesn't exist or can't be
// parsed, returns an empty map (all idle/activity columns show "—").
func loadDaemonState() map[string]daemonServerState {
	home, err := os.UserHomeDir()
	if err != nil {
		return map[string]daemonServerState{}
	}

	data, err := os.ReadFile(filepath.Join(home, ".pharos", "daemon.json"))
	if err != nil {
		return map[string]daemonServerState{}
	}

	return parseDaemonState(data)
}

// parseDaemonState unmarshals daemon.json. Canonical shape is
// DaemonState: {pid, startedAt, servers: map[name]ServerState}.
// There is no top-level "running" bool — the daemon is treated as up
// when the file parses (typically pid > 0). A legacy servers array is
// still accepted so older fixtures keep working.
func parseDaemonState(data []byte) map[string]daemonServerState {
	result := make(map[string]daemonServerState)
	if len(data) == 0 {
		return result
	}

	var envelope struct {
		PID     int             `json:"pid"`
		Servers json.RawMessage `json:"servers"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return result
	}
	_ = envelope.PID // pid>0 means the writer thought the daemon was up; overlay still uses servers

	if len(envelope.Servers) == 0 || string(envelope.Servers) == "null" {
		return result
	}

	var asMap map[string]daemonServerState
	if err := json.Unmarshal(envelope.Servers, &asMap); err == nil && asMap != nil {
		for name, s := range asMap {
			if strings.TrimSpace(s.Name) == "" {
				s.Name = name
			}
			key := s.Name
			if key == "" {
				key = name
			}
			result[key] = s
		}
		return result
	}

	var asArray []daemonServerState
	if err := json.Unmarshal(envelope.Servers, &asArray); err != nil {
		return result
	}
	for _, s := range asArray {
		if s.Name == "" {
			continue
		}
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
