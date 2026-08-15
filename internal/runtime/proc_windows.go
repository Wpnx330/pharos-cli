//go:build windows

package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// setProcGroup sets the process group attribute for child processes.
// On Windows: CREATE_NEW_PROCESS_GROUP for isolation.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

// termProc terminates a process on Windows (TerminateProcess — no SIGTERM).
func termProc(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	return proc.Kill()
}

// killProc force-kills a process on Windows (same as termProc).
func killProc(pid int) error {
	return termProc(pid)
}
