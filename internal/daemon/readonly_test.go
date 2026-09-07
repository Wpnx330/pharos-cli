package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempDaemonDir points daemonDirFn at a fresh temp dir for the test.
func withTempDaemonDir(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	orig := daemonDirFn
	daemonDirFn = func() (string, error) { return tmpDir, nil }
	t.Cleanup(func() { daemonDirFn = orig })
	return tmpDir
}

func TestReadStateMissingReturnsEmpty(t *testing.T) {
	dir := withTempDaemonDir(t)

	st, err := ReadState()
	if err != nil {
		t.Fatalf("ReadState with no state file: %v", err)
	}
	if st == nil {
		t.Fatal("ReadState with no state file: state = nil, want non-nil empty state")
	}
	if len(st.Servers) != 0 {
		t.Errorf("ReadState with no state file: servers = %d, want 0", len(st.Servers))
	}
	if st.PID != 0 {
		t.Errorf("ReadState with no state file: pid = %d, want 0", st.PID)
	}
	if _, err := os.Stat(filepath.Join(dir, "daemon.json")); !os.IsNotExist(err) {
		t.Errorf("ReadState must not create daemon.json (read-only); stat err = %v", err)
	}
}

func TestReadStateEmptyObject(t *testing.T) {
	dir := withTempDaemonDir(t)
	if err := os.WriteFile(filepath.Join(dir, "daemon.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write empty-object state: %v", err)
	}

	st, err := ReadState()
	if err != nil {
		t.Fatalf("ReadState on {} state file: %v", err)
	}
	if st == nil || st.Servers == nil {
		t.Fatalf("ReadState on {} state file: %+v, want non-nil state with non-nil server map", st)
	}
	if len(st.Servers) != 0 {
		t.Errorf("ReadState on {} state file: servers = %d, want 0", len(st.Servers))
	}
}

func TestReadStateCorruptReturnsError(t *testing.T) {
	dir := withTempDaemonDir(t)
	if err := os.WriteFile(filepath.Join(dir, "daemon.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}

	st, err := ReadState()
	if err == nil {
		t.Fatal("ReadState on corrupt state file: want error, got nil")
	}
	if st != nil {
		t.Errorf("ReadState on corrupt state file: state = %+v, want nil", st)
	}
	if !strings.Contains(err.Error(), "parse daemon state") {
		t.Errorf("error = %v, want it to name the parse failure", err)
	}
}

func TestReadStateRoundTripsServerFields(t *testing.T) {
	dir := withTempDaemonDir(t)
	const raw = `{
		"pid": 4242,
		"startedAt": "2026-09-01T12:00:00Z",
		"servers": {
			"srv-a": {
				"state": "running",
				"pid": 999,
				"port": 8421,
				"startedAt": "2026-09-01T12:01:00Z",
				"lastActivity": "2026-09-01T12:05:00Z",
				"idleTimeout": 60
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(dir, "daemon.json"), []byte(raw), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	st, err := ReadState()
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.PID != 4242 {
		t.Errorf("pid = %d, want 4242", st.PID)
	}
	srv, ok := st.Servers["srv-a"]
	if !ok {
		t.Fatalf("servers missing srv-a; keys = %v", st.Servers)
	}
	if srv.PID != 999 || srv.Port != 8421 || srv.IdleTimeout != 60 {
		t.Errorf("srv-a = %+v, want pid 999, port 8421, idleTimeout 60", srv)
	}
	if srv.State != "running" {
		t.Errorf("srv-a state = %q, want running", srv.State)
	}
	if srv.LastActivity.IsZero() {
		t.Error("srv-a lastActivity is zero, want parsed timestamp")
	}
}

func TestIsProcessAliveSelf(t *testing.T) {
	self := os.Getpid()
	if !IsProcessAlive(self) {
		t.Fatalf("IsProcessAlive(%d) = false, want true for the current process", self)
	}
	for _, pid := range []int{0, -1} {
		if IsProcessAlive(pid) {
			t.Errorf("IsProcessAlive(%d) = true, want false for non-positive PID", pid)
		}
	}
}
