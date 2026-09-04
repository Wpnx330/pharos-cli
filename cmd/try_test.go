package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/canonical"
)

// TestMain doubles as the fake MCP server for `pharos try` tests: when
// PHAROS_TRY_HELPER is set, this test binary re-executes itself as a
// scripted stdio server (no network, no external processes, GOOS-agnostic).
func TestMain(m *testing.M) {
	if mode := os.Getenv("PHAROS_TRY_HELPER"); mode != "" {
		runTryHelper(mode, os.Stdout, os.Stdin)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runTryHelper answers the MCP handshake: mode "mcp" is a full happy
// server; mode "hang" answers initialize then goes silent (for --timeout).
func runTryHelper(mode string, out interface{ Write([]byte) (int, error) }, in interface{ Read([]byte) (int, error) }) {
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			respondTry(out, req.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"serverInfo":      map[string]string{"name": "helper-server", "version": "2.0.0"},
			})
		case "tools/list":
			if mode == "hang" {
				time.Sleep(30 * time.Second)
			}
			respondTry(out, req.ID, map[string]any{"tools": []map[string]string{
				{"name": "echo", "description": "Echo back the provided message"},
				{"name": "status", "description": "Return server status"},
			}})
		case "resources/list":
			if mode == "hang" {
				time.Sleep(30 * time.Second)
			}
			respondTry(out, req.ID, map[string]any{"resources": []map[string]string{
				{"name": "notes", "uri": "file:///notes.md"},
			}})
		case "prompts/list":
			if mode == "hang" {
				time.Sleep(30 * time.Second)
			}
			respondTry(out, req.ID, map[string]any{"prompts": []map[string]string{
				{"name": "greeting"},
			}})
		}
	}
}

func respondTry(out interface{ Write([]byte) (int, error) }, id json.RawMessage, result any) {
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	fmt.Fprintln(out, string(data))
}

// setupTryServer isolates HOME and registers a canonical stdio server
// named name whose command is this test binary in the given helper mode.
func setupTryServer(t *testing.T, name, mode string) {
	t.Helper()
	isolateHome(t)
	dir := filepath.Join(contractHome(t), ".pharos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// os.Executable is absolute on every GOOS (os.Args[0] can be a bare
	// name on Windows, which would break the spawn-time PATH lookup).
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	srv := canonical.Server{
		Transport: "stdio",
		Command:   exe,
		Env:       map[string]string{"PHAROS_TRY_HELPER": mode},
		Package:   canonical.PackageInfo{Name: name, Version: "1.0.0", Source: "pharos"},
		Enabled:   true,
	}
	cfg := canonical.Config{Servers: map[string]canonical.Server{name: srv}}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTryJSONPurity — `try --json` prints exactly one JSON document on
// stdout; progress goes to stderr. PHAROS_JSON=1 is byte-identical.
func TestTryJSONPurity(t *testing.T) {
	setupTryServer(t, "demo", "mcp")

	flagOut, _ := runContract(t, nil, "try", "demo", "--json")
	envOut, combined := runContract(t, map[string]string{"PHAROS_JSON": "1"}, "try", "demo")

	if flagOut != envOut {
		t.Errorf("--json and PHAROS_JSON=1 disagree:\nflag: %q\nenv:  %q", flagOut, envOut)
	}
	if !json.Valid([]byte(strings.TrimSpace(flagOut))) {
		t.Fatalf("stdout is not a single valid JSON document: %q", flagOut)
	}
	var doc struct {
		Server string `json:"server"`
		Caps   *struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"caps"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(flagOut)), &doc); err != nil {
		t.Fatalf("decode try JSON: %v", err)
	}
	if doc.Server != "demo" {
		t.Errorf("server = %q, want demo", doc.Server)
	}
	if doc.Caps == nil || doc.Caps.ServerInfo.Name != "helper-server" || doc.Caps.ServerInfo.Version != "2.0.0" {
		t.Errorf("caps.serverInfo = %+v, want helper-server 2.0.0", doc.Caps)
	}
	if doc.Caps == nil || len(doc.Caps.Tools) != 2 {
		t.Errorf("caps.tools = %+v, want 2 tools", doc.Caps)
	}
	if !strings.Contains(combined, "Probing") {
		t.Errorf("progress line missing from stderr in JSON mode: %q", combined)
	}
}

// TestTryUnknownServerExit2 — an unknown name is a usage error (exit 2)
// whose hint names `pharos install`.
func TestTryUnknownServerExit2(t *testing.T) {
	isolateHome(t)
	resetAllFlags()
	t.Cleanup(resetAllFlags)

	err := runTry([]string{"ghost"})
	if err == nil {
		t.Fatal("runTry(ghost): want error, got nil")
	}
	te, ok := err.(*tryError)
	if !ok {
		t.Fatalf("error %T is not *tryError", err)
	}
	if te.Code != 2 {
		t.Errorf("code = %d, want 2", te.Code)
	}
	if !strings.Contains(te.Message, "not found in ~/.pharos/mcp.json") {
		t.Errorf("message = %q, want canonical-config mention", te.Message)
	}
	if !strings.Contains(te.Hint, "pharos install ghost") {
		t.Errorf("hint = %q, want 'pharos install ghost'", te.Hint)
	}
}

// TestTrySuccessSummaryText — plain mode prints the probing progress, the
// server header, and the tools/resources/prompts summary.
func TestTrySuccessSummaryText(t *testing.T) {
	setupTryServer(t, "demo", "mcp")

	_, combined := runContract(t, nil, "try", "demo")
	for _, want := range []string{
		"Probing demo",
		"helper-server",
		"v2.0.0",
		"2025-06-18",
		"TOOLS (2)",
		"echo",
		"Echo back the provided message",
		"RESOURCES (1)",
		"notes",
		"PROMPTS (1)",
		"greeting",
	} {
		if !strings.Contains(combined, want) {
			t.Errorf("summary output missing %q:\n%s", want, combined)
		}
	}
}

// TestTryInspectWithoutNpx — with nothing on PATH, --inspect fails
// honestly with an npx install hint instead of a silent spawn failure.
func TestTryInspectWithoutNpx(t *testing.T) {
	setupTryServer(t, "demo", "mcp")
	t.Setenv("PATH", "")

	_, combined := runContract(t, nil, "try", "demo", "--inspect")
	if !strings.Contains(combined, "npx not found in $PATH") {
		t.Errorf("output missing honest npx error:\n%s", combined)
	}
}

// TestTryTimeoutOverride — --timeout shortens the probe budget so a
// hanging server fails fast naming the request stage. Usually tools/list,
// but under CI load the initialize round trip itself can overrun the
// short budget, so either stage is accepted.
func TestTryTimeoutOverride(t *testing.T) {
	setupTryServer(t, "slow", "hang")

	_, combined := runContract(t, nil, "try", "slow", "--timeout", "200ms")
	// The 200ms budget usually expires via ctx (context deadline
	// exceeded); a per-request overrun instead reads "no response within".
	// Either timeout signature is a pass; the stage must be named.
	timeoutMention := strings.Contains(combined, "context deadline exceeded") ||
		strings.Contains(combined, "no response within")
	if !timeoutMention {
		t.Errorf("timeout failure missing timeout mention:\n%s", combined)
	}
	if !strings.Contains(combined, "tools/list") && !strings.Contains(combined, "initialize") {
		t.Errorf("timeout failure does not name a request stage:\n%s", combined)
	}
}

// TestTryInspectJSONReportsCommand — --inspect --json reports the exact
// npx command in the document without spawning an interactive process.
func TestTryInspectJSONReportsCommand(t *testing.T) {
	setupTryServer(t, "demo", "mcp")

	stdout, _ := runContract(t, nil, "try", "demo", "--inspect", "--json")
	var doc struct {
		Server      string `json:"server"`
		InspectCmd  string `json:"inspect_command"`
		InspectCmd2 string `json:"inspectCmd"`
	}
	trimmed := strings.TrimSpace(stdout)
	if !json.Valid([]byte(trimmed)) {
		t.Fatalf("stdout is not valid JSON: %q", stdout)
	}
	if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
		t.Fatalf("decode try inspect JSON: %v", err)
	}
	cmd := doc.InspectCmd
	if cmd == "" {
		cmd = doc.InspectCmd2
	}
	if !strings.HasPrefix(cmd, "npx -y @modelcontextprotocol/inspector") {
		t.Errorf("inspect_command = %q, want npx inspector prefix", cmd)
	}
}

// TestTryPreFlightJSONDocs — the pre-flight failure paths (unknown server
// → exit 2, non-stdio → exit 1) emit the minimal {server, errors} document
// on stdout in JSON mode instead of leaving it empty.
func TestTryPreFlightJSONDocs(t *testing.T) {
	isolateHome(t)
	resetAllFlags()
	t.Cleanup(resetAllFlags)
	t.Setenv("PHAROS_JSON", "1")

	stdout := captureTryStdout(t, func() {
		err := runTry([]string{"ghost"})
		te, ok := err.(*tryError)
		if !ok || te.Code != 2 {
			t.Fatalf("runTry(ghost) = %v, want exit-2 tryError", err)
		}
	})
	assertTryJSONDoc(t, stdout, "ghost", "not found in ~/.pharos/mcp.json")

	dir := filepath.Join(contractHome(t), ".pharos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]canonical.Server{
		"remote": {
			Transport: "http-sse",
			URL:       "http://127.0.0.1:9999/sse",
			Package:   canonical.PackageInfo{Name: "remote", Version: "1.0.0", Source: "pharos"},
			Enabled:   true,
		},
	}
	data, _ := json.Marshal(canonical.Config{Servers: cfg})
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	stdout = captureTryStdout(t, func() {
		err := runTry([]string{"remote"})
		te, ok := err.(*tryError)
		if !ok || te.Code != 1 {
			t.Fatalf("runTry(remote) = %v, want exit-1 tryError", err)
		}
	})
	assertTryJSONDoc(t, stdout, "remote", "not a stdio server")
}

// captureTryStdout runs fn with os.Stdout redirected to a pipe and returns
// what it printed (try's JSON paths write via fmt.Println on os.Stdout).
func captureTryStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = orig
	_ = w.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// assertTryJSONDoc checks a pre-flight failure document: valid JSON, the
// server named, exactly one error carrying the expected fragment.
func assertTryJSONDoc(t *testing.T, stdout, server, errContains string) {
	t.Helper()
	trimmed := strings.TrimSpace(stdout)
	if !json.Valid([]byte(trimmed)) {
		t.Fatalf("stdout is not valid JSON: %q", stdout)
	}
	var doc struct {
		Server string   `json:"server"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
		t.Fatalf("decode pre-flight JSON: %v", err)
	}
	if doc.Server != server {
		t.Errorf("server = %q, want %q", doc.Server, server)
	}
	if len(doc.Errors) != 1 || !strings.Contains(doc.Errors[0], errContains) {
		t.Errorf("errors = %v, want one entry containing %q", doc.Errors, errContains)
	}
}

// TestTryNonStdioServer — remote (non-stdio) servers are refused with an
// honest message instead of a bogus spawn attempt.
func TestTryNonStdioServer(t *testing.T) {
	isolateHome(t)
	dir := filepath.Join(contractHome(t), ".pharos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]canonical.Server{
		"remote": {
			Transport: "http-sse",
			URL:       "http://127.0.0.1:9999/sse",
			Package:   canonical.PackageInfo{Name: "remote", Version: "1.0.0", Source: "pharos"},
			Enabled:   true,
		},
	}
	data, _ := json.Marshal(canonical.Config{Servers: cfg})
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	resetAllFlags()
	t.Cleanup(resetAllFlags)

	err := runTry([]string{"remote"})
	if err == nil {
		t.Fatal("runTry(remote): want error, got nil")
	}
	te, ok := err.(*tryError)
	if !ok {
		t.Fatalf("error %T is not *tryError", err)
	}
	if te.Code != 1 {
		t.Errorf("code = %d, want 1", te.Code)
	}
	if !strings.Contains(te.Message, "not a stdio server") {
		t.Errorf("message = %q, want non-stdio refusal", te.Message)
	}
}
