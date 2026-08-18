package cmd

import (
	"net"
	"testing"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/install"
	"github.com/Wpnx330/pharos-cli/internal/runtime"
)

func TestParseDaemonStateMapNoRunningField(t *testing.T) {
	const raw = `{
		"pid": 4242,
		"startedAt": "2026-08-18T12:00:00Z",
		"servers": {
			"test-echo-server": {
				"state": "running",
				"pid": 999,
				"port": 8421,
				"lastActivity": "2026-08-18T12:01:00Z"
			}
		}
	}`

	got := parseDaemonState([]byte(raw))
	ds, ok := got["test-echo-server"]
	if !ok {
		t.Fatalf("overlay missing test-echo-server; keys=%v", keysOf(got))
	}
	if !daemonStateIsRunning(ds) {
		t.Errorf("state = %q, want running", ds.State)
	}
	if ds.Port != 8421 {
		t.Errorf("overlay port = %d, want 8421 (proxy; display must not use this)", ds.Port)
	}
	if ds.PID != 999 {
		t.Errorf("overlay pid = %d, want 999", ds.PID)
	}
}

func TestParseDaemonStateLegacyArray(t *testing.T) {
	const raw = `{
		"pid": 7,
		"servers": [
			{"name": "legacy-echo", "state": "running", "pid": 11, "port": 8421}
		]
	}`

	got := parseDaemonState([]byte(raw))
	ds, ok := got["legacy-echo"]
	if !ok {
		t.Fatalf("legacy array overlay missing legacy-echo; keys=%v", keysOf(got))
	}
	if !daemonStateIsRunning(ds) {
		t.Errorf("state = %q, want running", ds.State)
	}
}

func TestKind2ListenPortIgnoresDaemonProxyPort(t *testing.T) {
	launch := packageLaunch{
		Bin:       "python server.py",
		Transport: "http-sse",
	}
	daemon := map[string]daemonServerState{
		"test-echo-server": {Name: "test-echo-server", State: "running", Port: 8421, PID: 999},
	}

	got := kind2ListenPort("test-echo-server", launch, daemon)
	if got != defaultKind2ListenPort {
		t.Errorf("kind2ListenPort = %d, want %d (listen/backing), not daemon proxy 8421", got, defaultKind2ListenPort)
	}
}

func TestApplyKind2DaemonRunningDoesNotCopyProxyPort(t *testing.T) {
	daemon := map[string]daemonServerState{
		"test-echo-server": {Name: "test-echo-server", State: "running", Port: 8421},
	}
	st := applyKind2DaemonRunning("test-echo-server", runtime.ProcessStatus{}, daemon)
	if !st.Running {
		t.Fatal("Running = false, want true from daemon overlay")
	}
	if st.Port != 0 {
		t.Errorf("Port = %d, must not copy daemon proxy port onto ProcessStatus", st.Port)
	}
}

func TestBuildListRowKind2DaemonProxyPortDisplaysListenPort(t *testing.T) {
	pkg := install.InstalledPackage{
		Name:      "test-echo-server",
		Version:   "0.2.6",
		Transport: "http-sse",
		Location:  "/tmp/store/test-echo-server/0.2.6",
	}
	launch := packageLaunch{
		Bin:       "python server.py",
		Transport: "http-sse",
	}
	daemon := map[string]daemonServerState{
		"test-echo-server": {Name: "test-echo-server", State: "running", Port: 8421, PID: 999},
	}

	row := buildListRow(pkg, launch, runtime.ProcessStatus{}, 2048, daemon)
	if row.Kind != 2 {
		t.Fatalf("kind = %d, want 2", row.Kind)
	}
	if row.Status != "running" {
		t.Errorf("status = %q, want running (daemon overlay)", row.Status)
	}
	if row.Port != "8765" {
		t.Errorf("displayed PORT = %q, want 8765 (listen), not daemon proxy 8421", row.Port)
	}
}

func TestWaitForKind2ListenOpenAndClosed(t *testing.T) {
	if waitForKind2Listen(0, time.Second) {
		t.Error("port 0 must not report ready")
	}
	if waitForKind2Listen(1, 0) {
		t.Error("zero timeout must not report ready")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if !waitForKind2Listen(port, time.Second) {
		ln.Close()
		t.Fatalf("open port %d should be ready", port)
	}
	ln.Close()

	// Closed high port: fail fast.
	if waitForKind2Listen(port, 250*time.Millisecond) {
		t.Errorf("closed port %d must not report ready", port)
	}
}

func keysOf(m map[string]daemonServerState) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
