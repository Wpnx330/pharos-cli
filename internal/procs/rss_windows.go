//go:build windows

package procs

import (
	"os/exec"
	"strconv"
)

// RSS returns the working-set size of pid in bytes via
// `tasklist /FO CSV /NH /FI "PID eq <pid>"` (memory-usage column,
// kilobytes with thousands separators). The shell-out is the cheapest
// probe available with the standard library alone and stays inside this
// package per the W5.2 C2 design. ok is false when the command fails or
// the PID is not listed; callers treat that as "unknown".
func RSS(pid int) (int64, bool) {
	if pid <= 0 {
		return 0, false
	}
	out, err := exec.Command("tasklist", "/FO", "CSV", "/NH",
		"/FI", "PID eq "+strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, false
	}
	return parseTasklistMemUsage(string(out))
}
