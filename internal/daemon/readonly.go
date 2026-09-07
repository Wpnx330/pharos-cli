package daemon

// Read-only exports for CLI reporting views (W5.2 C2, `pharos budget`).
// These wrap the daemon's own state-loading and liveness helpers without
// changing any daemon behavior: a budget report must see exactly what the
// daemon sees, through the same code paths (loadState / isProcessAlive).
// Nothing here writes, mutates, or signals anything.

// ReadState loads the persisted daemon state (~/.pharos/daemon.json).
// Read-only: it never creates, writes, or mutates anything. A missing
// state file returns an empty state with no error, mirroring loadState;
// a present-but-unparsable file returns an error.
func ReadState() (*DaemonState, error) {
	return loadState()
}

// IsProcessAlive reports whether a process with the given PID is running,
// using the exact liveness check the daemon itself uses (isProcessAlive).
func IsProcessAlive(pid int) bool {
	return isProcessAlive(pid)
}
