//go:build !windows

package runtime

import (
	"os/exec"
	"syscall"
)

// setProcGroup sets the process group attribute for child processes.
// On Unix: Setpgid creates a new process group for clean signal delivery.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// termProc sends SIGTERM to a process group.
func termProc(pid int) error {
	return syscall.Kill(-pid, syscall.SIGTERM)
}

// killProc sends SIGKILL to a process group.
func killProc(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}
