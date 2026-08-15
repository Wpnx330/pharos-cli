//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// setSysProcAttr sets platform-specific process attributes for child processes.
// On Unix: creates a new process group so we can signal the whole group.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// readProcMemory reads RSS memory for a process.
// On Unix: reads /proc/<pid>/status (Linux) or returns 0 (other Unix).
func readProcMemory(pid int) int64 {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	for _, line := range splitLines(string(data)) {
		if len(line) > 6 && line[:6] == "VmRSS:" {
			fields := splitFields(line)
			if len(fields) >= 2 {
				kb, err := strconv.ParseInt(fields[1], 10, 64)
				if err == nil {
					return kb * 1024
				}
			}
		}
	}
	return 0
}

// terminateProcess sends a graceful termination signal to a process.
// On Unix: sends SIGTERM to the process group.
func terminateProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	return syscall.Kill(-pid, syscall.SIGTERM)
}

// killProcess sends a forceful kill signal to a process.
// On Unix: sends SIGKILL to the process group.
func killProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	return syscall.Kill(-pid, syscall.SIGKILL)
}

// sendReloadSignal sends the hot-reload signal to the daemon process.
// On Unix: sends SIGHUP.
func sendReloadSignal(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid PID: %d", pid)
	}
	return syscall.Kill(pid, syscall.SIGHUP)
}

// terminateDaemon sends SIGTERM to the daemon process directly (not its group).
func terminateDaemon(pid int) error {
	if pid <= 0 {
		return nil
	}
	return syscall.Kill(pid, syscall.SIGTERM)
}

// supportsSignals returns true on platforms that support Unix signals.
func supportsSignals() bool {
	return true
}
