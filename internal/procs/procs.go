// Package procs provides read-only OS process probes for CLI reporting
// views (W5.2 C2, `pharos budget`): process liveness and resident-set-size
// estimates.
//
// Liveness (Alive) mirrors the daemon's own isProcessAlive mechanism
// exactly — internal/daemon, process_unix.go / process_windows.go — so a
// budget report sees process state through the same check the daemon
// itself uses. No new liveness mechanism is invented here.
//
// Memory (RSS) is ESTIMATE-grade by design: it reads the cheapest
// per-platform RSS figure and reports ok=false when the probe fails.
// Callers must treat a failed probe as "unknown" (rendered "n/a"), never
// as a command failure.
//
// Platform files:
//
//	alive_unix.go    liveness, all non-Windows platforms (pure stdlib)
//	alive_windows.go liveness, Windows (pure stdlib)
//	rss_linux.go     /proc/<pid>/status VmRSS — pure stdlib, no subprocess
//	rss_ps.go        `ps -o rss=` shell-out (darwin + the BSDs)
//	rss_windows.go   `tasklist /FO CSV` shell-out (Windows)
package procs
