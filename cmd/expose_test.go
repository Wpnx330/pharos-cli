package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/daemon"
	"github.com/Wpnx330/pharos-cli/internal/expose"
)

// ── Test seams ───────────────────────────────────────────────────────────

// isolateExposeDir points the expose store at a fresh temp dir for the
// duration of the test.
func isolateExposeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := expose.DirFn
	expose.DirFn = func() (string, error) { return dir, nil }
	t.Cleanup(func() { expose.DirFn = orig })
	return dir
}

// fakeAlive overrides both liveness seams: expose PIDs answer from the
// alive map, the daemon PID is a fixed answer.
func fakeAlive(t *testing.T, alive map[int]bool, daemonAlive bool) {
	t.Helper()
	origExpose, origDaemon := exposeAliveFn, exposeDaemonAliveFn
	exposeAliveFn = func(pid int) bool { return alive[pid] }
	exposeDaemonAliveFn = func(pid int) bool { return daemonAlive }
	t.Cleanup(func() {
		exposeAliveFn, exposeDaemonAliveFn = origExpose, origDaemon
	})
}

// captureStdout captures everything fn writes to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	fn()
	w.Close()
	data, _ := io.ReadAll(r)
	return string(data)
}

// captureStdoutErr captures stdout+stderr together (for commands that mix
// progress on stdout with error guidance on stderr).
func captureStdoutErr(t *testing.T, fn func()) string {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout, os.Stderr = wOut, wErr
	defer func() { os.Stdout, os.Stderr = origOut, origErr }()
	fn()
	wOut.Close()
	wErr.Close()
	outData, _ := io.ReadAll(rOut)
	errData, _ := io.ReadAll(rErr)
	return string(outData) + string(errData)
}

// captureExit runs fn and recovers its osExit panic, returning the code.
func captureExit(t *testing.T, fn func()) (code int) {
	t.Helper()
	origExit := osExit
	osExit = func(c int) { panic(exitSignal{c}) }
	defer func() { osExit = origExit }()
	defer func() {
		if r := recover(); r != nil {
			sig, ok := r.(exitSignal)
			if !ok {
				panic(r)
			}
			code = sig.code
		}
	}()
	fn()
	return -1
}

type exitSignal struct{ code int }

// daemonFixture builds a daemon state: daemon alive at PID 100 (the liveness
// seam decides), three managed servers with distinct proxy ports.
func daemonFixture(now time.Time) *daemon.DaemonState {
	return &daemon.DaemonState{
		PID:       100,
		StartedAt: now.Add(-time.Hour),
		Servers: map[string]daemon.ServerState{
			"web":       {State: "running", PID: 200, Port: 8421},
			"docs-srv":  {State: "unloaded", PID: 0, Port: 8422},
			"tools-srv": {State: "running", PID: 201, Port: 8423},
		},
	}
}

// httpGetLocal dials the loopback expose listener.
func httpGetLocal(port int) (*http.Response, error) {
	return http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
}

// ── parseExposeAddr ──────────────────────────────────────────────────────

func TestParseExposeAddr(t *testing.T) {
	cases := []struct {
		name     string
		addr     string
		wantHost string
		wantPort int
		wantErr  bool
	}{
		{"port only", ":9500", "", 9500, false},
		{"all interfaces", "0.0.0.0:9500", "0.0.0.0", 9500, false},
		{"loopback", "127.0.0.1:9500", "127.0.0.1", 9500, false},
		{"hostname", "localhost:9500", "localhost", 9500, false},
		{"ipv6", "[::1]:9500", "::1", 9500, false},
		{"no port", "9500", "", 0, true},
		{"port zero", ":0", "", 0, true},
		{"port negative", ":-1", "", 0, true},
		{"port too large", ":70000", "", 0, true},
		{"port not numeric", ":http", "", 0, true},
		{"empty", "", "", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, port, err := parseExposeAddr(tc.addr)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseExposeAddr(%q) = %q, %d, want error", tc.addr, host, port)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseExposeAddr(%q): %v", tc.addr, err)
			}
			if host != tc.wantHost || port != tc.wantPort {
				t.Errorf("parseExposeAddr(%q) = %q, %d; want %q, %d", tc.addr, host, port, tc.wantHost, tc.wantPort)
			}
		})
	}
}

// ── resolveExposeTarget (precondition law: friendly error, no silent start)

func TestResolveExposeTargetDaemonNotRunning(t *testing.T) {
	state := daemonFixture(time.Now())
	state.PID = 0
	_, _, err := resolveExposeTarget(state, "web")
	if err == nil || !strings.Contains(err.Error(), "daemon is not running") {
		t.Errorf("err = %v, want 'daemon is not running'", err)
	}
}

func TestResolveExposeTargetDeadDaemonPID(t *testing.T) {
	fakeAlive(t, map[int]bool{}, false)
	state := daemonFixture(time.Now())
	state.PID = 100 // fixture pid; the seam answers dead
	_, _, err := resolveExposeTarget(state, "web")
	if err == nil || !strings.Contains(err.Error(), "daemon is not running") {
		t.Errorf("err = %v, want 'daemon is not running'", err)
	}
}

func TestResolveExposeTargetNoServersManaged(t *testing.T) {
	fakeAlive(t, nil, true) // daemon pid alive; the servers map is what varies
	state := daemonFixture(time.Now())
	state.Servers = nil
	_, managed, err := resolveExposeTarget(state, "web")
	if err == nil || !strings.Contains(err.Error(), "not managing any servers") {
		t.Errorf("err = %v, want 'not managing any servers'", err)
	}
	if managed != nil {
		t.Errorf("managed = %v, want nil", managed)
	}
}

func TestResolveExposeTargetUnknownNameListsManagedSorted(t *testing.T) {
	fakeAlive(t, nil, true)
	state := daemonFixture(time.Now())
	_, managed, err := resolveExposeTarget(state, "nope")
	if err == nil || !strings.Contains(err.Error(), `server "nope" is not managed`) {
		t.Errorf("err = %v, want not-managed error", err)
	}
	if len(managed) != 3 || managed[0] != "docs-srv" || managed[1] != "tools-srv" || managed[2] != "web" {
		t.Errorf("managed = %v, want sorted [docs-srv tools-srv web]", managed)
	}
}

func TestResolveExposeTargetReturnsDaemonProxyPort(t *testing.T) {
	fakeAlive(t, nil, true)
	state := daemonFixture(time.Now())
	port, managed, err := resolveExposeTarget(state, "web")
	if err != nil {
		t.Fatalf("resolveExposeTarget: %v", err)
	}
	if port != 8421 {
		t.Errorf("port = %d, want 8421 (daemon proxy port)", port)
	}
	if managed != nil {
		t.Errorf("managed = %v, want nil on success", managed)
	}
}

// ── classifyExpose ───────────────────────────────────────────────────────

func TestClassifyExpose(t *testing.T) {
	fakeAlive(t, map[int]bool{4242: true}, true)
	now := time.Now()

	cases := []struct {
		name     string
		entry    expose.Entry
		wantLive bool
		wantExp  bool
		wantSt   string
	}{
		{
			name:   "expired wins over live pid",
			entry:  expose.Entry{PID: 4242, ExpiresAt: now.Add(-time.Minute)},
			wantExp: true,
			wantSt: "expired",
		},
		{
			name:     "live pid inside ttl",
			entry:    expose.Entry{PID: 4242, ExpiresAt: now.Add(time.Hour)},
			wantLive: true,
			wantSt:   "live",
		},
		{
			name:   "dead pid inside ttl",
			entry:  expose.Entry{PID: 999999, ExpiresAt: now.Add(time.Hour)},
			wantSt: "stopped",
		},
		{
			name:   "pid zero inside ttl",
			entry:  expose.Entry{PID: 0, ExpiresAt: now.Add(time.Hour)},
			wantSt: "stopped",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			live, expired, status := classifyExpose(tc.entry, now)
			if live != tc.wantLive || expired != tc.wantExp || status != tc.wantSt {
				t.Errorf("classifyExpose = (%v, %v, %q); want (%v, %v, %q)",
					live, expired, status, tc.wantLive, tc.wantExp, tc.wantSt)
			}
		})
	}
}

// ── list --json (W1.1 purity: omitempty discipline, never null) ──────────

func TestExposeListJSONShape(t *testing.T) {
	isolateExposeDir(t)
	now := time.Now()
	seed := map[string]expose.Entry{
		"web": {Name: "web", PID: 4242, Addr: ":9500", Port: 9500, BackingPort: 8421,
			TokenHash: "h1", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		"dead": {Name: "dead", PID: 0, Addr: "127.0.0.1:9501", Port: 9501, BackingPort: 8422,
			TokenHash: "h2", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		"old": {Name: "old", PID: 4242, Addr: ":9502", Port: 9502, BackingPort: 8423,
			TokenHash: "h3", CreatedAt: now.Add(-25 * time.Hour), ExpiresAt: now.Add(-time.Hour)},
	}
	for _, e := range seed {
		if err := expose.UpsertEntry(e); err != nil {
			t.Fatalf("seed %s: %v", e.Name, err)
		}
	}

	fakeAlive(t, map[int]bool{4242: true}, true)
	t.Setenv("PHAROS_JSON", "1")
	out := captureStdout(t, func() {
		if err := runExposeList(nil, nil); err != nil {
			t.Errorf("runExposeList: %v", err)
		}
	})

	var doc struct {
		Exposes []map[string]any `json:"exposes"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(doc.Exposes) != 3 {
		t.Fatalf("exposes = %d entries, want 3\n%s", len(doc.Exposes), out)
	}

	byName := map[string]map[string]any{}
	order := []string{}
	for _, m := range doc.Exposes {
		name := m["name"].(string)
		byName[name] = m
		order = append(order, name)
	}
	if strings.Join(order, ",") != "dead,old,web" {
		t.Errorf("entry order = %v, want store (sorted) order dead,old,web", order)
	}

	// dead: pid omitted (omitempty — never null), live/expired always present.
	if _, present := byName["dead"]["pid"]; present {
		t.Error("dead entry has pid key, want omitted for pid=0")
	}
	if v, _ := byName["dead"]["live"].(bool); v {
		t.Error("dead.live = true, want false")
	}
	if v, _ := byName["dead"]["expired"].(bool); v {
		t.Error("dead entry expired = true, want false")
	}

	// old: past expiresAt → expired=true even though its pid is "alive".
	if v, _ := byName["old"]["expired"].(bool); !v {
		t.Error("old.expired = false, want true")
	}
	if v, _ := byName["old"]["live"].(bool); v {
		t.Error("old.live = true, want false")
	}

	// web: live.
	if v, _ := byName["web"]["live"].(bool); !v {
		t.Error("web.live = false, want true")
	}
	if pid, _ := byName["web"]["pid"].(float64); pid != 4242 {
		t.Errorf("web.pid = %v, want 4242", byName["web"]["pid"])
	}
	if bp, _ := byName["web"]["backingPort"].(float64); bp != 8421 {
		t.Errorf("web.backingPort = %v, want 8421", bp)
	}
	if _, err := time.Parse(time.RFC3339, byName["web"]["expiresAt"].(string)); err != nil {
		t.Errorf("web.expiresAt = %v is not RFC3339", byName["web"]["expiresAt"])
	}
}

func TestExposeListJSONEmpty(t *testing.T) {
	isolateExposeDir(t)
	t.Setenv("PHAROS_JSON", "1")
	out := captureStdout(t, func() { _ = runExposeList(nil, nil) })
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	exposes, ok := doc["exposes"].([]any)
	if !ok || len(exposes) != 0 {
		t.Errorf("exposes = %v, want empty array (never null)", doc["exposes"])
	}
}

func TestExposeListHumanEmpty(t *testing.T) {
	isolateExposeDir(t)
	out := captureStdout(t, func() { _ = runExposeList(nil, nil) })
	if !strings.Contains(out, "No exposes configured") {
		t.Errorf("empty human list missing guidance:\n%s", out)
	}
	if !strings.Contains(out, "pharos expose <name> --addr :9500") {
		t.Errorf("empty human list missing example:\n%s", out)
	}
}

// ── handoff (token printed once; JSON is the exact spec shape) ───────────

func TestPrintExposeHandoffJSONExactShape(t *testing.T) {
	t.Setenv("PHAROS_JSON", "1")
	now := time.Now()
	entry := expose.Entry{
		Name: "web", PID: 1, Addr: ":9500", Port: 9500, BackingPort: 8421,
		TokenHash: "deadbeef", CreatedAt: now, ExpiresAt: now.Add(2 * time.Hour),
	}
	out := captureStdout(t, func() { printExposeHandoff(entry, "tok-123", 2*time.Hour) })

	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("handoff output is not valid JSON: %v\n%s", err, out)
	}
	want := map[string]bool{"name": false, "addr": false, "port": false, "expiresAt": false, "token": false}
	for k := range doc {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected key %q in start JSON (spec: {name, addr, port, expiresAt, token})", k)
		} else {
			want[k] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("start JSON missing key %q", k)
		}
	}
	if doc["name"] != "web" || doc["addr"] != ":9500" || doc["port"].(float64) != 9500 {
		t.Errorf("start JSON scalars = %v", doc)
	}
	if doc["token"] != "tok-123" {
		t.Errorf("start JSON token = %v, want the one-time handoff token", doc["token"])
	}
	if _, err := time.Parse(time.RFC3339, doc["expiresAt"].(string)); err != nil {
		t.Errorf("expiresAt = %v is not RFC3339", doc["expiresAt"])
	}
}

func TestPrintExposeHandoffHumanShowsTokenOnce(t *testing.T) {
	now := time.Now()
	entry := expose.Entry{
		Name: "web", PID: 1, Addr: ":9500", Port: 9500, BackingPort: 8421,
		TokenHash: expose.HashToken("the-token-value"), CreatedAt: now, ExpiresAt: now.Add(2 * time.Hour),
	}
	out := captureStdout(t, func() { printExposeHandoff(entry, "the-token-value", 2*time.Hour) })

	if !strings.Contains(out, "the-token-value") {
		t.Errorf("handoff missing the token:\n%s", out)
	}
	if !strings.Contains(out, "shown once") || !strings.Contains(out, "SHA-256") {
		t.Errorf("handoff missing one-time/hash-at-rest note:\n%s", out)
	}
	if !strings.Contains(out, `Authorization: Bearer the-token-value`) {
		t.Errorf("handoff missing ready-to-copy client snippet:\n%s", out)
	}
	if !strings.Contains(out, "127.0.0.1:8421") {
		t.Errorf("handoff missing the loopback backing target:\n%s", out)
	}
}

// ── stop ─────────────────────────────────────────────────────────────────

func TestExposeStopRemovesStaleEntry(t *testing.T) {
	isolateExposeDir(t)
	fakeAlive(t, map[int]bool{}, true) // recorded pid is dead

	now := time.Now()
	if err := expose.UpsertEntry(expose.Entry{
		Name: "web", PID: 999999, Addr: ":9500", Port: 9500, BackingPort: 8421,
		TokenHash: "h", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out := captureStdout(t, func() { runExposeStop(nil, []string{"web"}) })
	if !strings.Contains(out, "removed stale") {
		t.Errorf("stale-stop output missing confirmation:\n%s", out)
	}
	if _, ok, _ := expose.GetEntry("web"); ok {
		t.Error("stale entry still present after stop")
	}
}

func TestExposeStopUnknownNameExitsNonZero(t *testing.T) {
	isolateExposeDir(t)
	code := captureExit(t, func() { runExposeStop(nil, []string{"ghost"}) })
	if code != 1 {
		t.Errorf("exit code = %d, want 1 for unknown expose name", code)
	}
}

func TestExposeStopGracefulViaRealServeLoop(t *testing.T) {
	isolateExposeDir(t)
	origWait := exposeStopWait
	exposeStopWait = 3 * time.Second
	t.Cleanup(func() { exposeStopWait = origWait })

	token := "integration-token"
	ln, err := expose.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	now := time.Now()
	entry := expose.Entry{
		Name: "web", PID: os.Getpid(), Addr: ln.Addr().String(), Port: port,
		BackingPort: 1, TokenHash: expose.HashToken(token),
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := expose.UpsertEntry(entry); err != nil {
		t.Fatalf("seed: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = expose.ServeListener(ln, expose.ServeConfig{
			Entry:    entry,
			Token:    token,
			TTL:      time.Hour,
			StopPoll: 10 * time.Millisecond,
			SignalCh: make(chan os.Signal, 1),
		})
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := httpGetLocal(port)
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expose listener never came up")
		}
		time.Sleep(10 * time.Millisecond)
	}

	out := captureStdout(t, func() { runExposeStop(nil, []string{"web"}) })
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("serve loop did not exit after stop request")
	}
	if !strings.Contains(out, "stopped") {
		t.Errorf("stop output missing confirmation:\n%s", out)
	}
	if _, ok, _ := expose.GetEntry("web"); ok {
		t.Error("entry still present after graceful stop")
	}
	if expose.StopRequested("web") {
		t.Error("stop file not consumed by the stopped process")
	}
}

func TestExposeStopTimesOutOnLiveProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	isolateExposeDir(t)
	origWait := exposeStopWait
	exposeStopWait = 400 * time.Millisecond
	t.Cleanup(func() { exposeStopWait = origWait })

	fakeAlive(t, map[int]bool{555555: true}, true) // alive forever, never cleans up
	now := time.Now()
	if err := expose.UpsertEntry(expose.Entry{
		Name: "web", PID: 555555, Addr: ":9500", Port: 9500, BackingPort: 8421,
		TokenHash: "h", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var code int
	out := captureStdoutErr(t, func() {
		code = captureExit(t, func() { runExposeStop(nil, []string{"web"}) })
	})
	if code != 1 {
		t.Errorf("exit code = %d, want 1 (stop did not complete in time)", code)
	}
	if !strings.Contains(out, "did not stop within") {
		t.Errorf("timeout output missing guidance:\n%s", out)
	}
	if _, ok, _ := expose.GetEntry("web"); !ok {
		t.Error("entry was removed despite the stop never being acknowledged")
	}
}
