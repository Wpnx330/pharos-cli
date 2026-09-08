package procs

import (
	"strconv"
	"strings"
)

// parseTasklistMemUsage extracts the memory-usage figure in bytes from the
// stdout of `tasklist /FO CSV /NH /FI "PID eq <pid>"` (Windows). The row is
// "image name","PID","session name","session#","mem usage", where mem usage
// is kilobytes with thousands separators, e.g. "7,524 K". Locale variants
// using another separator (e.g. German "75.524 K"), INFO lines, and short
// rows all report ok=false — the probe's "unknown" contract. Pure string
// parsing, kept in a build-tag-free file so its table test runs on every
// platform (the tasklist probe itself is windows-only).
func parseTasklistMemUsage(raw string) (int64, bool) {
	line := strings.TrimSpace(raw)
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
