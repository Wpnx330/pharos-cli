// Package daemon implements the Pharos daemon — a background process
// supervisor for HTTP/SSE/streamable-http MCP servers with JIT loading
// and configurable idle-timeout auto-unloading.
//
// The daemon holds a proxy listener on a dedicated port for each managed
// server. When a request arrives:
//   - If the backing server is running: proxy through, update lastActivity.
//   - If unloaded: start the backing process (JIT), wait for it, then proxy.
//
// After idleTimeout minutes with no activity, the backing process is killed
// but the proxy listener stays alive — so the next request JIT-reloads it.
//
// stdio servers are NOT managed by the daemon; MCP clients spawn those as
// child processes.
package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/canonical"
)

// ── Public types (used by cmd/daemon.go) ────────────────────────────────

// DaemonStatus holds information about the daemon process and the
// servers it is currently managing.
type DaemonStatus struct {
	Running   bool           // whether the daemon is currently running
	PID       int            // daemon process ID (0 if not running)
	Port      int            // base port (informational)
	StartedAt time.Time      // when the daemon was started
	Servers   []ServerStatus // managed server details
}

// ServerStatus holds the state of a single server managed by the daemon.
type ServerStatus struct {
	Name         string    // canonical server name
	State        string    // "running" | "unloaded" | "starting" | "error"
	Port         int       // proxy listener port
	Memory       int64     // RSS in bytes (0 if not running)
	LastActivity time.Time // last time the server handled a request
	IdleTimeout  int       // configured idle timeout in minutes (0 = never)
}

// ── Internal state types ────────────────────────────────────────────────

// DaemonState is persisted to ~/.pharos/daemon.json.
type DaemonState struct {
	PID       int                    `json:"pid"`
	StartedAt time.Time              `json:"startedAt"`
	Servers   map[string]ServerState `json:"servers"`
}

// ServerState is the per-server entry in DaemonState.
type ServerState struct {
	State        string    `json:"state"` // "running" | "unloaded"
	PID          int       `json:"pid"`
	Port         int       `json:"port"`
	StartedAt    time.Time `json:"startedAt"`
	LastActivity time.Time `json:"lastActivity"`
	IdleTimeout  int       `json:"idleTimeout"` // minutes; 0 = never unload
}

// ── Paths ───────────────────────────────────────────────────────────────

func daemonDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".pharos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// daemonDirFn is overridable in tests.
var daemonDirFn = daemonDir

func daemonPIDPath() (string, error) {
	dir, err := daemonDirFn()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.pid"), nil
}

func daemonStatePath() (string, error) {
	dir, err := daemonDirFn()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.json"), nil
}

func daemonLogPath() (string, error) {
	dir, err := daemonDirFn()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.log"), nil
}

// ── State persistence ───────────────────────────────────────────────────

func loadState() (*DaemonState, error) {
	path, err := daemonStatePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &DaemonState{Servers: make(map[string]ServerState)}, nil
		}
		return nil, err
	}
	var st DaemonState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse daemon state: %w", err)
	}
	if st.Servers == nil {
		st.Servers = make(map[string]ServerState)
	}
	return &st, nil
}

func saveState(st *DaemonState) error {
	path, err := daemonStatePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

// ── PID helpers ─────────────────────────────────────────────────────────

func readDaemonPID() (int, error) {
	path, err := daemonPIDPath()
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, nil // no PID file = not running
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return 0, nil
	}
	return pid, nil
}

func writeDaemonPID(pid int) error {
	path, err := daemonPIDPath()
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o600)
}

func removeDaemonPID() {
	path, err := daemonPIDPath()
	if err == nil {
		os.Remove(path)
	}
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// ── Daemon ──────────────────────────────────────────────────────────────

// Daemon is the core supervisor. One instance runs per machine.
type Daemon struct {
	mu      sync.RWMutex
	servers map[string]*ManagedServer
	state   *DaemonState
	log     *log.Logger
	logFile *os.File
	done    chan struct{}
}

// ManagedServer is one MCP server under daemon supervision.
type ManagedServer struct {
	Name         string
	Port         int // daemon proxy port
	IdleTimeout  int // minutes; 0 = never unload
	Command      string
	Args         []string
	WorkDir      string
	Env          []string
	BackingPort  int // actual server port (may differ from proxy port)

	mu            sync.Mutex
	state         string // "running" | "unloaded"
	pid           int
	startedAt     time.Time
	lastActivity  time.Time
	listener      net.Listener
	daemon        *Daemon // back-reference for state updates
}

// Start starts the daemon. It blocks until SIGTERM/SIGINT.
func Start() error {
	// Check if already running
	pid, _ := readDaemonPID()
	if pid > 0 && isProcessAlive(pid) {
		return fmt.Errorf("daemon already running (PID %d)", pid)
	}

	// Open log file
	logPath, err := daemonLogPath()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}

	d := &Daemon{
		servers: make(map[string]*ManagedServer),
		state: &DaemonState{
			PID:       os.Getpid(),
			StartedAt: time.Now(),
			Servers:   make(map[string]ServerState),
		},
		log:     log.New(logFile, "", log.LstdFlags),
		logFile: logFile,
		done:    make(chan struct{}),
	}

	// Write PID file
	if err := writeDaemonPID(os.Getpid()); err != nil {
		logFile.Close()
		return fmt.Errorf("write PID file: %w", err)
	}

	// Load previous state for port assignments
	prevState, _ := loadState()

	// Load canonical config
	cfg, err := canonical.Load()
	if err != nil {
		d.cleanup()
		return fmt.Errorf("load canonical config: %w", err)
	}

	// Build managed servers from canonical config
	portOffset := 0
	for name, srv := range cfg.Servers {
		transport := srv.Transport
		if transport != "http-sse" && transport != "http" && transport != "streamable-http" {
			continue // skip stdio servers
		}

		// Allocate port: reuse from previous state, else 8421 + offset
		port := 0
		prevIdle := 0
		if prev, ok := prevState.Servers[name]; ok && prev.Port > 0 {
			port = prev.Port
			prevIdle = prev.IdleTimeout
		} else {
			port = 8421 + portOffset
			portOffset++
		}

		idleTimeout := srv.IdleTimeout
		if idleTimeout == 0 && prevIdle == 0 {
			// Default is 60 — but only if not explicitly set to 0 via flag.
			// canonical.Server.IdleTimeout is 0 when omitted (omitempty),
			// so we can't distinguish "not set" from "set to 0" here.
			// The install command writes 60 as default, so 0 means explicitly
			// set to "never unload". We respect that.
			idleTimeout = 0 // never unload
		} else if idleTimeout == 0 && prevIdle > 0 {
			idleTimeout = prevIdle
		}

		ms := &ManagedServer{
			Name:        name,
			Port:        port,
			IdleTimeout: idleTimeout,
			WorkDir:     srv.Cwd,
			daemon:      d,
		}

		// Build command from canonical server config
		if srv.Command != "" {
			ms.Command = srv.Command
			ms.Args = srv.Args
		}
		// Extract env from map
		for k, v := range srv.Env {
			ms.Env = append(ms.Env, k+"="+v)
		}

		// Determine backing port from command or URL
		if srv.URL != "" {
			// Remote URL — parse port
			if u, err := url.Parse(srv.URL); err == nil {
				if p, err := strconv.Atoi(u.Port()); err == nil {
					ms.BackingPort = p
				}
			}
			// For remote servers, there's no local process to manage.
			// Skip — daemon only manages servers with a local command.
			if srv.Command == "" {
				continue
			}
		}

		// Try to extract backing port from command/args
		if ms.BackingPort == 0 {
			for _, arg := range ms.Args {
				if p, err := strconv.Atoi(arg); err == nil && p > 1024 && p < 65536 {
					ms.BackingPort = p
					break
				}
			}
		}
		// Default backing port to proxy port if we can't find one
		if ms.BackingPort == 0 {
			ms.BackingPort = port
		}

		// Start proxy listener
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			d.log.Printf("ERROR: failed to listen on port %d for %s: %v", port, name, err)
			continue
		}
		ms.listener = listener
		ms.state = "unloaded"

		d.mu.Lock()
		d.servers[name] = ms
		d.state.Servers[name] = ServerState{
			State:       "unloaded",
			Port:        port,
			IdleTimeout: idleTimeout,
		}
		d.mu.Unlock()

		d.log.Printf("Started proxy listener for %s on port %d (idleTimeout=%dm)", name, port, idleTimeout)

		// If idleTimeout == 0 (never unload), start the server immediately
		if idleTimeout == 0 {
			go func() {
				if err := ms.startBacking(); err != nil {
					d.log.Printf("ERROR: failed to start always-on server %s: %v", name, err)
				}
			}()
		}

		// Start proxy goroutine
		go ms.serveProxy()
	}

	// Start idle checker
	go d.idleChecker()

	// Save state
	if err := saveState(d.state); err != nil {
		d.log.Printf("WARNING: failed to save state: %v", err)
	}

	d.log.Printf("Daemon started (PID %d), managing %d servers", os.Getpid(), len(d.servers))

	// Wait for signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	for {
		sig := <-sigCh
		if sig == syscall.SIGHUP {
			d.log.Println("Received SIGHUP, reloading canonical config...")
			d.reconcile()
			continue
		}
		// SIGTERM or SIGINT — shutdown
		d.log.Println("Shutting down daemon...")
		d.shutdown()
		break
	}

	return nil
}

// Stop sends SIGTERM to the running daemon.
func Stop() error {
	pid, err := readDaemonPID()
	if err != nil || pid == 0 {
		return fmt.Errorf("daemon is not running (no PID file)")
	}
	if !isProcessAlive(pid) {
		removeDaemonPID()
		return fmt.Errorf("daemon is not running (stale PID file removed)")
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM to daemon: %w", err)
	}

	// Wait for daemon to exit (up to 10 seconds)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !isProcessAlive(pid) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	if isProcessAlive(pid) {
		return fmt.Errorf("daemon did not stop within 10s (PID %d)", pid)
	}

	removeDaemonPID()
	return nil
}

// Status returns information about the daemon and managed servers.
func Status() (*DaemonStatus, error) {
	pid, _ := readDaemonPID()
	if pid == 0 || !isProcessAlive(pid) {
		if pid > 0 {
			removeDaemonPID()
		}
		return &DaemonStatus{Running: false}, nil
	}

	// Load state file
	st, err := loadState()
	if err != nil {
		return nil, fmt.Errorf("load daemon state: %w", err)
	}

	ds := &DaemonStatus{
		Running:   true,
		PID:       pid,
		StartedAt: st.StartedAt,
	}

	// Read /proc for memory if running
	for name, ss := range st.Servers {
		s := ServerStatus{
			Name:        name,
			State:       ss.State,
			Port:        ss.Port,
			LastActivity: ss.LastActivity,
			IdleTimeout: ss.IdleTimeout,
		}
		if ss.PID > 0 && isProcessAlive(ss.PID) {
			s.Memory = readProcessMemory(ss.PID)
		}
		ds.Servers = append(ds.Servers, s)
	}

	return ds, nil
}

// ── Daemon internal methods ─────────────────────────────────────────────

func (d *Daemon) shutdown() {
	d.mu.Lock()
	defer d.mu.Unlock()

	for name, ms := range d.servers {
		if ms.state == "running" {
			d.log.Printf("Stopping %s (PID %d)...", name, ms.pid)
			ms.stopBacking()
		}
		if ms.listener != nil {
			ms.listener.Close()
		}
	}

	removeDaemonPID()
	d.logFile.Close()
}

func (d *Daemon) cleanup() {
	removeDaemonPID()
	if d.logFile != nil {
		d.logFile.Close()
	}
}

func (d *Daemon) idleChecker() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			d.checkIdle()
		case <-d.done:
			return
		}
	}
}

func (d *Daemon) checkIdle() {
	d.mu.RLock()
	defer d.mu.RUnlock()

	now := time.Now()
	for name, ms := range d.servers {
		ms.mu.Lock()
		if ms.state != "running" || ms.IdleTimeout <= 0 {
			ms.mu.Unlock()
			continue
		}

		idle := now.Sub(ms.lastActivity)
		if idle >= time.Duration(ms.IdleTimeout)*time.Minute {
			d.log.Printf("Auto-unloading %s (idle %v, timeout %dm)", name, idle.Round(time.Second), ms.IdleTimeout)
			ms.mu.Unlock()
			ms.stopBacking()
			d.mu.RUnlock()
			d.mu.Lock()
			d.state.Servers[name] = ServerState{
				State:       "unloaded",
				Port:        ms.Port,
				IdleTimeout: ms.IdleTimeout,
				LastActivity: ms.lastActivity,
			}
			d.mu.Unlock()
			d.mu.RLock()
			_ = saveState(d.state)
		} else {
			ms.mu.Unlock()
		}
	}
}

// reconcile re-reads the canonical config and adds/removes servers.
func (d *Daemon) reconcile() {
	cfg, err := canonical.Load()
	if err != nil {
		d.log.Printf("ERROR: failed to reload canonical config: %v", err)
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// Find servers to add
	maxPort := 8420
	for _, ss := range d.state.Servers {
		if ss.Port > maxPort {
			maxPort = ss.Port
		}
	}

	for name, srv := range cfg.Servers {
		transport := srv.Transport
		if transport != "http-sse" && transport != "http" && transport != "streamable-http" {
			continue
		}
		if srv.Command == "" {
			continue // remote-only, no local process
		}
		if _, exists := d.servers[name]; exists {
			continue // already managed
		}

		// Add new server
		maxPort++
		port := maxPort

		ms := &ManagedServer{
			Name:        name,
			Port:        port,
			IdleTimeout: srv.IdleTimeout,
			Command:     srv.Command,
			Args:        srv.Args,
			WorkDir:     srv.Cwd,
			daemon:      d,
		}
		for k, v := range srv.Env {
			ms.Env = append(ms.Env, k+"="+v)
		}

		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			d.log.Printf("ERROR: failed to listen on port %d for %s: %v", port, name, err)
			continue
		}
		ms.listener = listener
		ms.state = "unloaded"

		d.servers[name] = ms
		d.state.Servers[name] = ServerState{
			State:       "unloaded",
			Port:        port,
			IdleTimeout: srv.IdleTimeout,
		}

		d.log.Printf("Added server %s on port %d via SIGHUP reconcile", name, port)
		go ms.serveProxy()

		if srv.IdleTimeout == 0 {
			go func() {
				if err := ms.startBacking(); err != nil {
					d.log.Printf("ERROR: failed to start always-on server %s: %v", name, err)
				}
			}()
		}
	}

	// Find servers to remove
	for name := range d.servers {
		if _, exists := cfg.Servers[name]; !exists {
			ms := d.servers[name]
			if ms.state == "running" {
				ms.stopBacking()
			}
			if ms.listener != nil {
				ms.listener.Close()
			}
			delete(d.servers, name)
			delete(d.state.Servers, name)
			d.log.Printf("Removed server %s via SIGHUP reconcile", name)
		}
	}

	_ = saveState(d.state)
}

// ── ManagedServer methods ───────────────────────────────────────────────

// serveProxy runs the HTTP proxy that forwards requests to the backing
// server, JIT-loading it on first request.
func (ms *ManagedServer) serveProxy() {
	server := &http.Server{
		Handler: http.HandlerFunc(ms.handleProxy),
	}
	_ = server.Serve(ms.listener)
}

// handleProxy is the per-request handler. It ensures the backing server
// is running (JIT load if needed), then proxies the request.
func (ms *ManagedServer) handleProxy(w http.ResponseWriter, r *http.Request) {
	ms.mu.Lock()
	if ms.state != "running" {
		ms.mu.Unlock()
		// JIT load
		if err := ms.startBacking(); err != nil {
			http.Error(w, fmt.Sprintf("daemon: failed to start server: %v", err), http.StatusBadGateway)
			return
		}
	} else {
		ms.mu.Unlock()
	}

	// Update last activity
	ms.mu.Lock()
	ms.lastActivity = time.Now()
	ms.mu.Unlock()

	// Update daemon state
	ms.daemon.mu.Lock()
	ss := ms.daemon.state.Servers[ms.Name]
	ss.LastActivity = ms.lastActivity
	ss.State = "running"
	ms.daemon.state.Servers[ms.Name] = ss
	ms.daemon.mu.Unlock()
	_ = saveState(ms.daemon.state)

	// Proxy to backing server
	target := fmt.Sprintf("http://127.0.0.1:%d", ms.BackingPort)
	targetURL, _ := url.Parse(target)
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.ServeHTTP(w, r)
}

// startBacking starts the actual MCP server process (JIT load).
func (ms *ManagedServer) startBacking() error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if ms.state == "running" {
		return nil // already running
	}

	if ms.Command == "" {
		return fmt.Errorf("server %s has no command to start", ms.Name)
	}

	// Parse command
	cmdStr := ms.Command
	parts := splitCommand(cmdStr)
	if len(parts) == 0 {
		return fmt.Errorf("empty command for %s", ms.Name)
	}

	// Check executable
	exe := parts[0]
	if _, err := exec.LookPath(exe); err != nil {
		return fmt.Errorf("executable %q not found: %w", exe, err)
	}

	// Build command
	args := append(parts[1:], ms.Args...)
	cmd := exec.Command(exe, args...)
	cmd.Dir = ms.WorkDir
	cmd.Env = append(os.Environ(), ms.Env...)

	// Redirect output to daemon log
	if ms.daemon.logFile != nil {
		cmd.Stdout = ms.daemon.logFile
		cmd.Stderr = ms.daemon.logFile
	}

	// Set process group
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	ms.pid = cmd.Process.Pid
	ms.state = "running"
	ms.startedAt = time.Now()
	ms.lastActivity = time.Now()

	// Wait for port to be ready (up to 10s)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if isPortOpen(ms.BackingPort) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Update daemon state
	ms.daemon.mu.Lock()
	ss := ms.daemon.state.Servers[ms.Name]
	ss.State = "running"
	ss.PID = ms.pid
	ss.StartedAt = ms.startedAt
	ss.LastActivity = ms.lastActivity
	ms.daemon.state.Servers[ms.Name] = ss
	ms.daemon.mu.Unlock()
	_ = saveState(ms.daemon.state)

	ms.daemon.log.Printf("JIT-loaded %s (PID %d) on backing port %d", ms.Name, ms.pid, ms.BackingPort)

	// Reap the process when it exits
	go func() {
		_ = cmd.Wait()
		ms.mu.Lock()
		if ms.state == "running" {
			ms.state = "unloaded"
			ms.pid = 0
		}
		ms.mu.Unlock()
		ms.daemon.mu.Lock()
		ss := ms.daemon.state.Servers[ms.Name]
		ss.State = "unloaded"
		ss.PID = 0
		ms.daemon.state.Servers[ms.Name] = ss
		ms.daemon.mu.Unlock()
		_ = saveState(ms.daemon.state)
		ms.daemon.log.Printf("Backing process for %s exited", ms.Name)
	}()

	return nil
}

// stopBacking kills the backing server process but keeps the proxy listener.
func (ms *ManagedServer) stopBacking() {
	ms.mu.Lock()
	pid := ms.pid
	ms.mu.Unlock()

	if pid <= 0 {
		return
	}

	// SIGTERM the process group
	_ = syscall.Kill(-pid, syscall.SIGTERM)

	// Wait up to 5s
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !isProcessAlive(pid) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// SIGKILL if still alive
	if isProcessAlive(pid) {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		time.Sleep(500 * time.Millisecond)
	}

	ms.mu.Lock()
	ms.state = "unloaded"
	ms.pid = 0
	ms.mu.Unlock()

	ms.daemon.log.Printf("Unloaded %s", ms.Name)
}

// ── Helpers ─────────────────────────────────────────────────────────────

func isPortOpen(port int) bool {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func readProcessMemory(pid int) int64 {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	for _, line := range splitLines(string(data)) {
		if len(line) > 6 && line[:6] == "VmRSS:" {
			fields := splitFields(line)
			if len(fields) >= 2 {
				kb, err := strconv.ParseInt(fields[1], 10, 64)
				if err == nil {
					return kb * 1024
				}
			}
		}
	}
	return 0
}

func splitCommand(s string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false

	for _, ch := range s {
		switch ch {
		case '"':
			inQuote = !inQuote
		case ' ', '	':
			if !inQuote && current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			} else if inQuote {
				current.WriteRune(ch)
			}
		default:
			current.WriteRune(ch)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

func splitLines(s string) []string {
	var result []string
	start := 0
	for i, ch := range s {
		if ch == '\n' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

func splitFields(s string) []string {
	var result []string
	start := -1
	for i, ch := range s {
		if ch == ' ' || ch == '\t' {
			if start >= 0 {
				result = append(result, s[start:i])
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		result = append(result, s[start:])
	}
	return result
}
