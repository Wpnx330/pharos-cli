//go:build linux

package procs

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// RSS returns the resident set size of pid in bytes, read from
// /proc/<pid>/status (VmRSS, reported in kB). Pure stdlib — no subprocess.
// ok is false when the process cannot be read or VmRSS is missing;
// callers treat that as "unknown", never as an error.
func RSS(pid int) (int64, bool) {
	if pid <= 0 {
		return 0, false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, false
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || kb < 0 {
			return 0, false
		}
		return kb * 1024, true
	}
	return 0, false
}
