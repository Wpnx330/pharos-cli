//go:build windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// setSysProcAttr sets platform-specific process attributes for child processes.
// On Windows: creates a new process group via CREATE_NEW_PROCESS_GROUP.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

// readProcMemory reads RSS memory for a process.
// On Windows: not implemented (returns 0). Would require GetProcessMemoryInfo.
func readProcMemory(pid int) int64 {
	return 0
}

// terminateProcess sends a graceful termination signal to a process.
// On Windows: TerminateProcess is the only reliable option (no SIGTERM equivalent).
// For console processes started with CREATE_NEW_PROCESS_GROUP, we try
// GenerateConsoleCtrlEvent first, then fall back to TerminateProcess.
func terminateProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	// On Windows, we use os.FindProcess + Kill (TerminateProcess).
	// There's no graceful SIGTERM. Console apps that listen for Ctrl+C
	// can be signaled via GenerateConsoleCtrlEvent, but that requires
	// the process to be in the same console. For daemon-managed servers,
	// hard kill is the pragmatic approach.
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	return proc.Kill()
}

// killProcess sends a forceful kill signal to a process.
// On Windows: same as terminateProcess (TerminateProcess is already forceful).
func killProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	return proc.Kill()
}

// sendReloadSignal sends the hot-reload signal to the daemon process.
// On Windows: no SIGHUP. We use a file-based trigger instead.
// The daemon watches ~/.pharos/daemon.reload — touching it triggers reload.
func sendReloadSignal(pid int) error {
	// File-based trigger — the daemon's reload watcher will pick this up.
	return touchReloadFile()
}

// terminateDaemon terminates the daemon process.
// On Windows: TerminateProcess (no SIGTERM equivalent).
func terminateDaemon(pid int) error {
	if pid <= 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	return proc.Kill()
}

// supportsSignals returns true on platforms that support Unix signals.
func supportsSignals() bool {
	return false
}
