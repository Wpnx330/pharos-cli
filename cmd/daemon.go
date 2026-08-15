package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/daemon"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the Pharos daemon (MCP server process supervisor)",
	Long: ui.Label.Render("pharos daemon") + ` — background process supervisor for MCP servers.

The daemon manages HTTP/SSE/streamable-http servers with JIT loading:
servers start on first request and auto-unload after configurable idle time.

stdio servers are NOT managed by the daemon — MCP clients handle those.

Examples:
  pharos daemon start    # start the daemon
  pharos daemon stop     # stop daemon + all managed servers
  pharos daemon status   # show daemon and server status`,
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Pharos daemon",
	Run:   runDaemonStart,
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the Pharos daemon and all managed servers",
	Run:   runDaemonStop,
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status and managed server details",
	Run:   runDaemonStatus,
}

func init() {
	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	daemonCmd.AddCommand(daemonStatusCmd)
	rootCmd.AddCommand(daemonCmd)
}

// runDaemonStart starts the daemon. It blocks until SIGTERM/SIGINT.
// If the daemon is already running, it prints an error.
func runDaemonStart(cmd *cobra.Command, args []string) {
	fmt.Printf("%s  starting daemon...\n", ui.Label.Render("pharos daemon"))
	if err := daemon.Start(); err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to start daemon:"), err)
		os.Exit(1)
	}
}

// runDaemonStop sends SIGTERM to the running daemon, causing it to
// gracefully shut down all managed servers and exit.
func runDaemonStop(cmd *cobra.Command, args []string) {
	fmt.Printf("%s  stopping daemon...\n", ui.Label.Render("pharos daemon"))
	if err := daemon.Stop(); err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to stop daemon:"), err)
		os.Exit(1)
	}
	fmt.Printf("%s  daemon stopped\n", ui.Success.Render("✓"))
}

// runDaemonStatus queries the daemon and prints its status plus a table
// of managed servers.
func runDaemonStatus(cmd *cobra.Command, args []string) {
	status, err := daemon.Status()
	if err != nil {
		// Status returns an error when the daemon is not running (stub)
		// or can't be reached. Either way, show a clear message.
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot get daemon status:"), err)
		os.Exit(1)
	}

	if !status.Running {
		fmt.Println(ui.Muted.Render("Daemon is not running."))
		fmt.Printf("\n  %s  %s\n", ui.Muted.Render("Start it with:"), "pharos daemon start")
		return
	}

	// Daemon summary
	fmt.Printf("%s  %s\n", ui.Success.Render("✓ Daemon is running"),
		fmt.Sprintf("(PID %d, port %d)", status.PID, status.Port))

	if !status.StartedAt.IsZero() {
		fmt.Printf("  %s  %s\n", ui.Muted.Render("Started:"), formatTimeAgo(status.StartedAt))
	}

	if len(status.Servers) == 0 {
		fmt.Printf("\n%s\n", ui.Muted.Render("No servers managed by daemon."))
		return
	}

	// Server table
	cols := []ui.TableColumn{
		{Title: "NAME", Width: 22, MaxWidth: 0},
		{Title: "STATE", Width: 10, MaxWidth: 10},
		{Title: "IDLE", Width: 9, MaxWidth: 9},
		{Title: "PORT", Width: 7, MaxWidth: 7},
		{Title: "MEMORY", Width: 9, MaxWidth: 9},
		{Title: "LAST ACTIVITY", Width: 14, MaxWidth: 14},
	}

	var rows []ui.TableRow
	for _, s := range status.Servers {
		name := ui.PackageName.Render(s.Name)

		// State styling
		var stateStr string
		switch s.State {
		case "loaded", "running":
			stateStr = ui.Success.Render(s.State)
		case "starting":
			stateStr = ui.Label.Render(s.State)
		case "error":
			stateStr = ui.Error.Render(s.State)
		default:
			stateStr = ui.Muted.Render(s.State)
		}

		// Port
		portStr := ui.Muted.Render("—")
		if s.Port > 0 {
			portStr = fmt.Sprintf("%d", s.Port)
		}

		// Memory
		memStr := ui.Muted.Render("—")
		if s.Memory > 0 {
			memStr = ui.FormatBytes(s.Memory)
		}

		// Idle + Last Activity
		var idleStr, lastActStr string
		if s.LastActivity.IsZero() {
			idleStr = ui.Muted.Render("—")
			lastActStr = ui.Muted.Render("—")
		} else {
			idleStr = formatDuration(time.Since(s.LastActivity))
			lastActStr = formatTimeAgo(s.LastActivity)
		}

		rows = append(rows, ui.TableRow{
			name, stateStr, idleStr, portStr, memStr, lastActStr,
		})
	}

	fmt.Printf("\n%s\n", ui.Label.Render("Managed servers:"))
	fmt.Print(ui.RenderTable(cols, rows))
}
