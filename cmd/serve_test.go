package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── fixture registry ────────────────────────────────────────────────────────

// serveFixtureRegistry stands in for the PHAROS registry for serve tests:
// search + package detail + one 404 name, over localhost only. The
// subprocess talks to it over TCP with an isolated HOME config.
func serveFixtureRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/search"):
			if r.URL.Query().Get("q") == "nothing" {
				_, _ = io.WriteString(w, `{"results":[],"boosted":[],"nextCursor":"","total":0}`)
				return
			}
			_, _ = io.WriteString(w, `{"results":[`+
				`{"name":"git-mcp","version":"1.2.0","title":"Git MCP","description":"Git repository tools for agents","score":5,"downloads30d":1234,"transport":["stdio"],"source_registry":"pharos","publisher":{"namespace":"acme"},"scorecard":{"score":87,"grade":"A"}}`+
				`],"boosted":[],"nextCursor":"","total":1}`)
		case strings.HasPrefix(r.URL.Path, "/v1/packages/missing-pkg"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"package not found"}}`)
		case strings.HasPrefix(r.URL.Path, "/v1/packages/"):
			_, _ = io.WriteString(w, `{
				"name":"git-mcp","title":"Git MCP","description":"Git repository tools for agents","license":"MIT",
				"repo_url":"https://github.com/acme/git-mcp",
				"dist_tags":{"latest":"1.2.0"},
				"publisher":{"namespace":"acme"},
				"scorecard":{"score":87,"grade":"A","heuristic":"v1"},
				"versions":[{"version":"1.2.0","status":"active","created_at":"2026-01-01T00:00:00Z",
					"manifest":{"name":"git-mcp","version":"1.2.0","transport":"stdio","runtime":"npx","package":"@acme/git-mcp","capabilities":["tools"]}}]
			}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// serveHome isolates HOME and points ~/.pharos/config.json at srvURL. The
// env vars are returned so the subprocess (a separate process — t.Setenv
// alone does not reach it) inherits the same isolation.
func serveHome(t *testing.T, srvURL string) (home string, env []string) {
	t.Helper()
	home = isolateHome(t)
	dir := filepath.Join(home, ".pharos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"registry":` + strconv.Quote(srvURL) + `}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	env = append(os.Environ(),
		"HOME="+home, "USERPROFILE="+home,
		"APPDATA="+filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"),
	)
	return home, env
}

// ── serve subprocess driver ─────────────────────────────────────────────────

// serveProc is the test binary re-executed in serve-helper mode (the real
// `pharos serve` pipeline over real stdio pipes) plus a line-based driver.
type serveProc struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	lines    chan string
	readDone chan struct{}
	stderr   *bytes.Buffer
	closeOne sync.Once
}

// startServeHelper spawns the helper. lockDir becomes the process working
// directory: lockfile.DefaultPath prefers ./pharos.lock, so tests control
// list_installed by seeding that file (a clean dir keeps the repo's own
// lock artifacts out of the assertions).
func startServeHelper(t *testing.T, env []string, lockDir string, allowInstall bool) *serveProc {
	t.Helper()
	// os.Executable is absolute on every GOOS (os.Args[0] can be a bare
	// name on Windows, which would break PATH-based re-exec).
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmdEnv := append(env, "PHAROS_SERVE_HELPER=1")
	if allowInstall {
		cmdEnv = append(cmdEnv, "PHAROS_SERVE_ALLOW_INSTALL=1")
	}
	cmd := &exec.Cmd{Path: exe, Dir: lockDir, Env: cmdEnv}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr // os/exec drains this into the buffer before Wait returns
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve helper: %v", err)
	}
	p := &serveProc{
		cmd:      cmd,
		stdin:    stdin,
		lines:    make(chan string, 32),
		readDone: make(chan struct{}),
		stderr:   stderr,
	}
	go func() {
		defer close(p.readDone)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			p.lines <- sc.Text()
		}
	}()
	t.Cleanup(func() { p.close() })
	return p
}

// close shuts the helper down: stdin close (graceful EOF) → drain the
// stdout reader → Wait (the os/exec law: never Wait before reads finish).
func (p *serveProc) close() {
	p.closeOne.Do(func() {
		_ = p.stdin.Close()
		select {
		case <-p.readDone:
		case <-time.After(5 * time.Second):
		}
		_ = p.cmd.Wait()
	})
}

func (p *serveProc) sendLine(t *testing.T, line string) {
	t.Helper()
	if _, err := fmt.Fprintf(p.stdin, "%s\n", line); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

func (p *serveProc) sendRequest(t *testing.T, id int, method string, params any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	p.sendLine(t, string(data))
}

func (p *serveProc) notify(t *testing.T, method string) {
	t.Helper()
	p.sendLine(t, `{"jsonrpc":"2.0","method":`+mustJSONString(method)+`}`)
}

// nextLine reads the next stdout line with a deadline.
func (p *serveProc) nextLine(t *testing.T) string {
	t.Helper()
	select {
	case line, ok := <-p.lines:
		if !ok {
			t.Fatalf("serve helper stdout closed early (stderr: %q)", p.stderr.String())
		}
		return line
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out reading next stdout line (stderr: %q)", p.stderr.String())
		return ""
	}
}

// request sends one JSON-RPC request and returns the next response frame
// (asserting jsonrpc + numeric id echo).
func (p *serveProc) request(t *testing.T, id int, method string, params any) map[string]any {
	t.Helper()
	p.sendRequest(t, id, method, params)
	return parseServeResponse(t, p.nextLine(t), id)
}

func parseServeResponse(t *testing.T, line string, wantID int) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("stdout line is not JSON: %q (%v)", line, err)
	}
	if m["jsonrpc"] != "2.0" {
		t.Fatalf("stdout line jsonrpc = %v, want \"2.0\": %q", m["jsonrpc"], line)
	}
	gotID, ok := m["id"].(float64)
	if !ok || int(gotID) != wantID {
		t.Fatalf("response id = %v, want %d (line %q)", m["id"], wantID, line)
	}
	return m
}

// resultOf extracts the result object from a response frame.
func resultOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result object: %v", m)
	}
	return res
}

// toolText extracts content[0].text from a tools/call result.
func toolText(t *testing.T, res map[string]any) string {
	t.Helper()
	content, ok := res["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("result has no content: %v", res)
	}
	block, ok := content[0].(map[string]any)
	if !ok || block["type"] != "text" {
		t.Fatalf("content[0] = %v, want text block", content[0])
	}
	text, _ := block["text"].(string)
	return text
}

func mustJSONString(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// ── 1. initialize handshake ─────────────────────────────────────────────────

// TestServeInitializeHandshake spawns the serve helper, performs the
// initialize handshake, and asserts protocolVersion + serverInfo; the
// notifications/initialized ack must produce no stdout frame — the next
// response must be the ping's, in order.
func TestServeInitializeHandshake(t *testing.T) {
	srv := serveFixtureRegistry(t)
	_, env := serveHome(t, srv.URL)
	p := startServeHelper(t, env, t.TempDir(), false)

	m := p.request(t, 1, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "serve-test", "version": "0.0.0"},
	})
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize response has no result: %v", m)
	}
	if res["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion = %v, want 2024-11-05", res["protocolVersion"])
	}
	si, ok := res["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("serverInfo = %v, want object", res["serverInfo"])
	}
	if si["name"] != "pharos" {
		t.Errorf("serverInfo.name = %v, want pharos", si["name"])
	}
	if si["version"] != Version {
		t.Errorf("serverInfo.version = %v, want cmd.Version %q", si["version"], Version)
	}
	caps, ok := res["capabilities"].(map[string]any)
	if !ok || caps["tools"] == nil {
		t.Errorf("capabilities = %v, want tools entry", res["capabilities"])
	}

	// The initialized notification is acked by silence: the next stdout
	// frame must be the ping response, not anything the notification
	// emitted.
	p.notify(t, "notifications/initialized")
	pm := p.request(t, 2, "ping", nil)
	pingRes, ok := pm["result"].(map[string]any)
	if !ok || len(pingRes) != 0 {
		t.Errorf("ping result = %v, want empty object", pm["result"])
	}
}

// ── 2. tools/list ───────────────────────────────────────────────────────────

// serveToolNames validates every tool entry and returns names + schemas.
func serveToolNames(t *testing.T, m map[string]any) (names []string, schemas map[string]map[string]any) {
	t.Helper()
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list response has no result: %v", m)
	}
	tools, ok := res["tools"].([]any)
	if !ok {
		t.Fatalf("tools = %v", res["tools"])
	}
	schemas = map[string]map[string]any{}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		names = append(names, name)
		if tool["description"] == "" {
			t.Errorf("tool %q has empty description", name)
		}
		schema, ok := tool["inputSchema"].(map[string]any)
		if !ok {
			t.Fatalf("tool %q inputSchema = %v, want object", name, tool["inputSchema"])
		}
		if schema["type"] != "object" {
			t.Errorf("tool %q schema type = %v, want object", name, schema["type"])
		}
		if _, ok := schema["properties"].(map[string]any); !ok {
			t.Errorf("tool %q schema properties = %v, want object", name, schema["properties"])
		}
		schemas[name] = schema
	}
	return names, schemas
}

// TestServeToolsList — 3 tools by default (install omitted); 4 with
// --allow-install; every tool carries a valid inputSchema; install's
// schema requires "name" and offers "version".
func TestServeToolsList(t *testing.T) {
	srv := serveFixtureRegistry(t)
	_, env := serveHome(t, srv.URL)

	defaultProc := startServeHelper(t, env, t.TempDir(), false)
	names, _ := serveToolNames(t, defaultProc.request(t, 1, "tools/list", map[string]any{}))
	if len(names) != 3 {
		t.Fatalf("default tools = %v, want exactly 3", names)
	}
	for _, want := range []string{"search", "info", "list_installed"} {
		if !containsName(names, want) {
			t.Errorf("tools missing %q: %v", want, names)
		}
	}
	if containsName(names, "install") {
		t.Errorf("install must be omitted without --allow-install: %v", names)
	}

	allowProc := startServeHelper(t, env, t.TempDir(), true)
	names4, schemas := serveToolNames(t, allowProc.request(t, 1, "tools/list", map[string]any{}))
	if len(names4) != 4 || !containsName(names4, "install") {
		t.Fatalf("tools with --allow-install = %v, want 4 incl. install", names4)
	}
	required, ok := schemas["install"]["required"].([]any)
	if !ok || len(required) != 1 || required[0] != "name" {
		t.Errorf("install schema required = %v, want [name]", schemas["install"]["required"])
	}
	props, ok := schemas["install"]["properties"].(map[string]any)
	if !ok {
		t.Fatal("install schema has no properties object")
	}
	for _, want := range []string{"name", "version"} {
		if _, ok := props[want]; !ok {
			t.Errorf("install schema missing property %q", want)
		}
	}
}

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// ── 3. tools/call search + info (+ list_installed) ──────────────────────────

// TestServeToolSearchInfo — with the config pointed at the fixture
// registry via the isolated HOME, search and info return structured JSON
// in content[0].text; list_installed reflects a seeded pharos.lock.
func TestServeToolSearchInfo(t *testing.T) {
	srv := serveFixtureRegistry(t)
	_, env := serveHome(t, srv.URL)
	lockDir := t.TempDir()
	lock := `{"version":1,"servers":{"seeded-srv":{"version":"2.0.0","integrity":"sha512-x","transport":"stdio","resolved":"https://x/t.tgz","installedAt":"2026-09-08T00:00:00Z","clients":["cursor"],"origin":{"kind":"registry","ref":"seeded-srv@2.0.0","installed_via":"pharos install"},"pinnedAt":"2.0.0"}}}`
	if err := os.WriteFile(filepath.Join(lockDir, "pharos.lock"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	p := startServeHelper(t, env, lockDir, false)

	// search
	res := resultOf(t, p.request(t, 1, "tools/call", map[string]any{
		"name":      "search",
		"arguments": map[string]any{"query": "git", "limit": 5},
	}))
	if res["isError"] == true {
		t.Fatalf("search returned isError: %s", toolText(t, res))
	}
	var hits []map[string]any
	if err := json.Unmarshal([]byte(toolText(t, res)), &hits); err != nil {
		t.Fatalf("search payload is not a JSON array: %q (%v)", toolText(t, res), err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	hit := hits[0]
	if hit["name"] != "git-mcp" || hit["version"] != "1.2.0" || hit["description"] != "Git repository tools for agents" {
		t.Errorf("hit = %v", hit)
	}
	if hit["downloads"] != float64(1234) {
		t.Errorf("downloads = %v, want 1234", hit["downloads"])
	}
	if transports, _ := hit["transports"].([]any); len(transports) != 1 || transports[0] != "stdio" {
		t.Errorf("transports = %v, want [stdio]", hit["transports"])
	}
	if hit["grade"] != "A" {
		t.Errorf("grade = %v, want A", hit["grade"])
	}

	// info
	res2 := resultOf(t, p.request(t, 2, "tools/call", map[string]any{
		"name":      "info",
		"arguments": map[string]any{"name": "git-mcp"},
	}))
	if res2["isError"] == true {
		t.Fatalf("info returned isError: %s", toolText(t, res2))
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(toolText(t, res2)), &doc); err != nil {
		t.Fatalf("info payload is not a JSON object: %q (%v)", toolText(t, res2), err)
	}
	if doc["latest"] != "1.2.0" {
		t.Errorf("latest = %v, want 1.2.0", doc["latest"])
	}
	if hint, _ := doc["install_hint"].(string); !strings.Contains(hint, "pharos install git-mcp@1.2.0") {
		t.Errorf("install_hint = %q", hint)
	}
	if doc["transport"] != "stdio" {
		t.Errorf("transport = %v, want stdio", doc["transport"])
	}

	// list_installed (lockfile seeded in the helper's cwd)
	res3 := resultOf(t, p.request(t, 3, "tools/call", map[string]any{"name": "list_installed"}))
	if res3["isError"] == true {
		t.Fatalf("list_installed returned isError: %s", toolText(t, res3))
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(toolText(t, res3)), &entries); err != nil {
		t.Fatalf("list_installed payload is not a JSON array: %q (%v)", toolText(t, res3), err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e["name"] != "seeded-srv" || e["version"] != "2.0.0" {
		t.Errorf("entry = %v", e)
	}
	if e["origin"] != "registry" || e["pinned"] != true {
		t.Errorf("origin/pinned = %v/%v, want registry/true", e["origin"], e["pinned"])
	}
	if clients, _ := e["clients"].([]any); len(clients) != 1 || clients[0] != "cursor" {
		t.Errorf("clients = %v, want [cursor]", e["clients"])
	}
}

// ── 4. unknown methods, bad params, install gate ────────────────────────────

// TestServeToolsCallUnknownAndErrors — -32601 unknown method; -32602 bad
// params (missing name, non-object arguments); install without the flag
// → isError result naming --allow-install.
func TestServeToolsCallUnknownAndErrors(t *testing.T) {
	srv := serveFixtureRegistry(t)
	_, env := serveHome(t, srv.URL)
	p := startServeHelper(t, env, t.TempDir(), false)

	// Unknown method → -32601, id echoed.
	m := p.request(t, 1, "resources/list", map[string]any{})
	errObj, ok := m["error"].(map[string]any)
	if !ok || errObj["code"] != float64(-32601) {
		t.Fatalf("unknown method response = %v, want -32601 error", m)
	}
	if msg, _ := errObj["message"].(string); msg == "" {
		t.Error("error message is empty")
	}

	// tools/call without a tool name → -32602.
	m2 := p.request(t, 2, "tools/call", map[string]any{"arguments": map[string]any{}})
	if codeOf(t, m2) != -32602 {
		t.Fatalf("nameless tools/call = %v, want -32602", m2)
	}

	// tools/call with non-object arguments → -32602.
	m3 := p.request(t, 3, "tools/call", map[string]any{"name": "search", "arguments": "nope"})
	if codeOf(t, m3) != -32602 {
		t.Fatalf("non-object arguments = %v, want -32602", m3)
	}

	// install without --allow-install → isError tool result naming the flag.
	m4 := p.request(t, 4, "tools/call", map[string]any{
		"name":      "install",
		"arguments": map[string]any{"name": "git-mcp"},
	})
	res4, ok := m4["result"].(map[string]any)
	if !ok {
		t.Fatalf("install gate must be a tool result, got %v", m4)
	}
	if res4["isError"] != true {
		t.Fatalf("install without flag must be isError, got %v", res4)
	}
	if text := toolText(t, res4); !strings.Contains(text, "--allow-install") {
		t.Errorf("install-gate text %q must mention --allow-install", text)
	}
}

// codeOf returns the error code of an error-shaped response frame.
func codeOf(t *testing.T, m map[string]any) float64 {
	t.Helper()
	errObj, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("response has no error object: %v", m)
	}
	code, ok := errObj["code"].(float64)
	if !ok {
		t.Fatalf("error code = %v, not a number", errObj["code"])
	}
	return code
}

// ── 5. stdout protocol purity ───────────────────────────────────────────────

// TestServeStdoutProtocolPurity — a full session transcript: every stdout
// line must parse as strict JSON with jsonrpc "2.0" and the expected id
// (the purity law); stderr may carry anything. Covers the happy path,
// notifications, every error class, and raw protocol garbage. After the
// transcript, stdin EOF must terminate the server with no extra frames.
func TestServeStdoutProtocolPurity(t *testing.T) {
	srv := serveFixtureRegistry(t)
	_, env := serveHome(t, srv.URL)
	p := startServeHelper(t, env, t.TempDir(), false)

	// The full session: every frame is sent first (like a pipelining
	// client), then the transcript is drained in order.
	rawLines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"purity","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`, // acked by silence
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"query":"git"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"info","arguments":{"name":"git-mcp"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"list_installed"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"install","arguments":{"name":"git-mcp"}}}`, // gated → isError
		`{"jsonrpc":"2.0","id":8,"method":"resources/list","params":{}}`,                                            // -32601
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{}}`,                                                // -32602
		`hello, this is not json`, // -32700, id null
		`[1,2,3]`,                 // -32600, id null (batch unsupported)
		`{"jsonrpc":"1.0","id":10,"method":"ping"}`, // -32600, id echoed
	}
	// Expected stdout, in order: the id (raw JSON) of each response frame.
	// notifications/initialized produces no frame.
	wantIDs := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "null", "null", "10"}

	for _, line := range rawLines {
		p.sendLine(t, line)
	}

	var transcript []string
	for len(transcript) < len(wantIDs) {
		transcript = append(transcript, p.nextLine(t))
	}

	// stdin EOF must terminate the server with no extra stdout frames
	// (the documented shutdown path — no dangling output after exit).
	// readDone closes when stdout hits EOF: the helper has exited and all
	// of its output has been consumed. (p.lines is a data channel and is
	// never closed — waiting on it can never observe shutdown.)
	_ = p.stdin.Close()
	select {
	case <-p.readDone:
	case <-time.After(15 * time.Second):
		t.Fatalf("serve helper did not exit after stdin EOF (stderr: %q)", p.stderr.String())
	}
	// No new frames can arrive past readDone; anything still buffered in
	// p.lines is an extra frame after the transcript.
	drained := false
	for !drained {
		select {
		case line := <-p.lines:
			t.Fatalf("extra stdout frame after the transcript: %q", line)
		default:
			drained = true
		}
	}

	// Every line: strict JSON, jsonrpc 2.0, exact id.
	for i, line := range transcript {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("transcript line %d is not strict JSON: %q (%v)", i+1, line, err)
		}
		if m["jsonrpc"] != "2.0" {
			t.Errorf("transcript line %d jsonrpc = %v, want \"2.0\": %q", i+1, m["jsonrpc"], line)
		}
		// The id must be the raw JSON form the client sent (number) or the
		// protocol-assigned null — compare re-encoded JSON, not Go's %v.
		rawID, err := json.Marshal(m["id"])
		if err != nil {
			t.Fatalf("transcript line %d id does not encode: %q (%v)", i+1, line, err)
		}
		if string(rawID) != wantIDs[i] {
			t.Errorf("transcript line %d id = %s, want %s (line %q)", i+1, rawID, wantIDs[i], line)
		}
	}

	// Spot-check the interesting frames.
	var frame1, frame7, frame8, frame9, frame10 map[string]any
	for i, line := range transcript {
		var m map[string]any
		_ = json.Unmarshal([]byte(line), &m)
		switch wantIDs[i] {
		case "1":
			frame1 = m
		case "7":
			frame7 = m
		case "8":
			frame8 = m
		case "9":
			frame9 = m
		case "10":
			frame10 = m
		}
	}
	if res := frame1["result"].(map[string]any); res["protocolVersion"] != "2024-11-05" {
		t.Errorf("initialize protocolVersion = %v", res["protocolVersion"])
	}
	if res7, ok := frame7["result"].(map[string]any); !ok || res7["isError"] != true {
		t.Errorf("install-gate frame = %v, want isError result", frame7)
	} else if text := toolText(t, res7); !strings.Contains(text, "--allow-install") {
		t.Errorf("install-gate text %q must mention --allow-install", text)
	}
	if codeOf(t, frame8) != -32601 {
		t.Errorf("unknown-method frame = %v", frame8)
	}
	if codeOf(t, frame9) != -32602 {
		t.Errorf("bad-params frame = %v", frame9)
	}
	if codeOf(t, frame10) != -32600 {
		t.Errorf("wrong-jsonrpc frame = %v", frame10)
	}
}
