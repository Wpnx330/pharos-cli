//go:build windows

package mcpclient

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// setProcGroup mirrors internal/runtime: on Windows, CREATE_NEW_PROCESS_GROUP
// isolates the probe's child.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

// killProc force-kills the probe's child. TerminateProcess is tried first;
// taskkill /T /F is the fallback because it also tears down the child tree.
func killProc(pid int) error {
	proc, err := os.FindProcess(pid)
	if err == nil {
		if kerr := proc.Kill(); kerr == nil {
			return nil
		}
	}
	out, terr := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).CombinedOutput()
	if terr != nil {
		return fmt.Errorf("taskkill %d: %v: %s", pid, terr, string(out))
	}
	return nil
}
