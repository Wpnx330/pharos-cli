// Package mcpclient implements a minimal JSON-RPC-over-stdio MCP client
// used by `pharos try` to probe a stdio server's live capabilities — the
// initialize handshake plus tools/resources/prompts listing — without
// touching any client configuration.
//
// Transport: line-delimited JSON-RPC 2.0 over the child's stdin/stdout
// only (no HTTP). stdlib only: encoding/json + bufio.
package mcpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/runtime"
)

// ProtocolVersion is the MCP protocol version pharos negotiates.
const ProtocolVersion = "2025-06-18"

const (
	clientName    = "pharos"
	clientVersion = "1.1.0"

	// errMethodNotFound is the JSON-RPC code for "method not found"; list
	// calls answered with it are treated as "capability absent".
	errMethodNotFound = -32601
)

// perRequestTimeout bounds each JSON-RPC round trip independently of the
// overall probe budget. A var so tests can shorten it.
var perRequestTimeout = 10 * time.Second

// Tool is one entry of a server's tools/list result.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ServerInfo identifies the probed server as reported by the initialize
// handshake.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Caps is the capability summary collected from a live MCP handshake.
// Resources and Prompts carry names (count = len), so `pharos try` can
// list them when the set is small.
type Caps struct {
	ProtocolVersion string     `json:"protocolVersion"`
	ServerInfo      ServerInfo `json:"serverInfo"`
	Tools           []Tool     `json:"tools"`
	Resources       []string   `json:"resources"`
	Prompts         []string   `json:"prompts"`
}

// ProbeError wraps a probe failure with the stage it failed at and the
// tail of the server's stderr, so `pharos try` can show honest output
// instead of a bare "exit status 1".
type ProbeError struct {
	Stage      string // "spawn", "initialize", "tools/list", "resources/list", "prompts/list", ...
	Err        error
	StderrTail []string // last lines of server stderr (max 10), when captured
}

func (e *ProbeError) Error() string { return fmt.Sprintf("%s: %v", e.Stage, e.Err) }

func (e *ProbeError) Unwrap() error { return e.Err }

// Probe spawns the stdio server described by command, performs the MCP
// initialize handshake over line-delimited JSON-RPC, then lists tools,
// resources, and prompts. env entries are appended to the current
// environment; dir becomes the process working directory. timeout bounds
// the whole probe (0 = no overall budget; per-request timeouts still
// apply). The server process is always cleaned up — on success it is
// killed once capabilities are collected; a real wiring would restart it.
func Probe(ctx context.Context, command []string, env map[string]string, dir string, timeout time.Duration) (*Caps, error) {
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return nil, &ProbeError{Stage: "spawn", Err: errors.New("empty command")}
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// ResolveSpawnExe gives PATH lookup plus the python→python3 fallback
	// used everywhere else pharos spawns servers.
	exe, err := runtime.ResolveSpawnExe(command[0])
	if err != nil {
		return nil, &ProbeError{Stage: "spawn", Err: err}
	}

	cmd := exec.Command(exe, command[1:]...)
	cmd.Dir = dir
	cmd.Env = envWith(os.Environ(), env)
	setProcGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, &ProbeError{Stage: "spawn", Err: fmt.Errorf("stdin pipe: %w", err)}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, &ProbeError{Stage: "spawn", Err: fmt.Errorf("stdout pipe: %w", err)}
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, &ProbeError{Stage: "spawn", Err: fmt.Errorf("stderr pipe: %w", err)}
	}

	if err := cmd.Start(); err != nil {
		return nil, &ProbeError{Stage: "spawn", Err: err}
	}

	tail := &stderrTail{max: 10}
	go func() { _, _ = io.Copy(tail, stderrPipe) }()

	c := newClient(cmd, stdin)
	caps, perr := c.run(ctx, stdout)
	c.close()
	if perr != nil {
		return nil, &ProbeError{Stage: c.stage, Err: perr, StderrTail: tail.tail()}
	}
	return caps, nil
}

// envWith appends KEY=VALUE pairs to base without disturbing os.Environ.
func envWith(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	out := base
	for k, v := range extra {
		out = append(out, k+"="+v)
	}
	return out
}

// ── JSON-RPC framing ────────────────────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// normalizeID reduces a raw JSON id to a comparable key. Notifications
// (no id) and null ids report false. Quotes are stripped so a server that
// echoes a string id still matches pharos's numeric request id.
func normalizeID(raw json.RawMessage) (string, bool) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return "", false
	}
	return strings.Trim(s, `"`), true
}

// ── stdio client ────────────────────────────────────────────────────────────

type client struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	pending map[string]chan rpcMessage
	mu      sync.Mutex
	seq     int64
	stage   string
	done    chan struct{} // closed by waitLoop after cmd.Wait returns
	waitErr error
}

func newClient(cmd *exec.Cmd, stdin io.WriteCloser) *client {
	return &client{
		cmd:     cmd,
		stdin:   stdin,
		pending: make(map[string]chan rpcMessage),
		stage:   "initialize",
		done:    make(chan struct{}),
		seq:     1,
	}
}

// run performs the handshake + capability listing. It owns the reader and
// reaper goroutines for the child.
func (c *client) run(ctx context.Context, stdout io.Reader) (*Caps, error) {
	go c.readLoop(stdout)
	go c.waitLoop()

	var initResult struct {
		ProtocolVersion string     `json:"protocolVersion"`
		ServerInfo      ServerInfo `json:"serverInfo"`
	}
	if err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": clientName, "version": clientVersion},
	}, &initResult); err != nil {
		return nil, err
	}

	c.stage = "notifications/initialized"
	const notif = `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	if _, err := io.WriteString(c.stdin, notif); err != nil {
		return nil, fmt.Errorf("write initialized notification: %w", err)
	}

	caps := &Caps{
		ProtocolVersion: initResult.ProtocolVersion,
		ServerInfo:      initResult.ServerInfo,
		Tools:           []Tool{},
		Resources:       []string{},
		Prompts:         []string{},
	}

	c.stage = "tools/list"
	var tools struct {
		Tools []Tool `json:"tools"`
	}
	err := c.call(ctx, "tools/list", map[string]any{}, &tools)
	if err != nil && !isMethodNotFound(err) {
		return nil, err
	}
	if err == nil {
		caps.Tools = tools.Tools
	}

	c.stage = "resources/list"
	var resources struct {
		Resources []struct {
			Name string `json:"name"`
			URI  string `json:"uri"`
		} `json:"resources"`
	}
	err = c.call(ctx, "resources/list", map[string]any{}, &resources)
	if err != nil && !isMethodNotFound(err) {
		return nil, err
	}
	if err == nil {
		for _, r := range resources.Resources {
			if r.Name != "" {
				caps.Resources = append(caps.Resources, r.Name)
			} else {
				caps.Resources = append(caps.Resources, r.URI)
			}
		}
	}

	c.stage = "prompts/list"
	var prompts struct {
		Prompts []struct {
			Name string `json:"name"`
		} `json:"prompts"`
	}
	err = c.call(ctx, "prompts/list", map[string]any{}, &prompts)
	if err != nil && !isMethodNotFound(err) {
		return nil, err
	}
	if err == nil {
		for _, p := range prompts.Prompts {
			caps.Prompts = append(caps.Prompts, p.Name)
		}
	}

	return caps, nil
}

// call writes one JSON-RPC request and waits for the matching response.
// Notifications interleaved by the server, non-JSON stdout noise, and
// duplicate replies are ignored; a server exit, per-request timeout, or
// exhausted probe budget all surface as errors.
func (c *client) call(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	id := strconv.FormatInt(c.seq, 10)
	c.seq++
	ch := make(chan rpcMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	raw, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	if _, err := c.stdin.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("write request: %w", err)
	}

	timer := time.NewTimer(perRequestTimeout)
	defer timer.Stop()
	select {
	case msg := <-ch:
		if msg.Error != nil {
			return msg.Error
		}
		if result != nil && len(msg.Result) > 0 {
			if err := json.Unmarshal(msg.Result, result); err != nil {
				return fmt.Errorf("parse result: %w", err)
			}
		}
		return nil
	case <-timer.C:
		return fmt.Errorf("no response within %s", perRequestTimeout)
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		if c.waitErr != nil {
			return fmt.Errorf("server exited before responding: %v", c.waitErr)
		}
		return errors.New("server closed stdout before responding")
	}
}

// readLoop parses stdout lines and routes responses to waiting calls.
// Unparseable lines (server banners on stdout) are skipped.
func (c *client) readLoop(stdout io.Reader) {
	reader := bufio.NewReaderSize(stdout, 64*1024)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			c.dispatch(line)
		}
		if err != nil {
			return
		}
	}
}

func (c *client) dispatch(line []byte) {
	var msg rpcMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return
	}
	id, ok := normalizeID(msg.ID)
	if !ok {
		return // notification or parse-error frame — nothing pending
	}
	c.mu.Lock()
	ch := c.pending[id]
	c.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- msg:
	default: // duplicate reply for an id nobody waits on anymore
	}
}

// waitLoop reaps the child; waitErr is published before done closes.
func (c *client) waitLoop() {
	c.waitErr = c.cmd.Wait()
	close(c.done)
}

// close shuts the probe down: close stdin (graceful for well-behaved
// servers), then process-group kill if the grace period lapses.
func (c *client) close() {
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		select {
		case <-c.done:
		case <-time.After(500 * time.Millisecond):
			_ = killProc(c.cmd.Process.Pid)
		}
	}
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
	}
}

func isMethodNotFound(err error) bool {
	var re *rpcError
	return errors.As(err, &re) && re.Code == errMethodNotFound
}

// stderrTail is an io.Writer that keeps the last max non-empty lines of
// the server's stderr.
type stderrTail struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func (t *stderrTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, line := range strings.Split(string(p), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		t.lines = append(t.lines, line)
		if len(t.lines) > t.max {
			t.lines = t.lines[len(t.lines)-t.max:]
		}
	}
	return len(p), nil
}

func (t *stderrTail) tail() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, len(t.lines))
	copy(out, t.lines)
	return out
}
