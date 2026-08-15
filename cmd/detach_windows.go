//go:build windows

package cmd

import (
	"os/exec"
	"syscall"
)

// detachProcess detaches a command from the terminal so it continues
// running after the parent exits. On Windows: uses CREATE_NO_WINDOW flag
// combined with CREATE_NEW_PROCESS_GROUP for isolation.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000008 | syscall.CREATE_NEW_PROCESS_GROUP, // DETACHED_PROCESS
	}
}
