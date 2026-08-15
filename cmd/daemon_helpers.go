package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/daemon"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

// ensureDaemonRunning checks if the daemon is running. If not, it starts
// the daemon in the background. If the daemon is already running, it sends
// a reload signal so it picks up any newly installed servers.
func ensureDaemonRunning() {
	status, _ := daemon.Status()
	if status != nil && status.Running {
		// Daemon is running — send reload so it picks up the new server
		if err := daemon.ReloadDaemon(status.PID); err != nil {
			// Non-fatal — the server will be picked up on next daemon restart
			fmt.Fprintf(os.Stderr, "  %s  could not reload daemon: %v\n",
				ui.Muted.Render("⚠"), err)
		} else {
			fmt.Printf("  %s  daemon reloaded (new server now managed)\n",
				ui.Muted.Render("·"))
		}
		return
	}

	// Daemon is not running — start it in the background
	fmt.Printf("  %s  starting daemon for HTTP/SSE server management...\n",
		ui.Muted.Render("·"))

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  %s  cannot auto-start daemon: %v\n",
			ui.Muted.Render("⚠"), err)
		return
	}

	bgCmd := exec.Command(exe, "daemon", "start", "--daemon-internal")
	bgCmd.Stdin = nil
	bgCmd.Stdout = nil
	bgCmd.Stderr = nil
	detachProcess(bgCmd)

	if err := bgCmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "  %s  could not start daemon: %v\n",
			ui.Muted.Render("⚠"), err)
		return
	}

	// Wait briefly for daemon to come up
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := daemon.Status()
		if s != nil && s.Running {
			fmt.Printf("  %s  daemon started (PID %d) — server will be managed automatically\n",
				ui.Success.Render("✓"), s.PID)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}

	fmt.Printf("  %s  daemon starting in background — run 'pharos daemon status' to verify\n",
		ui.Muted.Render("·"))
}
