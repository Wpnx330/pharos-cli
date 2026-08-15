//go:build !windows

package runtime

import (
	"os"
	"syscall"
)

// procExists checks if a process is running by sending signal 0.
// On Unix: signal 0 is a no-op that returns an error if the process doesn't exist.
func procExists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
