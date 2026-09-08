package daemon

// Tests for the D-1 fix: proxy port collisions must never silently leave a
// server unmanaged. listenFree walks upward past held ports; when no port
// can be bound the failure is recorded in DaemonState.BindFailures so
// `pharos daemon status` and the install path can report it honestly.

import (
	"net"
	"os"
	"testing"
	"time"
)

// TestListenFreeWalksPastHeldPort verifies the probe-hop walk: when the
// preferred port is held, listenFree must return a working listener on a
// nearby free port, not an error — so callers persist the actual port and
// the server stays managed. The held port is self-selected (port 0 then
// rebind) so the test never assumes 8421 is free (the user's real daemon
// may legitimately hold it).
func TestListenFreeWalksPastHeldPort(t *testing.T) {
	holder, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fixture listen :0: %v", err)
	}
	held := holder.Addr().(*net.TCPAddr).Port
	// holder stays open: it is the collision.

	got, actual, err := listenFree(held)
	if err != nil {
		t.Fatalf("listenFree(%d) with port held: %v", held, err)
	}
	defer got.Close()

	if actual == held {
		t.Fatalf("listenFree returned the held port %d", actual)
	}
	if actual <= held || actual >= held+maxPortProbeHops {
		t.Errorf("actual port %d outside walk range (%d, %d]", actual, held, held+maxPortProbeHops)
	}
	if tcp, ok := got.Addr().(*net.TCPAddr); !ok || tcp.Port != actual {
		t.Errorf("listener addr %v does not match returned port %d", got.Addr(), actual)
	}
}

// TestListenFreeNoUsablePorts verifies honest failure: with the entire walk
// window out of range, listenFree returns an error instead of silently
// returning a nil listener.
func TestListenFreeNoUsablePorts(t *testing.T) {
	if _, _, err := listenFree(70000); err == nil {
		t.Fatal("listenFree(70000) should fail: port out of range")
	}
}

// TestBindFailureLifecycle exercises the state-level contract the status and
// install paths rely on: record -> visible -> survives save/load round-trip
// -> clear on success -> empty after clear.
func TestBindFailureLifecycle(t *testing.T) {
	tmp := t.TempDir()
	orig := daemonDirFn
	setDaemonDirFn(func() (string, error) { return tmp, nil })
	defer func() { setDaemonDirFn(orig) }()

	st := &DaemonState{Servers: map[string]ServerState{}}
	st.recordBindFailure("weather", "no usable port near 8421")
	if st.BindFailures["weather"] == "" {
		t.Fatal("recordBindFailure did not record the failure")
	}

	if err := saveState(st); err != nil {
		t.Fatalf("saveState: %v", err)
	}
	loaded, err := loadState()
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	if got := loaded.BindFailures["weather"]; got == "" {
		t.Fatal("BindFailures lost across save/load round-trip")
	}

	loaded.clearBindFailure("weather")
	if _, still := loaded.BindFailures["weather"]; still {
		t.Fatal("clearBindFailure did not clear the failure")
	}
	if err := saveState(loaded); err != nil {
		t.Fatalf("saveState after clear: %v", err)
	}
	again, err := loadState()
	if err != nil {
		t.Fatalf("loadState after clear: %v", err)
	}
	if len(again.BindFailures) != 0 {
		t.Fatalf("expected empty BindFailures after clear+save, got %v", again.BindFailures)
	}
}

// TestStatusWireFormatIncludesBindFailures pins the DaemonStatus field the
// cmd-layer JSON encoder maps to bind_failures (omitempty): present when
// failures exist, absent otherwise.
func TestStatusWireFormatIncludesBindFailures(t *testing.T) {
	tmp := t.TempDir()
	orig := daemonDirFn
	setDaemonDirFn(func() (string, error) { return tmp, nil })
	defer func() { setDaemonDirFn(orig) }()

	st := &DaemonState{
		PID:       os.Getpid(),
		StartedAt: time.Now().UTC(),
		Servers:   map[string]ServerState{},
	}
	st.recordBindFailure("weather", "no usable port near 8421")
	if err := saveState(st); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	loaded, err := loadState()
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	if loaded.BindFailures["weather"] == "" {
		t.Fatal("status wire source lost BindFailures across round-trip")
	}
}
