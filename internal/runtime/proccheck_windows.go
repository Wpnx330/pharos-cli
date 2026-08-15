//go:build windows

package runtime

import (
	"syscall"
)

// procExists checks if a process is running on Windows.
// Uses OpenProcess with QUERY_INFORMATION — if it succeeds, the process exists.
func procExists(pid int) bool {
	const PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
	handle, err := syscall.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	syscall.CloseHandle(handle)
	return true
}
