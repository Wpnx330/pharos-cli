package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
  pharos daemon start    # start the daemon (background)
  pharos daemon stop     # stop daemon + all managed servers
  pharos daemon status   # show daemon and server status
  pharos daemon log      # show recent daemon log output
  pharos daemon restart  # restart the daemon
  pharos daemon autostart --on   # enable autostart on boot
  pharos daemon autostart --off  # disable autostart on boot`,
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Pharos daemon",
	Long:  "Start the Pharos daemon in the background. Use --foreground to run in the foreground (for debugging).",
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
	RunE:  runDaemonStatus,
}

var daemonLogCmd = &cobra.Command{
	Use:   "log",
	Short: "Show recent daemon log output",
	Run:   runDaemonLog,
}

var daemonRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the Pharos daemon",
	Run:   runDaemonRestart,
}

var daemonAutostartCmd = &cobra.Command{
	Use:   "autostart",
	Short: "Enable or disable daemon autostart on boot",
	Long:  "Enable or disable the Pharos daemon to start automatically on system boot.\n\n  pharos daemon autostart --on   # enable autostart\n  pharos daemon autostart --off  # disable autostart\n  pharos daemon autostart        # show current status",
	Run:   runDaemonAutostart,
}

var daemonForeground bool
var daemonAutostartOn bool
var daemonAutostartOff bool
var daemonLogLines int
var daemonInternalFlag bool // hidden flag for background re-exec

func init() {
	daemonStartCmd.Flags().BoolVar(&daemonForeground, "foreground", false, "run in foreground (for debugging)")
	daemonStartCmd.Flags().BoolVar(&daemonInternalFlag, "daemon-internal", false, "internal: run the actual daemon loop")
	daemonStartCmd.Flags().MarkHidden("daemon-internal")

	daemonLogCmd.Flags().IntVarP(&daemonLogLines, "lines", "n", 50, "number of lines to show")

	daemonStatusCmd.Flags().BoolVar(&daemonStatusJSON, "json", false, "output as JSON")

	daemonAutostartCmd.Flags().BoolVar(&daemonAutostartOn, "on", false, "enable autostart on boot")
	daemonAutostartCmd.Flags().BoolVar(&daemonAutostartOff, "off", false, "disable autostart on boot")

	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	daemonCmd.AddCommand(daemonStatusCmd)
	daemonCmd.AddCommand(daemonLogCmd)
	daemonCmd.AddCommand(daemonRestartCmd)
	daemonCmd.AddCommand(daemonAutostartCmd)
	rootCmd.AddCommand(daemonCmd)
}

// runDaemonStart starts the daemon. By default it backgrounds itself by
// re-executing the binary with the hidden --daemon-internal flag. Use
// --foreground to run in the foreground (for debugging).
func runDaemonStart(cmd *cobra.Command, args []string) {
	// If invoked with --daemon-internal, we ARE the daemon — run the loop.
	if daemonInternalFlag {
		if err := daemon.Start(); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to start daemon:"), err)
			os.Exit(1)
		}
		return
	}

	// Check if already running
	status, _ := daemon.Status()
	if status != nil && status.Running {
		fmt.Fprintf(os.Stderr, "%s  daemon already running (PID %d)\n",
			ui.Error.Render("✗"), status.PID)
		os.Exit(1)
	}

	if daemonForeground {
		fmt.Printf("%s  starting daemon (foreground)...\n", ui.Label.Render("pharos daemon"))
		if err := daemon.Start(); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to start daemon:"), err)
			os.Exit(1)
		}
		return
	}

	// Background mode: re-exec ourselves with --daemon-internal
	fmt.Printf("%s  starting daemon (background)...\n", ui.Label.Render("pharos daemon"))

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot determine executable path:"), err)
		os.Exit(1)
	}

	// Build the command: <exe> daemon start --daemon-internal
	bgCmd := exec.Command(exe, "daemon", "start", "--daemon-internal")
	bgCmd.Stdin = nil
	bgCmd.Stdout = nil
	bgCmd.Stderr = nil
	// Detach from terminal
	detachProcess(bgCmd)

	if err := bgCmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to background daemon:"), err)
		os.Exit(1)
	}

	// Wait briefly for daemon to write its PID file
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := daemon.Status()
		if s != nil && s.Running {
			fmt.Printf("%s  daemon started (PID %d)\n", ui.Success.Render("✓"), s.PID)
			fmt.Printf("  %s  %s\n", ui.Muted.Render("Logs:"), "~/.pharos/daemon.log")
			fmt.Printf("  %s  %s\n", ui.Muted.Render("Status:"), "pharos daemon status")
			return
		}
		time.Sleep(200 * time.Millisecond)
	}

	fmt.Fprintf(os.Stderr, "%s  daemon process started but PID not confirmed yet\n",
		ui.Muted.Render("⚠"))
	fmt.Printf("  %s  %s\n", ui.Muted.Render("Check:"), "pharos daemon status")
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
// of managed servers. With --json (or PHAROS_JSON=1) the same information
// is emitted as a single JSON object.
func runDaemonStatus(cmd *cobra.Command, args []string) error {
	status, err := daemon.Status()
	if err != nil {
		// Status returns an error when the daemon is not running (stub)
		// or can't be reached. Either way, show a clear message.
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot get daemon status:"), err)
		os.Exit(1)
	}

	if !status.Running {
		if JSONRequested() {
			return printDaemonStatusJSON(status)
		}
		fmt.Println(ui.Muted.Render("Daemon is not running."))
		fmt.Printf("\n  %s  %s\n", ui.Muted.Render("Start it with:"), "pharos daemon start")
		return nil
	}

	if JSONRequested() {
		return printDaemonStatusJSON(status)
	}

	// Daemon summary
	fmt.Printf("%s  %s\n", ui.Success.Render("✓ Daemon is running"),
		fmt.Sprintf("(PID %d, port %d)", status.PID, status.Port))

	if !status.StartedAt.IsZero() {
		fmt.Printf("  %s  %s\n", ui.Muted.Render("Started:"), formatTimeAgo(status.StartedAt))
	}

	if len(status.Servers) == 0 {
		fmt.Printf("\n%s\n", ui.Muted.Render("No servers managed by daemon."))
		return nil
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
	return nil
}

// daemonStatusJSON holds the flag for `daemon status --json`.
var daemonStatusJSON bool

// daemonStatusOut is the JSON shape of `daemon status --json`. It shadows
// internal/daemon's types so the wire format stays stable even if the
// internal structs change.
type daemonStatusOut struct {
	Running   bool              `json:"running"`
	PID       int               `json:"pid"`
	Port      int               `json:"port,omitempty"`
	StartedAt string            `json:"started_at,omitempty"`
	Servers   []daemonServerOut `json:"servers"`
}

// daemonServerOut is one managed server in the status JSON.
type daemonServerOut struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	Port         int    `json:"port,omitempty"`
	Memory       int64  `json:"memory,omitempty"`
	LastActivity string `json:"last_activity,omitempty"`
	IdleTimeout  int    `json:"idle_timeout,omitempty"`
}

// printDaemonStatusJSON emits the daemon status as JSON to stdout.
func printDaemonStatusJSON(status *daemon.DaemonStatus) error {
	out := daemonStatusOut{
		Running: status.Running,
		PID:     status.PID,
		Port:    status.Port,
		Servers: make([]daemonServerOut, 0, len(status.Servers)),
	}
	if !status.StartedAt.IsZero() {
		out.StartedAt = status.StartedAt.UTC().Format(time.RFC3339)
	}
	for _, s := range status.Servers {
		srv := daemonServerOut{
			Name:        s.Name,
			State:       s.State,
			Port:        s.Port,
			Memory:      s.Memory,
			IdleTimeout: s.IdleTimeout,
		}
		if !s.LastActivity.IsZero() {
			srv.LastActivity = s.LastActivity.UTC().Format(time.RFC3339)
		}
		out.Servers = append(out.Servers, srv)
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// runDaemonLog shows recent daemon log output.
func runDaemonLog(cmd *cobra.Command, args []string) {
	path, err := daemon.LogPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot find daemon log:"), err)
		os.Exit(1)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println(ui.Muted.Render("No daemon log found. The daemon may not have been started yet."))
			return
		}
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot read daemon log:"), err)
		os.Exit(1)
	}

	lines := strings.Split(string(data), "\n")
	start := len(lines) - daemonLogLines
	if start < 0 {
		start = 0
	}

	fmt.Printf("%s  %s\n\n", ui.Label.Render("pharos daemon log"), ui.Muted.Render(path))
	for _, line := range lines[start:] {
		if line != "" {
			fmt.Println(line)
		}
	}
}

// runDaemonRestart stops the daemon and starts it again.
func runDaemonRestart(cmd *cobra.Command, args []string) {
	fmt.Printf("%s  restarting daemon...\n", ui.Label.Render("pharos daemon"))

	// Stop if running
	status, _ := daemon.Status()
	if status != nil && status.Running {
		if err := daemon.Stop(); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to stop daemon:"), err)
			os.Exit(1)
		}
		fmt.Printf("%s  daemon stopped\n", ui.Muted.Render("·"))
		time.Sleep(500 * time.Millisecond)
	}

	// Start in background
	runDaemonStart(cmd, args)
}

// runDaemonAutostart enables, disables, or shows autostart status.
func runDaemonAutostart(cmd *cobra.Command, args []string) {
	if daemonAutostartOn && daemonAutostartOff {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot use --on and --off together"))
		os.Exit(1)
	}

	if daemonAutostartOn {
		if err := daemon.EnableAutostart(); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to enable autostart:"), err)
			os.Exit(1)
		}
		fmt.Printf("%s  daemon autostart enabled — daemon will start on boot\n", ui.Success.Render("✓"))
		return
	}

	if daemonAutostartOff {
		if err := daemon.DisableAutostart(); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to disable autostart:"), err)
			os.Exit(1)
		}
		fmt.Printf("%s  daemon autostart disabled\n", ui.Success.Render("✓"))
		return
	}

	// Show status
	enabled, err := daemon.AutostartStatus()
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot check autostart status:"), err)
		os.Exit(1)
	}
	if enabled {
		fmt.Printf("%s  daemon autostart is enabled\n", ui.Success.Render("✓"))
	} else {
		fmt.Println(ui.Muted.Render("Daemon autostart is not enabled."))
		fmt.Printf("  %s  %s\n", ui.Muted.Render("Enable with:"), "pharos daemon autostart --on")
	}
}
