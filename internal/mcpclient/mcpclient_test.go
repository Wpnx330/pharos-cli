package mcpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMain doubles as the fake MCP server: when PHAROS_MCP_HELPER is set in
// the environment, this binary re-executes itself as a scripted stdio
// server (the classic helper-process pattern — works on every GOOS, no
// network, no external processes).
func TestMain(m *testing.M) {
	if mode := os.Getenv("PHAROS_MCP_HELPER"); mode != "" {
		runHelper(mode, os.Stdout, os.Stdin)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runHelper answers JSON-RPC requests according to mode:
//
//	happy      — notification noise, then a full valid handshake (2 tools,
//	             2 resources — one without a name, 1 prompt)
//	notfound   — tools ok; resources/list + prompts/list answer -32601
//	toolserror — tools/list answers -32000 (a real error must surface)
//	hang       — initialize ok, then never answers tools/list
//	malformed  — stdout banner noise + a non-object initialize result
//	exitsearly — writes nothing, logs to stderr, exits 1
//	bigtools   — 200 tools
func runHelper(mode string, out io.Writer, in io.Reader) {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
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
			switch mode {
			case "exitsearly":
				fmt.Fprintln(os.Stderr, "boom: bad config")
				os.Exit(1)
			case "malformed":
				fmt.Fprintln(out, "IGNORE — server banner noise on stdout")
				fmt.Fprintln(out, `{"jsonrpc":"2.0","id":1,"result":"oops"}`)
				return
			}
			if mode == "happy" {
				fmt.Fprintln(out, `{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info"}}`)
			}
			respond(out, req.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"serverInfo":      map[string]string{"name": "test-server", "version": "1.2.3"},
			})
		case "tools/list":
			switch mode {
			case "hang":
				time.Sleep(30 * time.Second)
			case "toolserror":
				respondErr(out, req.ID, -32000, "tools exploded")
			case "bigtools":
				tools := make([]map[string]string, 0, 200)
				for i := 0; i < 200; i++ {
					tools = append(tools, map[string]string{
						"name":        fmt.Sprintf("tool_%03d", i),
						"description": "generated tool",
					})
				}
				respond(out, req.ID, map[string]any{"tools": tools})
			default:
				respond(out, req.ID, map[string]any{"tools": []map[string]string{
					{"name": "echo", "description": "Echo back the provided message"},
					{"name": "status", "description": "Return server status"},
				}})
			}
		case "resources/list":
			if mode == "notfound" {
				respondErr(out, req.ID, -32601, "method not found")
				continue
			}
			respond(out, req.ID, map[string]any{"resources": []map[string]string{
				{"name": "notes", "uri": "file:///notes.md"},
				{"uri": "file:///config.json"}, // no name — URI fallback
			}})
		case "prompts/list":
			if mode == "notfound" {
				respondErr(out, req.ID, -32601, "method not found")
				continue
			}
			respond(out, req.ID, map[string]any{"prompts": []map[string]string{
				{"name": "greeting"},
			}})
		}
	}
}

func respond(out io.Writer, id json.RawMessage, result any) {
	data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	fmt.Fprintln(out, string(data))
}

func respondErr(out io.Writer, id json.RawMessage, code int, msg string) {
	data, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": msg},
	})
	fmt.Fprintln(out, string(data))
}

// probeHelper probes this test binary re-executed in the given helper mode.
// reqTimeout > 0 temporarily shortens the per-request timeout.
func probeHelper(t *testing.T, mode string, timeout, reqTimeout time.Duration) (*Caps, error) {
	t.Helper()
	if reqTimeout > 0 {
		old := perRequestTimeout
		perRequestTimeout = reqTimeout
		t.Cleanup(func() { perRequestTimeout = old })
	}
	// os.Executable is absolute on every GOOS (os.Args[0] can be a bare
	// name on Windows, which would break the PATH lookup in Probe).
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := []string{exe}
	env := map[string]string{"PHAROS_MCP_HELPER": mode}
	return Probe(context.Background(), cmd, env, "", timeout)
}

// TestProbeHappyPath — full initialize handshake plus tools/list parsing,
// tolerating a server notification interleaved before the first response.
func TestProbeHappyPath(t *testing.T) {
	caps, err := probeHelper(t, "happy", 0, 0)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if caps.ProtocolVersion != "2025-06-18" {
		t.Errorf("protocolVersion = %q, want 2025-06-18", caps.ProtocolVersion)
	}
	if caps.ServerInfo.Name != "test-server" || caps.ServerInfo.Version != "1.2.3" {
		t.Errorf("serverInfo = %+v, want test-server 1.2.3", caps.ServerInfo)
	}
	if len(caps.Tools) != 2 {
		t.Fatalf("tools = %d entries, want 2", len(caps.Tools))
	}
	if caps.Tools[0].Name != "echo" || !strings.Contains(caps.Tools[0].Description, "Echo back") {
		t.Errorf("tools[0] = %+v, want echo with description", caps.Tools[0])
	}
	if caps.Tools[1].Name != "status" {
		t.Errorf("tools[1].name = %q, want status", caps.Tools[1].Name)
	}
}

// TestProbeResourceAndPromptNames — resource names are collected (URI
// fallback when the server sends no name) as are prompt names.
func TestProbeResourceAndPromptNames(t *testing.T) {
	caps, err := probeHelper(t, "happy", 0, 0)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	wantResources := []string{"notes", "file:///config.json"}
	if len(caps.Resources) != len(wantResources) {
		t.Fatalf("resources = %v, want %v", caps.Resources, wantResources)
	}
	for i, want := range wantResources {
		if caps.Resources[i] != want {
			t.Errorf("resources[%d] = %q, want %q", i, caps.Resources[i], want)
		}
	}
	if len(caps.Prompts) != 1 || caps.Prompts[0] != "greeting" {
		t.Errorf("prompts = %v, want [greeting]", caps.Prompts)
	}
}

// TestProbeMethodNotFoundTolerated — resources/list and prompts/list
// answering -32601 count as "capability absent", not a probe failure.
func TestProbeMethodNotFoundTolerated(t *testing.T) {
	caps, err := probeHelper(t, "notfound", 0, 0)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(caps.Tools) != 2 {
		t.Errorf("tools = %d, want 2 (tools/list still parsed)", len(caps.Tools))
	}
	if len(caps.Resources) != 0 {
		t.Errorf("resources = %v, want empty", caps.Resources)
	}
	if len(caps.Prompts) != 0 {
		t.Errorf("prompts = %v, want empty", caps.Prompts)
	}
}

// TestProbeTimeout — a server that never answers tools/list fails the
// per-request timeout (shortened so the test stays fast).
func TestProbeTimeout(t *testing.T) {
	caps, err := probeHelper(t, "hang", 0, 250*time.Millisecond)
	if err == nil {
		t.Fatal("Probe: want timeout error, got nil")
	}
	if caps != nil {
		t.Errorf("caps = %+v, want nil on failure", caps)
	}
	if !strings.Contains(err.Error(), "tools/list") || !strings.Contains(err.Error(), "no response within") {
		t.Errorf("error = %q, want tools/list timeout mention", err)
	}
}

// TestProbeMalformedResponse — stdout banner noise is skipped, but a
// non-object initialize result surfaces as a parse error.
func TestProbeMalformedResponse(t *testing.T) {
	_, err := probeHelper(t, "malformed", 0, 0)
	if err == nil {
		t.Fatal("Probe: want parse error, got nil")
	}
	if !strings.Contains(err.Error(), "parse result") {
		t.Errorf("error = %q, want parse-result failure", err)
	}
}

// TestProbeServerExitsEarly — an early crash surfaces the wait error and
// the captured stderr tail.
func TestProbeServerExitsEarly(t *testing.T) {
	_, err := probeHelper(t, "exitsearly", 0, 0)
	if err == nil {
		t.Fatal("Probe: want error, got nil")
	}
	if !strings.Contains(err.Error(), "server exited before responding") {
		t.Errorf("error = %q, want exited-before-responding", err)
	}
	var pe *ProbeError
	if !errors.As(err, &pe) {
		t.Fatalf("error %T is not *ProbeError", err)
	}
	if pe.Stage != "initialize" {
		t.Errorf("stage = %q, want initialize", pe.Stage)
	}
	if len(pe.StderrTail) == 0 || pe.StderrTail[0] != "boom: bad config" {
		t.Errorf("stderrTail = %v, want [boom: bad config]", pe.StderrTail)
	}
}

// TestProbeLargeToolList — 200 tools parse without truncation.
func TestProbeLargeToolList(t *testing.T) {
	caps, err := probeHelper(t, "bigtools", 0, 0)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(caps.Tools) != 200 {
		t.Fatalf("tools = %d, want 200", len(caps.Tools))
	}
	if caps.Tools[0].Name != "tool_000" || caps.Tools[199].Name != "tool_199" {
		t.Errorf("first/last tool = %q/%q, want tool_000/tool_199",
			caps.Tools[0].Name, caps.Tools[199].Name)
	}
}

// TestProbeToolListErrorSurfaces — a non-32601 tools/list error is a real
// failure, not an empty capability list.
func TestProbeToolListErrorSurfaces(t *testing.T) {
	_, err := probeHelper(t, "toolserror", 0, 0)
	if err == nil {
		t.Fatal("Probe: want error, got nil")
	}
	if !strings.Contains(err.Error(), "tools exploded") {
		t.Errorf("error = %q, want JSON-RPC error text", err)
	}
}

// TestProbeEmptyCommand — an empty command fails at the spawn stage.
func TestProbeEmptyCommand(t *testing.T) {
	_, err := Probe(context.Background(), nil, nil, "", 0)
	if err == nil {
		t.Fatal("Probe: want error, got nil")
	}
	var pe *ProbeError
	if !errors.As(err, &pe) || pe.Stage != "spawn" {
		t.Fatalf("error = %v, want spawn-stage ProbeError", err)
	}
}

// TestProbeMissingExecutable — an unknown binary fails the PATH lookup
// honestly at the spawn stage.
func TestProbeMissingExecutable(t *testing.T) {
	_, err := Probe(context.Background(),
		[]string{"definitely-not-a-real-pharos-exe-xyz"}, nil, "", 0)
	if err == nil {
		t.Fatal("Probe: want error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want not-found mention", err)
	}
}
