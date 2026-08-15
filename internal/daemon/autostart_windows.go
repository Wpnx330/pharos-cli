//go:build windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"
)

// EnableAutostart configures the daemon to start on login via Windows Task Scheduler.
func EnableAutostart() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}

	// Create a scheduled task that runs at logon
	cmd := exec.Command("schtasks", "/create", "/tn", "PharosDaemon",
		"/tr", fmt.Sprintf("\"%s\" daemon start --daemon-internal", exe),
		"/sc", "onlogon", "/f")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("create scheduled task: %w", err)
	}
	return nil
}

// DisableAutostart removes the Windows Task Scheduler entry.
func DisableAutostart() error {
	cmd := exec.Command("schtasks", "/delete", "/tn", "PharosDaemon", "/f")
	if err := cmd.Run(); err != nil {
		// Task doesn't exist is not an error
		return nil
	}
	return nil
}

// AutostartStatus returns true if the scheduled task exists.
func AutostartStatus() (bool, error) {
	cmd := exec.Command("schtasks", "/query", "/tn", "PharosDaemon")
	if err := cmd.Run(); err != nil {
		return false, nil
	}
	return true, nil
}
