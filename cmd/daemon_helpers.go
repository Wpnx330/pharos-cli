package cmd

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/daemon"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

// ensureDaemonRunning checks if the daemon is running. If not, it starts
// the daemon in the background. If the daemon is already running, it sends
// a reload signal so it picks up any newly installed servers, then JIT-loads
// the named kind-2 backing process so `pharos list` can show it running.
func ensureDaemonRunning(name string) {
	// Queue the load request before reload/start so Start() and the first
	// reconcile see it. LoadServer also SIGHUPs if the daemon is already up.
	queued := queueBackingLoad(name)

	status, _ := daemon.Status()
	if status != nil && status.Running {
		// Extra reload covers the race where LoadServer queued the file
		// before the daemon PID was visible (Start already passed consume).
		if err := daemon.ReloadDaemon(status.PID); err != nil && !queued {
			fmt.Fprintf(os.Stderr, "  %s  could not reload daemon: %v\n",
				ui.Muted.Render("⚠"), err)
		} else if name != "" {
			progressf("  %s  daemon reloaded — starting backing process for %s\n",
				ui.Muted.Render("·"), name)
		} else {
			progressf("  %s  daemon reloaded (new server now managed)\n",
				ui.Muted.Render("·"))
		}
		waitForKind2Listen(defaultKind2ListenPort, 5*time.Second)
		return
	}

	// Daemon is not running — start it in the background
	progressf("  %s  starting daemon for HTTP/SSE server management...\n",
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
			progressf("  %s  daemon started (PID %d) — server will be managed automatically\n",
				ui.Success.Render("✓"), s.PID)
			// Start() consumes a pre-queued load request. Re-issue in case
			// Start() already passed consumeLoadRequests before we queued.
			queueBackingLoad(name)
			waitForKind2Listen(defaultKind2ListenPort, 5*time.Second)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}

	progressf("  %s  daemon starting in background — run 'pharos daemon status' to verify\n",
		ui.Muted.Render("·"))
}

// waitForKind2Listen polls until the backing listen port accepts or
// timeout elapses. Used after queueBackingLoad so install can see 8765
// come up instead of racing the daemon JIT spawn.
func waitForKind2Listen(port int, timeout time.Duration) bool {
	if port <= 0 || timeout <= 0 {
		return false
	}
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// queueBackingLoad asks the daemon to JIT-start the kind-2 backing process.
// LoadServer always writes the request file; if the daemon is not up yet
// that is not an error — Start() or the next reconcile will consume it.
func queueBackingLoad(name string) bool {
	if name == "" {
		return false
	}
	if err := daemon.LoadServer(name); err != nil {
		// Request is still queued when the daemon is merely not running.
		if !strings.Contains(err.Error(), "not running") {
			fmt.Fprintf(os.Stderr, "  %s  could not load server %s: %v\n",
				ui.Muted.Render("⚠"), name, err)
			return false
		}
		return true
	}
	return true
}
