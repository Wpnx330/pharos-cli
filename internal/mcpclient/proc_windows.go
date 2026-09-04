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

// killProc force-kills the probe's child. taskkill /T /F runs FIRST because
// it tears down the whole descendant tree — npx-wrapped servers are
// npx.cmd → cmd.exe → node, and TerminateProcess (proc.Kill) only kills the
// direct child, orphaning the node descendants. Direct termination is the
// fallback for when taskkill is unavailable or fails.
func killProc(pid int) error {
	out, terr := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).CombinedOutput()
	if terr == nil {
		return nil
	}
	proc, ferr := os.FindProcess(pid)
	if ferr != nil {
		return fmt.Errorf("taskkill %d: %v: %s", pid, terr, string(out))
	}
	if kerr := proc.Kill(); kerr != nil {
		return fmt.Errorf("kill %d: taskkill: %v: %s; terminate: %v", pid, terr, string(out), kerr)
	}
	return nil
}
