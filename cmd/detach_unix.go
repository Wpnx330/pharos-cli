//go:build !windows

package cmd

import (
	"os/exec"
	"syscall"
)

// detachProcess detaches a command from the terminal so it continues
// running after the parent exits. On Unix: sets setsid to create a new
// session and process group.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
