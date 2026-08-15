package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ── State persistence tests ─────────────────────────────────────────────

func TestStateSaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	orig := daemonDirFn
	daemonDirFn = func() (string, error) { return tmpDir, nil }
	defer func() { daemonDirFn = orig }()

	st := &DaemonState{
		PID:       12345,
		StartedAt: time.Now().UTC().Truncate(time.Second),
		Servers: map[string]ServerState{
			"test-server": {
				State:        "running",
				PID:          12346,
				Port:         8421,
				StartedAt:    time.Now().UTC().Truncate(time.Second),
				LastActivity: time.Now().UTC().Truncate(time.Second),
				IdleTimeout:  60,
			},
		},
	}

	if err := saveState(st); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	loaded, err := loadState()
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}

	if loaded.PID != st.PID {
		t.Errorf("PID mismatch: got %d, want %d", loaded.PID, st.PID)
	}
	if len(loaded.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(loaded.Servers))
	}
	s := loaded.Servers["test-server"]
	if s.Port != 8421 {
		t.Errorf("Port mismatch: got %d, want 8421", s.Port)
	}
	if s.IdleTimeout != 60 {
		t.Errorf("IdleTimeout mismatch: got %d, want 60", s.IdleTimeout)
	}
}

func TestStateLoadMissing(t *testing.T) {
	tmpDir := t.TempDir()
	orig := daemonDirFn
	daemonDirFn = func() (string, error) { return tmpDir, nil }
	defer func() { daemonDirFn = orig }()

	st, err := loadState()
	if err != nil {
		t.Fatalf("loadState on missing file: %v", err)
	}
	if st == nil {
		t.Fatal("expected non-nil state")
	}
	if len(st.Servers) != 0 {
		t.Errorf("expected empty servers, got %d", len(st.Servers))
	}
}

func TestStateFilePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	orig := daemonDirFn
	daemonDirFn = func() (string, error) { return tmpDir, nil }
	defer func() { daemonDirFn = orig }()

	st := &DaemonState{
		PID:     1,
		Servers: make(map[string]ServerState),
	}
	if err := saveState(st); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	path := filepath.Join(tmpDir, "daemon.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected 0600 permissions, got %v", perm)
	}
}

// ── PID file tests ──────────────────────────────────────────────────────

func TestPIDFileWriteRead(t *testing.T) {
	tmpDir := t.TempDir()
	orig := daemonDirFn
	daemonDirFn = func() (string, error) { return tmpDir, nil }
	defer func() { daemonDirFn = orig }()

	if err := writeDaemonPID(99999); err != nil {
		t.Fatalf("writeDaemonPID: %v", err)
	}

	pid, err := readDaemonPID()
	if err != nil {
		t.Fatalf("readDaemonPID: %v", err)
	}
	if pid != 99999 {
		t.Errorf("PID mismatch: got %d, want 99999", pid)
	}

	path := filepath.Join(tmpDir, "daemon.pid")
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("expected 0600, got %v", info.Mode().Perm())
	}
}

func TestPIDFileMissing(t *testing.T) {
	tmpDir := t.TempDir()
	orig := daemonDirFn
	daemonDirFn = func() (string, error) { return tmpDir, nil }
	defer func() { daemonDirFn = orig }()

	pid, err := readDaemonPID()
	if err != nil {
		t.Fatalf("readDaemonPID on missing: %v", err)
	}
	if pid != 0 {
		t.Errorf("expected 0, got %d", pid)
	}
}

// ── Helper function tests ───────────────────────────────────────────────

func TestSplitCommand(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"node server.js", []string{"node", "server.js"}},
		{"python -m server --port 8765", []string{"python", "-m", "server", "--port", "8765"}},
		{`node "path with space.js"`, []string{"node", "path with space.js"}},
		{"", []string{}},
		{"single", []string{"single"}},
	}

	for _, tt := range tests {
		got := splitCommand(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("splitCommand(%q) = %v (len %d), want %v (len %d)",
				tt.input, got, len(got), tt.want, len(tt.want))
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitCommand(%q)[%d] = %q, want %q",
					tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"a\nb\nc", 3},
		{"a\nb\n", 2},
		{"", 0},
		{"single", 1},
	}
	for _, tt := range tests {
		got := splitLines(tt.input)
		if len(got) != tt.want {
			t.Errorf("splitLines(%q) = %d lines, want %d", tt.input, len(got), tt.want)
		}
	}
}

func TestSplitFields(t *testing.T) {
	got := splitFields("VmRSS:\t 12345 kB")
	if len(got) < 2 {
		t.Fatalf("expected at least 2 fields, got %d", len(got))
	}
	if got[1] != "12345" {
		t.Errorf("field[1] = %q, want %q", got[1], "12345")
	}
}

// ── Port helper tests ───────────────────────────────────────────────────

func TestIsPortOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	port := 0
	fmt.Sscanf(srv.URL, "http://127.0.0.1:%d", &port)
	if port == 0 {
		t.Fatal("failed to extract test server port")
	}

	if !isPortOpen(port) {
		t.Error("expected port to be open")
	}
	if isPortOpen(59999) {
		t.Error("expected port 59999 to be closed")
	}
}

// ── Process alive test ──────────────────────────────────────────────────

func TestIsProcessAlive(t *testing.T) {
	if !isProcessAlive(os.Getpid()) {
		t.Error("current process should be alive")
	}
	if isProcessAlive(999999) {
		t.Error("PID 999999 should not be alive")
	}
	if isProcessAlive(0) {
		t.Error("PID 0 should not be alive")
	}
}

// ── Integration: proxy with pre-running backing server ──────────────────

func TestProxyForwarding(t *testing.T) {
	tmpDir := t.TempDir()
	orig := daemonDirFn
	daemonDirFn = func() (string, error) { return tmpDir, nil }
	defer func() { daemonDirFn = orig }()

	// Start a backing server
	backing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backing-ok"))
	}))
	defer backing.Close()

	backingPort := 0
	fmt.Sscanf(backing.URL, "http://127.0.0.1:%d", &backingPort)

	// Open log file
	logFile, _ := os.CreateTemp(tmpDir, "daemon-*.log")
	defer logFile.Close()

	d := &Daemon{
		servers: make(map[string]*ManagedServer),
		state: &DaemonState{
			Servers: make(map[string]ServerState),
		},
		log:     log.New(logFile, "", log.LstdFlags),
		logFile: logFile,
		done:    make(chan struct{}),
	}

	// Find a free port for the proxy listener
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	proxyPort := listener.Addr().(*net.TCPAddr).Port

	ms := &ManagedServer{
		Name:        "test-server",
		Port:        proxyPort,
		IdleTimeout: 60,
		BackingPort: backingPort,
		Command:     "", // no local command — backing already running
		daemon:      d,
		listener:    listener,
		state:       "unloaded",
	}

	d.servers["test-server"] = ms
	d.state.Servers["test-server"] = ServerState{
		State:       "unloaded",
		Port:        proxyPort,
		IdleTimeout: 60,
	}

	// Start proxy goroutine
	go ms.serveProxy()
	time.Sleep(100 * time.Millisecond)

	// Set state to running (simulating JIT load already happened)
	ms.mu.Lock()
	ms.state = "running"
	ms.lastActivity = time.Now()
	ms.mu.Unlock()

	// Make request through proxy
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/test", proxyPort))
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// Verify lastActivity updated
	ms.mu.Lock()
	updated := !ms.lastActivity.IsZero()
	ms.mu.Unlock()
	if !updated {
		t.Error("expected lastActivity to be set")
	}

	listener.Close()
}

// ── Concurrency test ────────────────────────────────────────────────────

func TestManagedServerConcurrentAccess(t *testing.T) {
	ms := &ManagedServer{
		Name:        "test",
		IdleTimeout: 60,
		state:       "unloaded",
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ms.mu.Lock()
			ms.lastActivity = time.Now()
			ms.mu.Unlock()
		}()
	}
	wg.Wait()

	ms.mu.Lock()
	if ms.lastActivity.IsZero() {
		t.Error("expected lastActivity to be set")
	}
	ms.mu.Unlock()
}

// ── JSON round-trip test ────────────────────────────────────────────────

func TestDaemonStateJSONRoundTrip(t *testing.T) {
	original := DaemonState{
		PID:       42,
		StartedAt: time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC),
		Servers: map[string]ServerState{
			"srv1": {
				State:        "running",
				PID:          100,
				Port:         8421,
				IdleTimeout:  60,
				LastActivity: time.Date(2026, 8, 15, 12, 30, 0, 0, time.UTC),
			},
			"srv2": {
				State:       "unloaded",
				PID:         0,
				Port:        8422,
				IdleTimeout: 0,
			},
		},
	}

	data, err := json.MarshalIndent(&original, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded DaemonState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.PID != original.PID {
		t.Errorf("PID: got %d, want %d", decoded.PID, original.PID)
	}
	if len(decoded.Servers) != 2 {
		t.Errorf("expected 2 servers, got %d", len(decoded.Servers))
	}
	if decoded.Servers["srv1"].State != "running" {
		t.Errorf("srv1 state: got %q, want %q",
			decoded.Servers["srv1"].State, "running")
	}
	if decoded.Servers["srv2"].IdleTimeout != 0 {
		t.Errorf("srv2 idleTimeout: got %d, want 0",
			decoded.Servers["srv2"].IdleTimeout)
	}
}

// ── Stop when not running ───────────────────────────────────────────────

func TestStopBackingWhenNotRunning(t *testing.T) {
	ms := &ManagedServer{
		Name:        "test",
		IdleTimeout: 60,
		state:       "unloaded",
		pid:         0,
	}

	// Should be a no-op — no panic, no error
	ms.stopBacking()
}

// ── Status when daemon not running ──────────────────────────────────────

func TestStatusNotRunning(t *testing.T) {
	tmpDir := t.TempDir()
	orig := daemonDirFn
	daemonDirFn = func() (string, error) { return tmpDir, nil }
	defer func() { daemonDirFn = orig }()

	// No PID file → not running
	st, err := Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Running {
		t.Error("expected daemon not running")
	}
}
