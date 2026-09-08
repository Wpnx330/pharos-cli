package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/api"
)

// ── in-process harness ──────────────────────────────────────────────────────

// isolateHomeDir points HOME/USERPROFILE at a fresh temp dir so lockfile
// lookups never touch the real home (same pattern as cmd's isolateHome).
func isolateHomeDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	return home
}

// runSession feeds input (newline-delimited frames) to a fresh server and
// returns everything the server wrote to stdout/stderr.
func runSession(t *testing.T, cfg Config, input string) (out, errOut string) {
	t.Helper()
	outBuf, errBuf := &bytes.Buffer{}, &bytes.Buffer{}
	New(cfg).Run(context.Background(), strings.NewReader(input), outBuf, errBuf)
	return outBuf.String(), errBuf.String()
}

// frame builds one JSON-RPC request line (id kept as given: nil → null).
func frame(id any, method string, params any) string {
	return `{"jsonrpc":"2.0","id":` + mustJSON(id) + `,"method":` + mustJSON(method) +
		`,"params":` + mustJSON(params) + "}"
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// rawFrame wraps an arbitrary stdin line as a full session input.
func rawFrame(line string) string { return line + "\n" }

// parseLines splits server output into per-line generic maps, failing on
// any line that is not strict JSON (the in-process purity check).
func parseLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	var frames []map[string]any
	if strings.TrimSpace(out) == "" {
		return frames
	}
	for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("stdout line %d is not strict JSON: %q (%v)", i+1, line, err)
		}
		if m["jsonrpc"] != "2.0" {
			t.Fatalf("stdout line %d is not a JSON-RPC 2.0 frame: %q", i+1, line)
		}
		frames = append(frames, m)
	}
	return frames
}

// firstFrameFor finds the response frame with the given id.
func firstFrameFor(t *testing.T, frames []map[string]any, id any) map[string]any {
	t.Helper()
	want := fmt.Sprint(id)
	for _, f := range frames {
		if got, ok := f["id"]; ok && fmt.Sprint(got) == want {
			return f
		}
	}
	t.Fatalf("no response frame with id %v in %d frames", id, len(frames))
	return nil
}

// callToolViaFrames drives tools/call end-to-end and returns the decoded
// text payload (the JSON string inside content[0].text) plus the result
// envelope for isError checks.
func callToolViaFrames(t *testing.T, cfg Config, name string, args map[string]any) (payload string, res map[string]any) {
	t.Helper()
	input := frame(1, "tools/call", map[string]any{"name": name, "arguments": args}) + "\n"
	out, _ := runSession(t, cfg, input)
	f := firstFrameFor(t, parseLines(t, out), 1)
	res, ok := f["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/call %s: no result object: %v", name, f)
	}
	content, ok := res["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("tools/call %s: no content: %v", name, res)
	}
	c, ok := content[0].(map[string]any)
	if !ok || c["type"] != "text" {
		t.Fatalf("tools/call %s: content[0] = %v, want text block", name, content[0])
	}
	text, _ := c["text"].(string)
	return text, res
}

// fixtureRegistry stands in for the PHAROS registry (search + package
// detail + one 404 name). All traffic stays on localhost.
func fixtureRegistry(t *testing.T) *httptest.Server {
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
				`{"name":"git-mcp","version":"1.2.0","title":"Git MCP","description":"Git tools","score":5,"downloads30d":1234,"transport":["stdio"],"source_registry":"pharos","publisher":{"namespace":"acme"},"scorecard":{"score":87,"grade":"A"}},`+
				`{"name":"fs-mcp","version":"0.9.1","description":"Filesystem tools","downloads30d":10,"transport":["stdio","http-sse"]}`+
				`],"boosted":[],"nextCursor":"","total":2}`)
		case strings.HasPrefix(r.URL.Path, "/v1/packages/missing-pkg"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"package not found"}}`)
		case strings.HasPrefix(r.URL.Path, "/v1/packages/"):
			_, _ = io.WriteString(w, `{
				"name":"git-mcp","title":"Git MCP","description":"Git tools","license":"MIT",
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

// testConfig wires a Config against the fixture registry with HOME isolated.
func testConfig(t *testing.T, mutate func(*Config)) Config {
	t.Helper()
	isolateHomeDir(t)
	cfg := Config{
		Version: "9.9.9-test",
		Client:  api.New(fixtureRegistry(t).URL, ""),
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return cfg
}

// ── initialize handshake ────────────────────────────────────────────────────

func TestInitializeHandshake(t *testing.T) {
	cfg := testConfig(t, nil)
	input := frame(1, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "unit-test", "version": "0.0.0"},
	}) + "\n" + `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	out, _ := runSession(t, cfg, input)

	frames := parseLines(t, out)
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1 (initialized notification must stay silent)", len(frames))
	}
	f := frames[0]
	if f["id"].(float64) != 1 {
		t.Errorf("id = %v, want 1 (numeric echo)", f["id"])
	}
	res, ok := f["result"].(map[string]any)
	if !ok {
		t.Fatalf("result = %v, want object", f["result"])
	}
	if res["protocolVersion"] != ProtocolVersion {
		t.Errorf("protocolVersion = %v, want %s", res["protocolVersion"], ProtocolVersion)
	}
	si, ok := res["serverInfo"].(map[string]any)
	if !ok || si["name"] != "pharos" || si["version"] != cfg.Version {
		t.Errorf("serverInfo = %v, want {pharos %s}", res["serverInfo"], cfg.Version)
	}
	caps, ok := res["capabilities"].(map[string]any)
	if !ok || caps["tools"] == nil {
		t.Errorf("capabilities = %v, want tools entry", res["capabilities"])
	}
}

func TestInitializeStringIDEcho(t *testing.T) {
	cfg := testConfig(t, nil)
	out, _ := runSession(t, cfg, frame("abc-1", "initialize", map[string]any{})+"\n")
	frames := parseLines(t, out)
	f := firstFrameFor(t, frames, "abc-1")
	// String ids must come back as strings, not reinterpreted.
	res := f["result"].(map[string]any)
	if res["protocolVersion"] != ProtocolVersion {
		t.Errorf("protocolVersion = %v", res["protocolVersion"])
	}
}

func TestNotificationSilence(t *testing.T) {
	cfg := testConfig(t, nil)
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`,
		`{"jsonrpc":"2.0","method":"some/unknown/notification"}`,
		`{"jsonrpc":"2.0","method":"some/unknown/request-without-id"}`,
		`{"jsonrpc":"2.0","id":null,"method":"ping"}`,
		"", // blank line must be skipped silently
	}, "\n")
	out, _ := runSession(t, cfg, input)
	if strings.TrimSpace(out) != "" {
		t.Errorf("notifications and null-id requests must not be answered, got %q", out)
	}
}

func TestResponseShapedFrameIgnored(t *testing.T) {
	cfg := testConfig(t, nil)
	out, _ := runSession(t, cfg, rawFrame(`{"jsonrpc":"2.0","id":7,"result":{"echo":true}}`))
	if strings.TrimSpace(out) != "" {
		t.Errorf("response-shaped frames must be ignored, got %q", out)
	}
}

// ── protocol errors ─────────────────────────────────────────────────────────

func TestProtocolErrors(t *testing.T) {
	cfg := testConfig(t, nil)
	cases := []struct {
		name     string
		line     string
		wantID   string // raw JSON of the echoed id
		wantCode float64
	}{
		{"unknown method", frame(2, "resources/list", map[string]any{}), "2", -32601},
		{"garbage line", "this is not json", "null", -32700},
		{"json scalar", "42", "null", -32600},
		{"json array batch unsupported", "[1,2,3]", "null", -32600},
		{"wrong jsonrpc version", `{"jsonrpc":"1.0","id":8,"method":"ping"}`, "8", -32600},
		{"no method no response fields", `{"jsonrpc":"2.0","id":9}`, "9", -32600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := runSession(t, cfg, tc.line+"\n")
			frames := parseLines(t, out)
			if len(frames) != 1 {
				t.Fatalf("frames = %d, want 1", len(frames))
			}
			f := frames[0]
			errObj, ok := f["error"].(map[string]any)
			if !ok {
				t.Fatalf("frame has no error object: %v", f)
			}
			if errObj["code"] != tc.wantCode {
				t.Errorf("code = %v, want %v", errObj["code"], tc.wantCode)
			}
			if msg, _ := errObj["message"].(string); msg == "" {
				t.Error("error message is empty")
			}
			if mustJSON(f["id"]) != tc.wantID {
				t.Errorf("id = %s, want %s", mustJSON(f["id"]), tc.wantID)
			}
		})
	}
}

func TestToolsCallBadParams(t *testing.T) {
	cfg := testConfig(t, nil)
	cases := []struct {
		name  string
		frame string
	}{
		{"missing tool name", frame(1, "tools/call", map[string]any{})},
		{"null params", frame(2, "tools/call", nil)},
		{"non-object arguments", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":"nope"}}`},
		{"unknown tool", frame(4, "tools/call", map[string]any{"name": "nope", "arguments": map[string]any{}})},
		{"wrong argument type", `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"search","arguments":{"query":123}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := runSession(t, cfg, tc.frame+"\n")
			frames := parseLines(t, out)
			if len(frames) != 1 {
				t.Fatalf("frames = %d, want 1", len(frames))
			}
			errObj, ok := frames[0]["error"].(map[string]any)
			if !ok || errObj["code"] != float64(codeInvalidParams) {
				t.Fatalf("frame = %v, want -32602 error", frames[0])
			}
		})
	}
}

// ── tools/list ──────────────────────────────────────────────────────────────

func TestToolsListDefaultThree(t *testing.T) {
	cfg := testConfig(t, nil)
	out, _ := runSession(t, cfg, frame(1, "tools/list", map[string]any{})+"\n")
	frames := parseLines(t, out)
	res := firstFrameFor(t, frames, 1)["result"].(map[string]any)
	tools, ok := res["tools"].([]any)
	if !ok {
		t.Fatalf("tools = %v", res["tools"])
	}
	names := map[string]bool{}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		names[tool["name"].(string)] = true
		if tool["description"] == "" {
			t.Errorf("tool %v has empty description", tool["name"])
		}
		schema, ok := tool["inputSchema"].(map[string]any)
		if !ok {
			t.Fatalf("tool %v inputSchema = %v, want object", tool["name"], tool["inputSchema"])
		}
		if schema["type"] != "object" {
			t.Errorf("tool %v schema type = %v, want object", tool["name"], schema["type"])
		}
		if _, ok := schema["properties"].(map[string]any); !ok {
			t.Errorf("tool %v schema properties = %v, want object", tool["name"], schema["properties"])
		}
	}
	for _, want := range []string{"search", "info", "list_installed"} {
		if !names[want] {
			t.Errorf("tools missing %q (got %v)", want, names)
		}
	}
	if names["install"] {
		t.Error("install tool must be omitted without --allow-install")
	}
}

func TestToolsListWithAllowInstall(t *testing.T) {
	cfg := testConfig(t, func(c *Config) { c.AllowInstall = true })
	out, _ := runSession(t, cfg, frame(1, "tools/list", map[string]any{})+"\n")
	frames := parseLines(t, out)
	res := firstFrameFor(t, frames, 1)["result"].(map[string]any)
	tools := res["tools"].([]any)
	if len(tools) != 4 {
		t.Fatalf("tools = %d, want 4 with AllowInstall", len(tools))
	}
	var installSchema map[string]any
	for _, raw := range tools {
		tool := raw.(map[string]any)
		if tool["name"] == "install" {
			installSchema = tool["inputSchema"].(map[string]any)
		}
	}
	if installSchema == nil {
		t.Fatal("install tool missing from tools/list")
	}
	required, ok := installSchema["required"].([]any)
	if !ok || len(required) != 1 || required[0] != "name" {
		t.Errorf("install schema required = %v, want [name]", installSchema["required"])
	}
}

// ── tools/call: search / info / list_installed ──────────────────────────────

func TestToolSearchHappyPath(t *testing.T) {
	cfg := testConfig(t, nil)
	text, res := callToolViaFrames(t, cfg, "search", map[string]any{"query": "git", "limit": 5})
	if res["isError"] == true {
		t.Fatalf("search returned isError: %s", text)
	}
	var hits []map[string]any
	if err := json.Unmarshal([]byte(text), &hits); err != nil {
		t.Fatalf("search payload is not a JSON array: %q (%v)", text, err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(hits))
	}
	first := hits[0]
	if first["name"] != "git-mcp" || first["version"] != "1.2.0" {
		t.Errorf("first hit = %v", first)
	}
	if first["downloads"] != float64(1234) {
		t.Errorf("downloads = %v, want 1234", first["downloads"])
	}
	transports, ok := first["transports"].([]any)
	if !ok || len(transports) != 1 || transports[0] != "stdio" {
		t.Errorf("transports = %v, want [stdio]", first["transports"])
	}
	if first["grade"] != "A" {
		t.Errorf("grade = %v, want A (scorecard present)", first["grade"])
	}
	second := hits[1]
	if _, has := second["grade"]; has {
		t.Errorf("unscored hit must omit grade, got %v", second["grade"])
	}
}

func TestToolSearchLimitEdgeCases(t *testing.T) {
	cfg := testConfig(t, nil)
	// limit 0/negative → default; over max → capped; both must not error.
	for _, limit := range []int{0, -3, 500} {
		text, res := callToolViaFrames(t, cfg, "search", map[string]any{"query": "git", "limit": limit})
		if res["isError"] == true {
			t.Fatalf("limit %d: isError: %s", limit, text)
		}
	}
	_, res := callToolViaFrames(t, cfg, "search", map[string]any{"query": "nothing"})
	if res["isError"] == true {
		t.Fatal("empty result set must not be an error")
	}
}

func TestToolSearchEmptyQueryBadParams(t *testing.T) {
	cfg := testConfig(t, nil)
	input := frame(1, "tools/call", map[string]any{"name": "search", "arguments": map[string]any{"query": "  "}}) + "\n"
	out, _ := runSession(t, cfg, input)
	f := firstFrameFor(t, parseLines(t, out), 1)
	errObj, ok := f["error"].(map[string]any)
	if !ok || errObj["code"] != float64(codeInvalidParams) {
		t.Fatalf("empty query must be -32602, got %v", f)
	}
}

func TestToolInfoHappyPath(t *testing.T) {
	cfg := testConfig(t, nil)
	text, res := callToolViaFrames(t, cfg, "info", map[string]any{"name": "git-mcp"})
	if res["isError"] == true {
		t.Fatalf("info returned isError: %s", text)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("info payload is not a JSON object: %q (%v)", text, err)
	}
	if doc["name"] != "git-mcp" || doc["latest"] != "1.2.0" {
		t.Errorf("doc = %v", doc)
	}
	if hint, _ := doc["install_hint"].(string); !strings.Contains(hint, "pharos install git-mcp@1.2.0") {
		t.Errorf("install_hint = %q, want pharos install git-mcp@1.2.0", hint)
	}
	versions, ok := doc["versions"].([]any)
	if !ok || len(versions) != 1 || versions[0] != "1.2.0" {
		t.Errorf("versions = %v", doc["versions"])
	}
	if doc["transport"] != "stdio" {
		t.Errorf("transport = %v, want stdio", doc["transport"])
	}
	sc, ok := doc["scorecard"].(map[string]any)
	if !ok || sc["grade"] != "A" || sc["score"] != float64(87) {
		t.Errorf("scorecard = %v", doc["scorecard"])
	}
}

func TestToolInfoNotFoundIsError(t *testing.T) {
	cfg := testConfig(t, nil)
	text, res := callToolViaFrames(t, cfg, "info", map[string]any{"name": "missing-pkg"})
	if res["isError"] != true {
		t.Fatalf("404 lookup must be an isError result, got %v", res)
	}
	if !strings.Contains(text, "missing-pkg") {
		t.Errorf("error text %q must name the package", text)
	}
}

func TestToolListInstalled(t *testing.T) {
	isolateHomeDir(t)
	cwd := t.TempDir()
	t.Chdir(cwd) // lockfile.DefaultPath prefers ./pharos.lock in a writable cwd
	lock := `{"version":1,"servers":{` +
		`"seeded-srv":{"version":"2.0.0","integrity":"sha512-x","transport":"stdio","resolved":"https://x/t.tgz","installedAt":"2026-09-08T00:00:00Z","clients":["cursor"],"origin":{"kind":"registry","ref":"seeded-srv@2.0.0","installed_via":"pharos install"},"pinnedAt":"2.0.0"},` +
		`"legacy-srv":{"version":"1.0.0","integrity":"","transport":"stdio","resolved":"","installedAt":"2025-01-01T00:00:00Z"}` +
		`}}`
	if err := os.WriteFile(filepath.Join(cwd, "pharos.lock"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Version: "t"}
	text, res := callToolViaFrames(t, cfg, "list_installed", nil)
	if res["isError"] == true {
		t.Fatalf("list_installed returned isError: %s", text)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(text), &entries); err != nil {
		t.Fatalf("payload is not a JSON array: %q (%v)", text, err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	// Sorted by name: legacy first.
	if entries[0]["name"] != "legacy-srv" || entries[1]["name"] != "seeded-srv" {
		t.Fatalf("order = %v/%v, want legacy-srv/seeded-srv", entries[0]["name"], entries[1]["name"])
	}
	legacy := entries[0]
	if legacy["origin"] != "registry" {
		t.Errorf("legacy origin = %v, want registry (W5.1 backfill rule)", legacy["origin"])
	}
	if legacy["pinned"] != false || legacy["version"] != "1.0.0" {
		t.Errorf("legacy entry = %v", legacy)
	}
	clients, ok := legacy["clients"].([]any)
	if !ok || len(clients) != 0 {
		t.Errorf("legacy clients = %v, want [] (never null)", legacy["clients"])
	}
	seeded := entries[1]
	if seeded["pinned"] != true {
		t.Errorf("seeded pinned = %v, want true", seeded["pinned"])
	}
	if seeded["origin"] != "registry" {
		t.Errorf("seeded origin = %v, want registry", seeded["origin"])
	}
	seededClients, _ := seeded["clients"].([]any)
	if len(seededClients) != 1 || seededClients[0] != "cursor" {
		t.Errorf("seeded clients = %v, want [cursor]", seededClients)
	}
}

func TestToolListInstalledEmpty(t *testing.T) {
	isolateHomeDir(t)
	t.Chdir(t.TempDir()) // no pharos.lock anywhere in scope
	cfg := Config{Version: "t"}
	text, res := callToolViaFrames(t, cfg, "list_installed", nil)
	if res["isError"] == true {
		t.Fatalf("empty lockfile must not error: %s", text)
	}
	if strings.TrimSpace(text) != "[]" {
		t.Errorf("payload = %q, want []", text)
	}
}

// ── install gate ────────────────────────────────────────────────────────────

func TestInstallDisabledIsErrorNamingFlag(t *testing.T) {
	cfg := testConfig(t, nil) // AllowInstall false
	text, res := callToolViaFrames(t, cfg, "install", map[string]any{"name": "git-mcp"})
	if res["isError"] != true {
		t.Fatalf("install without flag must be isError, got %v", res)
	}
	if !strings.Contains(text, "--allow-install") {
		t.Errorf("error text %q must mention --allow-install", text)
	}
}

// ── error surfaces ──────────────────────────────────────────────────────────

func TestRegistryFailureIsErrorResult(t *testing.T) {
	isolateHomeDir(t)
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"code":"boom","message":"registry exploded"}}`)
	}))
	t.Cleanup(dead.Close)
	cfg := Config{Version: "t", Client: api.New(dead.URL, "")}
	text, res := callToolViaFrames(t, cfg, "search", map[string]any{"query": "git"})
	if res["isError"] != true {
		t.Fatalf("registry failure must be isError, got %v (%s)", res, text)
	}
	if !strings.Contains(text, "registry") {
		t.Errorf("error text %q should mention the registry failure", text)
	}
}

func TestToolPanicRecovered(t *testing.T) {
	cfg := testConfig(t, nil)
	s := New(cfg)
	s.byName["boom"] = toolDef{
		spec: Tool{Name: "boom", InputSchema: json.RawMessage(listInstalledSchema)},
		run: func(*Server, json.RawMessage) (any, error) {
			panic("kaboom")
		},
	}
	outBuf := &bytes.Buffer{}
	s.Run(context.Background(),
		strings.NewReader(frame(1, "tools/call", map[string]any{"name": "boom"})+"\n"),
		outBuf, &bytes.Buffer{})
	frames := parseLines(t, outBuf.String())
	res := firstFrameFor(t, frames, 1)["result"].(map[string]any)
	if res["isError"] != true {
		t.Fatalf("panic must become isError result, got %v", res)
	}
	content := res["content"].([]any)[0].(map[string]any)
	if !strings.Contains(content["text"].(string), "internal error") {
		t.Errorf("text = %q, want internal-error mention", content["text"])
	}
}

func TestPingEmptyResult(t *testing.T) {
	cfg := testConfig(t, nil)
	out, _ := runSession(t, cfg, frame(1, "ping", nil)+"\n")
	frames := parseLines(t, out)
	f := firstFrameFor(t, frames, 1)
	res, ok := f["result"].(map[string]any)
	if !ok || len(res) != 0 {
		t.Errorf("ping result = %v, want empty object", f["result"])
	}
}

// ── stdout purity (in-process, full mixed session) ──────────────────────────

func TestStdoutPurityMixedSession(t *testing.T) {
	cfg := testConfig(t, nil)
	input := strings.Join([]string{
		frame(1, "initialize", map[string]any{}),
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		frame(2, "tools/list", map[string]any{}),
		frame(3, "tools/call", map[string]any{"name": "search", "arguments": map[string]any{"query": "git"}}),
		frame(4, "tools/call", map[string]any{"name": "info", "arguments": map[string]any{"name": "git-mcp"}}),
		frame(5, "tools/call", map[string]any{"name": "list_installed"}),
		frame(6, "ping", nil),
		frame(7, "resources/list", map[string]any{}), // -32601
		frame(8, "tools/call", map[string]any{}),     // -32602
		"totally not json",                           // -32700
		"[1,2,3]",                                    // -32600
	}, "\n") + "\n"
	out, _ := runSession(t, cfg, input)
	frames := parseLines(t, out) // parses strictly + asserts jsonrpc 2.0 on every line
	ids := map[string]bool{}
	for _, f := range frames {
		ids[mustJSON(f["id"])] = true
	}
	for _, want := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "null"} {
		if !ids[want] {
			t.Errorf("missing response for id %s (got ids %v)", want, ids)
		}
	}
	// 8 id'd requests + garbage (-32700) + array (-32600), both id null.
	if len(frames) != 10 {
		t.Errorf("frames = %d, want 10 (one per answered request, zero extra)", len(frames))
	}
}

// TestContextCancellationStopsRun — a canceled ctx makes Run return even
// though the reader never delivers data (serveLoop winds down in the
// background; callers close stdin to release it).
func TestContextCancellationStopsRun(t *testing.T) {
	cfg := testConfig(t, nil)
	s := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx, blockingReader{}, &bytes.Buffer{}, &bytes.Buffer{})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run after cancel = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after ctx cancellation")
	}
}

// blockingReader blocks forever, simulating a stdin that never delivers
// data (leaked at test end by design).
type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) {
	select {}
}
