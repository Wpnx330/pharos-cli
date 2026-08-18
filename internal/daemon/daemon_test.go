package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// ── StopServer stop-request file tests ──────────────────────────────────

func TestStopServerCreatesRequestFile(t *testing.T) {
	tmpDir := t.TempDir()
	orig := daemonDirFn
	daemonDirFn = func() (string, error) { return tmpDir, nil }
	defer func() { daemonDirFn = orig }()

	// Start a dummy process that just sleeps, to use as a fake daemon PID
	dummy := exec.Command("sleep", "10")
	if err := dummy.Start(); err != nil {
		t.Fatalf("start dummy: %v", err)
	}
	defer dummy.Process.Kill()
	dummyPID := dummy.Process.Pid

	// Write a PID file pointing to the dummy process
	pidPath := filepath.Join(tmpDir, "daemon.pid")
	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", dummyPID)), 0o600)

	// Write a fake state file
	st := &DaemonState{
		PID:       dummyPID,
		StartedAt: time.Now().UTC(),
		Servers:   map[string]ServerState{},
	}
	stateData, _ := json.Marshal(st)
	os.WriteFile(filepath.Join(tmpDir, "daemon.json"), stateData, 0o600)

	// Call StopServer — it should create a stop-request file
	_ = StopServer("test-http-server")

	stopFile := filepath.Join(tmpDir, "daemon.stop", "test-http-server")
	data, err := os.ReadFile(stopFile)
	if err != nil {
		t.Fatalf("stop-request file not created: %v", err)
	}
	if string(data) != "stop" {
		t.Errorf("stop file content = %q, want %q", string(data), "stop")
	}
}

func TestStopServerNoDaemon(t *testing.T) {
	tmpDir := t.TempDir()
	orig := daemonDirFn
	daemonDirFn = func() (string, error) { return tmpDir, nil }
	defer func() { daemonDirFn = orig }()

	// No PID file — daemon not running
	err := StopServer("test-server")
	if err == nil {
		t.Error("expected error when daemon not running")
	}
}

// ── Autostart status test (non-destructive) ─────────────────────────────

func TestAutostartStatusNotEnabled(t *testing.T) {
	// On any system without pharos-daemon.service, this should return false
	enabled, err := AutostartStatus()
	if err != nil {
		// Error is acceptable on systems without systemd/launchd
		return
	}
	// In a test environment, autostart should not be enabled
	if enabled {
		t.Log("autostart appears enabled — this may be from a previous test run")
	}
}

// ── LogPath test ─────────────────────────────────────────────────────────

func TestLogPath(t *testing.T) {
	tmpDir := t.TempDir()
	orig := daemonDirFn
	daemonDirFn = func() (string, error) { return tmpDir, nil }
	defer func() { daemonDirFn = orig }()

	path, err := LogPath()
	if err != nil {
		t.Fatalf("LogPath: %v", err)
	}
	expected := filepath.Join(tmpDir, "daemon.log")
	if path != expected {
		t.Errorf("LogPath = %q, want %q", path, expected)
	}
}

// ── ReloadDaemon test ────────────────────────────────────────────────────

func TestReloadDaemonInvalidPID(t *testing.T) {
	// PID 0 should be rejected
	err := ReloadDaemon(0)
	if err == nil {
		t.Error("expected error for PID 0")
	}
}

// ── Backing port assignment ──────────────────────────────────────────────

func TestResolveBackingPort(t *testing.T) {
	const proxyPort = 8421
	tests := []struct {
		name    string
		command string
		args    []string
		url     string
		want    int
	}{
		{
			name:    "explicit --port in args",
			command: "python",
			args:    []string{"server.py", "--port", "9000"},
			want:    9000,
		},
		{
			name:    "explicit --port in command string",
			command: "python server.py --port 9001",
			want:    9001,
		},
		{
			name:    "no declared port defaults to 8765 not proxy",
			command: "python server.py",
			want:    8765,
		},
		{
			name:    "numeric arg is declared port",
			command: "python",
			args:    []string{"server.py", "9333"},
			want:    9333,
		},
		{
			name:    "url port used when command has no port",
			command: "python server.py",
			url:     "http://127.0.0.1:9444/sse",
			want:    9444,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveBackingPort(tt.command, tt.args, tt.url, proxyPort)
			if got != tt.want {
				t.Errorf("resolveBackingPort(...) = %d, want %d", got, tt.want)
			}
			if tt.want == 8765 && got == proxyPort {
				t.Errorf("backing port must not default to proxy port %d", proxyPort)
			}
		})
	}
}

func TestResolveBackingPortNeverEqualsProxyWhenUndeclared(t *testing.T) {
	got := resolveBackingPort("python server.py", nil, "", 8421)
	if got == 8421 {
		t.Fatalf("BackingPort defaulted to proxy port 8421; want well-known 8765")
	}
	if got != 8765 {
		t.Fatalf("BackingPort = %d, want 8765", got)
	}
}

// ── Kind 1 URL-only is not managed ───────────────────────────────────────

func TestShouldManageServerKind1URLOnlySkipped(t *testing.T) {
	tests := []struct {
		name      string
		transport string
		command   string
		url       string
		want      bool
	}{
		{"kind 1 URL-only http-sse", "http-sse", "", "https://example.com/sse", false},
		{"kind 1 URL-only http", "http", "", "https://example.com/mcp", false},
		{"kind 1 URL-only streamable-http", "streamable-http", "", "https://example.com/mcp", false},
		{"kind 2 local http-sse", "http-sse", "python server.py", "", true},
		{"kind 2 local http", "http", "python server.py", "", true},
		{"kind 3 stdio", "stdio", "npx some-server", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldManageLocalHTTP(tt.transport, tt.command)
			if got != tt.want {
				t.Errorf("shouldManageLocalHTTP(%q, %q) = %v, want %v",
					tt.transport, tt.command, got, tt.want)
			}
		})
	}
}

// ── Load-after-install helper (no live cluster) ──────────────────────────

func TestLoadServerCreatesRequestFile(t *testing.T) {
	tmpDir := t.TempDir()
	orig := daemonDirFn
	daemonDirFn = func() (string, error) { return tmpDir, nil }
	defer func() { daemonDirFn = orig }()

	dummy := exec.Command("sleep", "10")
	if err := dummy.Start(); err != nil {
		t.Fatalf("start dummy: %v", err)
	}
	defer dummy.Process.Kill()
	dummyPID := dummy.Process.Pid

	if err := os.WriteFile(filepath.Join(tmpDir, "daemon.pid"), []byte(fmt.Sprintf("%d", dummyPID)), 0o600); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	st := &DaemonState{
		PID:       dummyPID,
		StartedAt: time.Now().UTC(),
		Servers:   map[string]ServerState{},
	}
	stateData, _ := json.Marshal(st)
	if err := os.WriteFile(filepath.Join(tmpDir, "daemon.json"), stateData, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	if err := LoadServer("test-echo-server"); err != nil {
		t.Fatalf("LoadServer: %v", err)
	}

	loadFile := filepath.Join(tmpDir, "daemon.load", "test-echo-server")
	data, err := os.ReadFile(loadFile)
	if err != nil {
		t.Fatalf("load-request file not created: %v", err)
	}
	if string(data) != "load" {
		t.Errorf("load file content = %q, want %q", string(data), "load")
	}
}

func TestLoadServerNoDaemon(t *testing.T) {
	tmpDir := t.TempDir()
	orig := daemonDirFn
	daemonDirFn = func() (string, error) { return tmpDir, nil }
	defer func() { daemonDirFn = orig }()

	err := LoadServer("test-echo-server")
	if err == nil {
		t.Fatal("expected error when daemon not running")
	}
}

func TestLoadServerSanitizesPathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	orig := daemonDirFn
	daemonDirFn = func() (string, error) { return tmpDir, nil }
	defer func() { daemonDirFn = orig }()

	dummy := exec.Command("sleep", "10")
	if err := dummy.Start(); err != nil {
		t.Fatalf("start dummy: %v", err)
	}
	defer dummy.Process.Kill()
	dummyPID := dummy.Process.Pid
	_ = os.WriteFile(filepath.Join(tmpDir, "daemon.pid"), []byte(fmt.Sprintf("%d", dummyPID)), 0o600)
	st := &DaemonState{PID: dummyPID, Servers: map[string]ServerState{}}
	stateData, _ := json.Marshal(st)
	_ = os.WriteFile(filepath.Join(tmpDir, "daemon.json"), stateData, 0o600)

	if err := LoadServer("../escape"); err != nil {
		t.Fatalf("LoadServer: %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "daemon.load", "escape")); err != nil {
		t.Fatalf("expected sanitized load file: %v", err)
	}
}

func TestConsumeLoadRequests(t *testing.T) {
	tmpDir := t.TempDir()
	orig := daemonDirFn
	daemonDirFn = func() (string, error) { return tmpDir, nil }
	defer func() { daemonDirFn = orig }()

	loadDir := filepath.Join(tmpDir, "daemon.load")
	if err := os.MkdirAll(loadDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(loadDir, "alpha"), []byte("load"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loadDir, "beta"), []byte("load"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := consumeLoadRequests()
	if len(got) != 2 {
		t.Fatalf("consumeLoadRequests = %v, want 2 names", got)
	}
	seen := map[string]bool{}
	for _, n := range got {
		seen[n] = true
	}
	if !seen["alpha"] || !seen["beta"] {
		t.Errorf("names = %v, want alpha and beta", got)
	}

	// Files must be consumed (deleted) so a later reconcile does not re-load.
	if entries, err := os.ReadDir(loadDir); err != nil {
		t.Fatalf("readdir: %v", err)
	} else if len(entries) != 0 {
		t.Errorf("expected load dir empty after consume, got %d entries", len(entries))
	}
}

func TestShouldStartBackingAfterInstall(t *testing.T) {
	// Table / fake: install-time load is independent of idleTimeout.
	// idleTimeout 0 already starts immediately; idleTimeout 60 must also
	// start once when a load request is present.
	tests := []struct {
		name         string
		idleTimeout  int
		loadRequested bool
		alreadyRunning bool
		wantStart    bool
	}{
		{"idle 60 + load request", 60, true, false, true},
		{"idle 0 + load request", 0, true, false, true},
		{"idle 60 no request", 60, false, false, false},
		{"idle 0 no request (always-on path)", 0, false, false, true},
		{"already running + load request", 60, true, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldStartBacking(tt.idleTimeout, tt.loadRequested, tt.alreadyRunning)
			if got != tt.wantStart {
				t.Errorf("shouldStartBacking(%d, load=%v, running=%v) = %v, want %v",
					tt.idleTimeout, tt.loadRequested, tt.alreadyRunning, got, tt.wantStart)
			}
		})
	}
}

// ── startBacking python → python3 ────────────────────────────────────────

const spawnArgLogger = "#!/bin/sh\n{\n  printf 'exe=%s\\n' \"$0\"\n  for a in \"$@\"; do printf 'arg=%s\\n' \"$a\"; done\n} > \"$PHAROS_SPAWN_LOG\"\nexec /bin/sleep 30\n"

func writePathExe(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func testDaemon(t *testing.T) *Daemon {
	t.Helper()
	tmp := t.TempDir()
	orig := daemonDirFn
	daemonDirFn = func() (string, error) { return tmp, nil }
	t.Cleanup(func() { daemonDirFn = orig })

	origWait := backingReadyWait
	backingReadyWait = 150 * time.Millisecond
	t.Cleanup(func() { backingReadyWait = origWait })

	return &Daemon{
		state: &DaemonState{Servers: map[string]ServerState{}},
		log:   log.New(io.Discard, "", 0),
	}
}

func waitSpawnLog(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil && len(b) > 0 {
			return string(b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for spawn log %s", path)
	return ""
}

func TestStartBacking_PythonMissingUsesPython3KeepsArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH exe stubs are POSIX scripts")
	}
	d := testDaemon(t)
	bin := t.TempDir()
	writePathExe(t, bin, "python3", spawnArgLogger)
	t.Setenv("PATH", bin)

	work := t.TempDir()
	logPath := filepath.Join(work, "spawned.txt")
	ms := &ManagedServer{
		Name:        "echo",
		Command:     "python",
		Args:        []string{"server.py"},
		WorkDir:     work,
		Env:         []string{"PHAROS_SPAWN_LOG=" + logPath},
		BackingPort: 1,
		daemon:      d,
	}
	if err := ms.startBacking(); err != nil {
		t.Fatalf("startBacking: %v", err)
	}
	t.Cleanup(func() { ms.stopBacking() })

	body := waitSpawnLog(t, logPath)
	if !strings.Contains(body, "exe="+filepath.Join(bin, "python3")) {
		t.Errorf("spawned exe = %q, want python3", body)
	}
	if !strings.Contains(body, "arg=server.py") {
		t.Errorf("args lost server.py: %q", body)
	}
	if strings.Contains(body, "test-echo-server") {
		t.Errorf("must not rewrite args to package name: %q", body)
	}
}

func TestStartBacking_PythonPresentKeepsPython(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH exe stubs are POSIX scripts")
	}
	d := testDaemon(t)
	bin := t.TempDir()
	writePathExe(t, bin, "python", spawnArgLogger)
	writePathExe(t, bin, "python3", "#!/bin/sh\necho unexpected-python3 > \"$PHAROS_SPAWN_LOG\"\nexec /bin/sleep 30\n")
	t.Setenv("PATH", bin)

	work := t.TempDir()
	logPath := filepath.Join(work, "spawned.txt")
	ms := &ManagedServer{
		Name:        "echo",
		Command:     "python server.py",
		WorkDir:     work,
		Env:         []string{"PHAROS_SPAWN_LOG=" + logPath},
		BackingPort: 1,
		daemon:      d,
	}
	if err := ms.startBacking(); err != nil {
		t.Fatalf("startBacking: %v", err)
	}
	t.Cleanup(func() { ms.stopBacking() })

	body := waitSpawnLog(t, logPath)
	if !strings.Contains(body, "exe="+filepath.Join(bin, "python")) {
		t.Errorf("spawned exe = %q, want python", body)
	}
	if strings.Contains(body, "unexpected-python3") || strings.Contains(body, filepath.Join(bin, "python3")) {
		t.Errorf("must not substitute python3 when python is on PATH: %q", body)
	}
	if !strings.Contains(body, "arg=server.py") {
		t.Errorf("args lost server.py: %q", body)
	}
}

func TestStartBacking_NeitherPythonErrorsWithoutAptHint(t *testing.T) {
	d := testDaemon(t)
	t.Setenv("PATH", t.TempDir())
	ms := &ManagedServer{
		Name:    "echo",
		Command: "python server.py",
		WorkDir: t.TempDir(),
		daemon:  d,
	}
	err := ms.startBacking()
	if err == nil {
		t.Fatal("expected error when neither python nor python3 is on PATH")
	}
	msg := err.Error()
	if !strings.Contains(msg, "python") {
		t.Errorf("error should mention python: %v", err)
	}
	if strings.Contains(msg, "python-is-python3") || strings.Contains(msg, "apt install") {
		t.Errorf("must not tell the user to apt-install python-is-python3: %v", err)
	}
}
