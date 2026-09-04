// W2.2 — `pharos profile run` (SPEC A1 daemon synergy): load the
// profile's target set into the daemon and idle/stop every other
// daemon-managed server, so context switching also saves memory.
//
// The wiring is deliberately thin over the existing primitives:
//   - daemon.LoadServer(name)  — queue a JIT load (kind-2 servers are
//     managed; kind 1/3 are ignored by the daemon reconcile loop).
//   - daemon.StopServer(name)  — queue an unload for a managed server.
//   - daemon.Status().Servers  — the currently daemon-managed set.
//
// If the daemon is not running it is started in the background the same
// way `pharos install` auto-starts it (detached self-exec); if it does
// not come up the command reports and exits 1 without guessing.
package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/daemon"
	"github.com/Wpnx330/pharos-cli/internal/profiles"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

// profileRunAutoStart starts the daemon in the background and reports
// whether it came up. A package variable so tests never spawn processes.
var profileRunAutoStart = autoStartDaemonForProfile

var profileRunCmd = &cobra.Command{
	Use:   "run <name>",
	Short: "Load a profile's servers in the daemon and idle the rest",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		loaded, stopped, target, code, err := runProfileRun(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot run profile:"), err)
			os.Exit(code)
		}
		if code != 0 {
			os.Exit(code)
		}

		type runOut struct {
			Profile   string   `json:"profile"`
			Loaded    []string `json:"loaded"`
			Stopped   []string `json:"stopped"`
			TargetSet []string `json:"target_set"`
		}
		out := runOut{Profile: args[0], Loaded: loaded, Stopped: stopped, TargetSet: target}
		if JSONRequested() {
			return emitProfileJSON(out)
		}
		fmt.Printf("%s  Profile %q: %d server(s) requested, %d daemon server(s) idled\n",
			ui.Success.Render("✓"), args[0], len(loaded), len(stopped))
		if len(loaded) > 0 {
			fmt.Printf("%s  loaded: %v\n", ui.Muted.Render("·"), loaded)
		}
		if len(stopped) > 0 {
			fmt.Printf("%s  idled: %v\n", ui.Muted.Render("·"), stopped)
		}
		return nil
	},
}

// runProfileRun is the testable core of `pharos profile run`. target is
// the resolved target set; loaded/stopped are what was requested from
// the daemon (nil-when-empty is normalized to empty slices).
func runProfileRun(name string) (loaded, stopped, target []string, code int, err error) {
	st, err := profiles.Load()
	if err != nil {
		return nil, nil, nil, 2, err
	}
	if !st.HasProfile(name) {
		return nil, nil, nil, 2, fmt.Errorf("profile %q does not exist", name)
	}
	target, err = st.TargetSet(name)
	if err != nil {
		return nil, nil, nil, 2, err
	}

	status, _ := daemon.Status()
	if status == nil || !status.Running {
		if !profileRunAutoStart() {
			return nil, nil, target, 1, fmt.Errorf("daemon is not running — start it with 'pharos daemon start' and re-run 'pharos profile run %s'", name)
		}
		status, _ = daemon.Status()
		if status == nil || !status.Running {
			return nil, nil, target, 1, fmt.Errorf("daemon did not start — run 'pharos daemon status' to diagnose")
		}
	}

	inTarget := make(map[string]bool, len(target))
	for _, srv := range target {
		inTarget[srv] = true
	}

	for _, srv := range target {
		if lerr := daemon.LoadServer(srv); lerr != nil {
			fmt.Fprintf(os.Stderr, "%s  load %s: %v\n", ui.Warning.Render("Warning:"), srv, lerr)
			continue
		}
		loaded = append(loaded, srv)
	}
	for _, managed := range status.Servers {
		if inTarget[managed.Name] {
			continue
		}
		if serr := daemon.StopServer(managed.Name); serr != nil {
			fmt.Fprintf(os.Stderr, "%s  unload %s: %v\n", ui.Warning.Render("Warning:"), managed.Name, serr)
			continue
		}
		stopped = append(stopped, managed.Name)
	}
	if loaded == nil {
		loaded = []string{}
	}
	if stopped == nil {
		stopped = []string{}
	}
	return loaded, stopped, target, 0, nil
}

// autoStartDaemonForProfile mirrors ensureDaemonRunning's detached
// self-exec start (`pharos daemon start --daemon-internal` blocks for
// the daemon's lifetime, so the child is detached and never waited on),
// then polls briefly for the PID file to appear.
func autoStartDaemonForProfile() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	bgCmd := exec.Command(exe, "daemon", "start", "--daemon-internal")
	bgCmd.Stdin = nil
	bgCmd.Stdout = nil
	bgCmd.Stderr = nil
	detachProcess(bgCmd)
	if err := bgCmd.Start(); err != nil {
		return false
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, _ := daemon.Status()
		if status != nil && status.Running {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}
