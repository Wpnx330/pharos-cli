//go:build !windows

package procs

import (
	"os"
	"syscall"
)

// Alive reports whether a process with the given PID is running.
// It mirrors the daemon's liveness check exactly (internal/daemon
// isProcessAlive, process_unix.go): os.FindProcess followed by signal 0,
// which probes the process without delivering anything.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
