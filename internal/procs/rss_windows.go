//go:build windows

package procs

import (
	"os/exec"
	"strconv"
	"strings"
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
	line := strings.TrimSpace(string(out))
	if line == "" {
		return 0, false
	}
	// CSV row: "image name","PID","session name","session#","mem usage"
	parts := strings.Split(line, "\",\"")
	if len(parts) < 5 {
		return 0, false
	}
	mem := strings.TrimSpace(parts[len(parts)-1])
	mem = strings.TrimSuffix(strings.TrimPrefix(mem, "\""), "\"")
	mem = strings.TrimSuffix(mem, " K")
	mem = strings.ReplaceAll(mem, ",", "")
	mem = strings.ReplaceAll(mem, "\"", "")
	kb, err := strconv.ParseInt(strings.TrimSpace(mem), 10, 64)
	if err != nil || kb < 0 {
		return 0, false
	}
	return kb * 1024, true
}
