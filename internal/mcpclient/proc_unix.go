//go:build !windows

package mcpclient

import (
	"os/exec"
	"syscall"
)

// setProcGroup mirrors internal/runtime: on Unix, Setpgid creates a new
// process group so the probe can kill the whole server tree.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProc sends SIGKILL to the server's process group.
func killProc(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}
