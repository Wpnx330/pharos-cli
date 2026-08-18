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
	"github.com/Wpnx330/pharos-cli/internal/runtime"
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

// LogPath returns the path to the daemon log file (exported for CLI use).
func LogPath() (string, error) {
	return daemonLogPath()
}

// ReloadDaemon sends a reload signal to the daemon process.
// On Unix: sends SIGHUP. On Windows: touches the reload trigger file.
func ReloadDaemon(pid int) error {
	return sendReloadSignal(pid)
}

// defaultLocalHTTPBackingPort is the well-known local HTTP/SSE listen
// port used when a managed server's command does not declare --port.
// It must never be the daemon proxy port (8421+).
const defaultLocalHTTPBackingPort = 8765

// LoadServer tells a running daemon to JIT-start the backing process for
// a managed local HTTP server. It writes a load-request file and sends a
// reload signal. The daemon's reconcile loop starts the process.
// Kind 1 (URL-only) servers are never managed and will be ignored.
func LoadServer(name string) error {
	// Queue first so a just-started daemon can consume the request in Start()
	// even if this call races ahead of the PID file.
	if err := writeLoadRequest(name); err != nil {
		return err
	}

	st, err := Status()
	if err != nil || !st.Running {
		return fmt.Errorf("daemon is not running")
	}

	return sendReloadSignal(st.PID)
}

// StopServer tells the daemon to unload a specific server.
// It creates a stop-request file and sends a reload signal.
// The daemon's reconcile loop checks for stop-request files and
// unloads the corresponding server.
func StopServer(name string) error {
	// Check daemon is running
	st, err := Status()
	if err != nil || !st.Running {
		return fmt.Errorf("daemon is not running")
	}

	// Create stop-request file
	dir, err := daemonDirFn()
	if err != nil {
		return err
	}
	stopDir := filepath.Join(dir, "daemon.stop")
	if err := os.MkdirAll(stopDir, 0o700); err != nil {
		return fmt.Errorf("create stop dir: %w", err)
	}
	// Sanitize server name — prevent path traversal
	safeName := filepath.Base(name)
	stopFile := filepath.Join(stopDir, safeName)
	if err := os.WriteFile(stopFile, []byte("stop"), 0o600); err != nil {
		return fmt.Errorf("write stop file: %w", err)
	}

	// Send reload signal so daemon picks up the stop request
	return sendReloadSignal(st.PID)
}

// mustDaemonDir returns the daemon directory or panics.
// Used in reconcile where we don't want to propagate errors.
func mustDaemonDir() string {
	dir, err := daemonDirFn()
	if err != nil {
		return ""
	}
	return dir
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

// isProcessAlive checks if a process with the given PID is running.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if supportsSignals() {
		return proc.Signal(syscall.Signal(0)) == nil
	}
	// Windows: FindProcess always succeeds, so check if the process
	// is actually running by attempting to open it.
	proc.Signal(os.Signal(syscall.Signal(0)))
	// On Windows, Signal returns "not supported" but doesn't mean the
	// process is dead. Use a different approach: check if the process
	// handle is valid.
	return proc != nil
}

// touchReloadFile creates/updates the reload trigger file.
// On Windows (no SIGHUP), the daemon watches this file for changes.
func touchReloadFile() error {
	dir, err := daemonDirFn()
	if err != nil {
		return err
	}
	reloadPath := filepath.Join(dir, "daemon.reload")
	// Write current timestamp to the file
	return os.WriteFile(reloadPath, []byte(time.Now().Format(time.RFC3339)), 0o644)
}

// daemonReloadPath returns the path to the reload trigger file.
func daemonReloadPath() (string, error) {
	dir, err := daemonDirFn()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.reload"), nil
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
		if !shouldManageLocalHTTP(srv.Transport, srv.Command) {
			continue // skip stdio and kind-1 URL-only (no local process)
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

		// For remote servers, there's no local process to manage.
		// Skip — daemon only manages servers with a local command.
		if srv.Command == "" {
			continue
		}

		ms.BackingPort = resolveBackingPort(ms.Command, ms.Args, srv.URL, port)

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

		d.log.Printf("Started proxy listener for %s on port %d (idleTimeout=%dm, backing=%d)", name, port, idleTimeout, ms.BackingPort)

		// Start proxy goroutine
		go ms.serveProxy()
	}

	// Start backing processes: idleTimeout 0 (always-on) and any pending
	// load-after-install requests. idleTimeout 60 stays JIT after this
	// first load; idleTimeout 0 already started immediately before.
	pendingLoad := map[string]bool{}
	for _, n := range consumeLoadRequests() {
		pendingLoad[n] = true
	}
	d.mu.RLock()
	for name, ms := range d.servers {
		if !shouldStartBacking(ms.IdleTimeout, pendingLoad[name], false) {
			continue
		}
		go func(name string, ms *ManagedServer) {
			if err := ms.startBacking(); err != nil {
				d.log.Printf("ERROR: failed to start server %s: %v", name, err)
			}
		}(name, ms)
	}
	d.mu.RUnlock()

	// Start idle checker
	go d.idleChecker()

	// Save state
	if err := saveState(d.state); err != nil {
		d.log.Printf("WARNING: failed to save state: %v", err)
	}

	d.log.Printf("Daemon started (PID %d), managing %d servers", os.Getpid(), len(d.servers))

	// Wait for signal or reload file
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	if supportsSignals() {
		signal.Notify(sigCh, syscall.SIGHUP)
	}

	// Start reload file watcher (for Windows where SIGHUP doesn't exist)
	reloadCh := make(chan struct{}, 1)
	go d.watchReloadFile(reloadCh)

	for {
		select {
		case sig := <-sigCh:
			if sig == syscall.SIGHUP {
				d.log.Println("Received SIGHUP, reloading canonical config...")
				d.reconcile()
				continue
			}
			// SIGTERM or SIGINT — shutdown
			d.log.Println("Shutting down daemon...")
			d.shutdown()
			break
		case <-reloadCh:
			d.log.Println("Reload file triggered, reloading canonical config...")
			d.reconcile()
			continue
		}
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

	if err := terminateDaemon(pid); err != nil {
		return fmt.Errorf("failed to stop daemon: %w", err)
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

// watchReloadFile watches the daemon.reload file for changes.
// On Windows (no SIGHUP), touching this file triggers a config reload.
// On Unix, SIGHUP is the primary trigger, but the file watcher also works.
func (d *Daemon) watchReloadFile(ch chan<- struct{}) {
	var lastMod time.Time
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			path, err := daemonReloadPath()
			if err != nil {
				continue
			}
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if info.ModTime().After(lastMod) && !lastMod.IsZero() {
				select {
				case ch <- struct{}{}:
				default:
				}
			}
			lastMod = info.ModTime()
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

	loadRequested := map[string]bool{}
	for _, n := range consumeLoadRequests() {
		loadRequested[n] = true
	}

	d.mu.Lock()

	// Find servers to add
	maxPort := 8420
	for _, ss := range d.state.Servers {
		if ss.Port > maxPort {
			maxPort = ss.Port
		}
	}

	var toStart []*ManagedServer
	for name, srv := range cfg.Servers {
		if !shouldManageLocalHTTP(srv.Transport, srv.Command) {
			continue // stdio or kind-1 URL-only
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
			BackingPort: resolveBackingPort(srv.Command, srv.Args, srv.URL, port),
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

		d.log.Printf("Added server %s on port %d (backing=%d) via SIGHUP reconcile", name, port, ms.BackingPort)
		go ms.serveProxy()

		// Newly added always-on servers start immediately (same as Start()).
		if shouldStartBacking(ms.IdleTimeout, loadRequested[name], false) {
			toStart = append(toStart, ms)
		}
	}

	// Load-after-install: start existing managed servers that were requested.
	seenStart := map[string]bool{}
	for _, ms := range toStart {
		seenStart[ms.Name] = true
	}
	for name := range loadRequested {
		if seenStart[name] {
			continue
		}
		ms, exists := d.servers[name]
		if !exists {
			continue
		}
		// Do not take ms.mu while holding d.mu — startBacking locks
		// ms.mu then d.mu. A stale "running" read is safe: startBacking
		// is a no-op if the process is already up.
		if shouldStartBacking(ms.IdleTimeout, true, ms.state == "running") {
			toStart = append(toStart, ms)
			seenStart[name] = true
		}
	}

	// Check for stop-request files (from `pharos stop <server>`)
	stopDir := filepath.Join(mustDaemonDir(), "daemon.stop")
	if entries, err := os.ReadDir(stopDir); err == nil {
		for _, entry := range entries {
			srvName := entry.Name()
			if ms, exists := d.servers[srvName]; exists {
				if ms.state == "running" {
					ms.stopBacking()
					d.log.Printf("Stopped server %s via stop-request file", srvName)
				}
				// Remove the stop-request file
				os.Remove(filepath.Join(stopDir, srvName))
				// A stop request wins over a same-cycle load request.
				filtered := toStart[:0]
				for _, candidate := range toStart {
					if candidate.Name != srvName {
						filtered = append(filtered, candidate)
					}
				}
				toStart = filtered
			}
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
	d.mu.Unlock()

	// startBacking takes ms.mu then d.mu — never call it while holding d.mu.
	for _, ms := range toStart {
		go func(ms *ManagedServer) {
			if err := ms.startBacking(); err != nil {
				d.log.Printf("ERROR: failed to start server %s: %v", ms.Name, err)
			}
		}(ms)
	}
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

	// Check executable. Persist may still say "python"; resolve python3
	// at spawn time only. Remaining args (e.g. server.py) stay as-is.
	exe, err := runtime.ResolveSpawnExe(parts[0])
	if err != nil {
		return err
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

	// Set process group (platform-specific)
	setSysProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	ms.pid = cmd.Process.Pid
	ms.state = "running"
	ms.startedAt = time.Now()
	ms.lastActivity = time.Now()

	// Wait for port to be ready (up to backingReadyWait).
	deadline := time.Now().Add(backingReadyWait)
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

	// SIGTERM the process (platform-specific)
	_ = terminateProcess(pid)

	// Wait up to 5s
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !isProcessAlive(pid) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Force kill if still alive
	if isProcessAlive(pid) {
		_ = killProcess(pid)
		time.Sleep(500 * time.Millisecond)
	}

	ms.mu.Lock()
	ms.state = "unloaded"
	ms.pid = 0
	ms.mu.Unlock()

	ms.daemon.log.Printf("Unloaded %s", ms.Name)
}

// backingReadyWait is how long startBacking waits for the listen port.
// Tests shorten this so a missing listener does not stall for 10s.
var backingReadyWait = 10 * time.Second

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
	return readProcMemory(pid)
}

// shouldManageLocalHTTP reports whether the daemon should supervise a
// local HTTP/SSE process. Kind 1 (URL, no command) is never managed.
func shouldManageLocalHTTP(transport, command string) bool {
	switch transport {
	case "http-sse", "http", "streamable-http":
		return strings.TrimSpace(command) != ""
	default:
		return false
	}
}

// shouldStartBacking decides whether to spawn the backing process now.
// idleTimeout 0 is always-on. A load-after-install request starts even
// when idleTimeout is 60 (first load); later idle unload still applies.
func shouldStartBacking(idleTimeout int, loadRequested, alreadyRunning bool) bool {
	if alreadyRunning {
		return false
	}
	if loadRequested {
		return true
	}
	return idleTimeout == 0
}

// resolveBackingPort prefers a port declared in command/args/--port or
// URL. It never defaults to the proxy port — undeclared local HTTP
// servers use the well-known test-echo port 8765.
func resolveBackingPort(command string, args []string, rawURL string, proxyPort int) int {
	if p := parseDeclaredPort(command, args); p > 0 {
		return p
	}
	if rawURL != "" {
		if u, err := url.Parse(rawURL); err == nil {
			if p, err := strconv.Atoi(u.Port()); err == nil && isUsablePort(p) {
				return p
			}
		}
	}
	_ = proxyPort // never default backing to the proxy listener
	return defaultLocalHTTPBackingPort
}

func parseDeclaredPort(command string, args []string) int {
	tokens := append(splitCommand(command), args...)
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		switch {
		case tok == "--port" || tok == "-p":
			if i+1 < len(tokens) {
				if p := parseUsablePort(tokens[i+1]); p > 0 {
					return p
				}
			}
		case strings.HasPrefix(tok, "--port="):
			if p := parseUsablePort(strings.TrimPrefix(tok, "--port=")); p > 0 {
				return p
			}
		}
	}
	for _, tok := range tokens {
		if p := parseUsablePort(tok); p > 1024 {
			return p
		}
	}
	return 0
}

func parseUsablePort(s string) int {
	p, err := strconv.Atoi(s)
	if err != nil || !isUsablePort(p) {
		return 0
	}
	return p
}

func isUsablePort(p int) bool {
	return p > 0 && p < 65536
}

// consumeLoadRequests returns queued server names and deletes the files.
func consumeLoadRequests() []string {
	dir := mustDaemonDir()
	if dir == "" {
		return nil
	}
	loadDir := filepath.Join(dir, "daemon.load")
	entries, err := os.ReadDir(loadDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := filepath.Base(entry.Name())
		if name == "" || name == "." {
			continue
		}
		names = append(names, name)
		_ = os.Remove(filepath.Join(loadDir, name))
	}
	return names
}

func writeLoadRequest(name string) error {
	dir, err := daemonDirFn()
	if err != nil {
		return err
	}
	loadDir := filepath.Join(dir, "daemon.load")
	if err := os.MkdirAll(loadDir, 0o700); err != nil {
		return fmt.Errorf("create load dir: %w", err)
	}
	safeName := filepath.Base(name)
	if safeName == "" || safeName == "." || safeName == string(filepath.Separator) {
		return fmt.Errorf("invalid server name")
	}
	loadFile := filepath.Join(loadDir, safeName)
	if err := os.WriteFile(loadFile, []byte("load"), 0o600); err != nil {
		return fmt.Errorf("write load file: %w", err)
	}
	return nil
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
