//go:build windows

package procs

import "os"

// Alive reports whether a process with the given PID is running.
// It mirrors the daemon's liveness check exactly (internal/daemon
// isProcessAlive, process_windows.go): os.FindProcess success. On Windows
// FindProcess opens a handle to the PID, signals are unsupported, and the
// daemon treats a successfully opened process as alive — so we do too.
// Best-effort by design; callers must not hard-fail on a wrong answer.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc != nil
}
