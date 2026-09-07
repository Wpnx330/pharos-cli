//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package procs

import (
	"os/exec"
	"strconv"
	"strings"
)

// RSS returns the resident set size of pid in bytes via
// `ps -o rss= -p <pid>` (RSS in kilobytes). The shell-out is the cheapest
// portable probe on platforms without /proc; it stays inside this package
// per the W5.2 C2 design (memory probing never hard-fails the caller).
// ok is false when the command fails or the output parses oddly.
func RSS(pid int) (int64, bool) {
	if pid <= 0 {
		return 0, false
	}
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, false
	}
	kb, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil || kb < 0 {
		return 0, false
	}
	return kb * 1024, true
}
