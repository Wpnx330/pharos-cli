// Package mcpserver implements the `pharos serve` MCP stdio server: a
// hand-rolled JSON-RPC 2.0 endpoint over stdin/stdout that exposes Pharos
// registry and lockfile data as MCP tools.
//
// Transport: newline-delimited JSON-RPC 2.0 (the MCP stdio transport) —
// the server-side mirror of internal/mcpclient's request framing. stdlib
// only; no MCP SDK.
//
// Purity law: stdout carries ONLY protocol frames, one JSON object per
// line. Any progress, banner, color, or log line on stdout corrupts the
// protocol; diagnostics go to the error writer (stderr when serving for
// real). Tool payloads are data-layer only — no ui colors, no tables, no
// progress lines.
//
// Concurrency: frames are processed sequentially in arrival order. That
// is sufficient for MCP stdio clients (they issue requests one at a time
// for tools) and eliminates response-interleaving races by construction.
package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Wpnx330/pharos-cli/internal/api"
)

// ProtocolVersion is the MCP protocol version pharos serves.
const ProtocolVersion = "2024-11-05"

// JSON-RPC 2.0 error codes used by the server.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// maxLineSize bounds one protocol frame (matches the mcpclient scanner
// budget; tool arguments are small but be generous).
const maxLineSize = 1024 * 1024

// nullID is the JSON-RPC id used in error replies when the request id is
// unknown (parse errors, non-object frames).
var nullID = json.RawMessage("null")

// Config wires the server to its environment.
type Config struct {
	// Version is reported as serverInfo.version (cmd.Version).
	Version string
	// AllowInstall exposes the install tool (default OFF — see
	// tools_install.go for the flag contract).
	AllowInstall bool
	// Client is the registry API client backing search/info/install.
	Client *api.Client
}

// toolDef pairs a tools/list descriptor with its dispatcher entry.
type toolDef struct {
	spec Tool
	run  func(s *Server, args json.RawMessage) (any, error)
}

// Server is the MCP stdio server. New + Run is the whole surface.
type Server struct {
	cfg    Config
	tools  []Tool
	byName map[string]toolDef
}

// New builds a Server. The tool surface is fixed at construction
// (AllowInstall), matching the flag-gated `pharos serve` contract.
func New(cfg Config) *Server {
	s := &Server{cfg: cfg}
	s.tools, s.byName = buildTools(cfg)
	return s
}

// Run serves JSON-RPC over in→out until EOF, a stdout write failure, or
// ctx cancellation. errW receives diagnostics (pass os.Stderr; nil is
// discarded). Run never writes to out except protocol frames.
//
// Cancellation note: a blocked Read cannot be interrupted, so after ctx
// fires Run returns while the read loop winds down in the background —
// callers that cancel should also close the reader (stdin) to release it.
func (s *Server) Run(ctx context.Context, in io.Reader, out io.Writer, errW io.Writer) error {
	if errW == nil {
		errW = io.Discard
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.serveLoop(in, out, errW)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	return nil
}

// serveLoop reads newline-delimited frames and writes responses. A
// malformed line is answered with an error frame (never dropped
// silently) so strict clients always see a reply for what they sent.
func (s *Server) serveLoop(in io.Reader, out io.Writer, errW io.Writer) {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), maxLineSize)
	w := bufio.NewWriter(out)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		resp := s.handleFrame(line)
		if resp == nil {
			continue // notification or ignorable frame — no response
		}
		data, err := json.Marshal(resp)
		if err != nil {
			fmt.Fprintf(errW, "pharos serve: encode response: %v\n", err)
			continue
		}
		if _, err := w.Write(append(data, '\n')); err != nil {
			return // stdout gone (client closed) — stop serving
		}
		if err := w.Flush(); err != nil {
			return
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(errW, "pharos serve: read stdin: %v\n", err)
	}
}

// ── request dispatch ────────────────────────────────────────────────────────

// incoming is the subset of a JSON-RPC 2.0 request/notification pharos
// serves. The id is kept raw so responses echo the client's exact id
// shape (string, number, or whatever it sent).
type incoming struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

// handleFrame turns one stdin line into a response envelope, or nil when
// the frame must not be answered (notifications, response-shaped frames).
func (s *Server) handleFrame(line []byte) any {
	if !json.Valid(line) {
		return errorReply(nullID, codeParseError, "parse error: line is not valid JSON")
	}
	var msg incoming
	err := json.Unmarshal(line, &msg)
	if err != nil {
		// Valid JSON but not a request object (array, scalar, ...).
		return errorReply(nullID, codeInvalidRequest, "invalid request: expected a JSON-RPC 2.0 object with a method")
	}
	if msg.Method == "" && msg.Result == nil && msg.Error == nil {
		// Object with neither method nor response fields.
		return errorReply(echoableID(msg.ID), codeInvalidRequest, "invalid request: expected a JSON-RPC 2.0 object with a method")
	}
	// Response-shaped frames (client echoing results at us) are ignored.
	if msg.Method == "" {
		return nil
	}
	if msg.JSONRPC != "" && msg.JSONRPC != "2.0" {
		return errorReply(echoableID(msg.ID), codeInvalidRequest,
			fmt.Sprintf("invalid request: unsupported jsonrpc version %q", msg.JSONRPC))
	}
	// Notifications (no usable id) are processed and never answered —
	// including well-known methods sent without an id.
	if !hasID(msg.ID) {
		return nil
	}

	switch msg.Method {
	case "initialize":
		return s.reply(msg.ID, s.initializeResult())
	case "tools/list":
		return s.reply(msg.ID, map[string]any{"tools": s.tools})
	case "tools/call":
		return s.handleToolsCall(msg)
	case "ping":
		return s.reply(msg.ID, struct{}{})
	default:
		return errorReply(msg.ID, codeMethodNotFound, fmt.Sprintf("method not found: %q", msg.Method))
	}
}

// initializeResult is the MCP initialize handshake answer. The client's
// requested version is not negotiated: pharos serves exactly one version
// and states it (clients disconnect when incompatible, per spec).
func (s *Server) initializeResult() map[string]any {
	return map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "pharos",
			"version": s.cfg.Version,
		},
		"instructions": "PHAROS MCP server: search the registry, inspect packages, and list locally installed MCP servers.",
	}
}

// handleToolsCall implements tools/call dispatch by name. Unknown tools
// and malformed arguments are -32602 protocol errors (mirroring the
// reference SDK); failures inside a known tool become isError tool
// results — never a crash.
func (s *Server) handleToolsCall(msg incoming) any {
	params := bytes.TrimSpace(msg.Params)
	if len(params) == 0 {
		params = []byte("{}")
	}
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return errorReply(msg.ID, codeInvalidParams, fmt.Sprintf("invalid params: %v", err))
	}
	if call.Name == "" {
		return errorReply(msg.ID, codeInvalidParams, "invalid params: tools/call requires a tool name")
	}
	def, ok := s.byName[call.Name]
	if !ok {
		return errorReply(msg.ID, codeInvalidParams, fmt.Sprintf("invalid params: unknown tool %q", call.Name))
	}
	args := bytes.TrimSpace(call.Arguments)
	if len(args) == 0 || bytes.Equal(args, []byte("null")) {
		args = []byte("{}")
	} else if args[0] != '{' {
		return errorReply(msg.ID, codeInvalidParams, "invalid params: arguments must be an object")
	}
	result, err := s.callTool(def, call.Name, args)
	if err != nil {
		var bp *badParamsError
		if errors.As(err, &bp) {
			return errorReply(msg.ID, codeInvalidParams, err.Error())
		}
		return s.reply(msg.ID, errorToolResult(err.Error()))
	}
	return s.reply(msg.ID, result)
}

// callTool runs one tool with panic recovery. Business failures return
// an error that the dispatcher wraps as an isError tool result; argument
// shape failures (badParamsError) surface as -32602 protocol errors.
func (s *Server) callTool(def toolDef, name string, args json.RawMessage) (res toolResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			res = errorToolResult(fmt.Sprintf("internal error executing %s: %v", name, r))
			err = nil
		}
	}()
	payload, err := def.run(s, args)
	if err != nil {
		return toolResult{}, err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return toolResult{}, fmt.Errorf("failed to encode %s result: %w", name, err)
	}
	return toolResult{
		Content: []toolContent{{Type: "text", Text: string(data)}},
		IsError: false,
	}, nil
}

// ── response envelopes ──────────────────────────────────────────────────────

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// toolResult is the MCP tools/call result shape.
type toolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// errorToolResult builds an isError tool result with a human-readable
// message (tool failures are results, not protocol errors).
func errorToolResult(msg string) toolResult {
	return toolResult{
		Content: []toolContent{{Type: "text", Text: msg}},
		IsError: true,
	}
}

// hasID reports whether the raw id is a real request id (present and not
// JSON null — notifications carry neither, mirroring mcpclient's
// normalizeID convention).
func hasID(raw json.RawMessage) bool {
	s := bytes.TrimSpace(raw)
	return len(s) > 0 && !bytes.Equal(s, []byte("null"))
}

// echoableID returns the request id when it can be echoed, else null.
func echoableID(raw json.RawMessage) json.RawMessage {
	if hasID(raw) {
		return raw
	}
	return nullID
}

func (s *Server) reply(id json.RawMessage, result any) any {
	return rpcResponse{JSONRPC: "2.0", ID: echoableID(id), Result: result}
}

func errorReply(id json.RawMessage, code int, message string) any {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}
