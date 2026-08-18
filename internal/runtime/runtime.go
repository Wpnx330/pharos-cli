// Package runtime provides process lifecycle management for locally
// installed MCP servers — start, stop, status probing, and PID tracking.
package runtime

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// PIDFile is the path where pharos stores PID files for running servers.
// ~/.pharos/run/<name>.pid
func PIDFile(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".pharos", "run")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".pid"), nil
}

// ProcessStatus represents the runtime state of an MCP server.
type ProcessStatus struct {
	Running bool   `json:"running"`
	PID     int    `json:"pid,omitempty"`
	Port    int    `json:"port,omitempty"`
	Memory  int64  `json:"memory,omitempty"` // RSS in bytes
	Uptime  string `json:"uptime,omitempty"`
}

// ReadPID reads the PID from the PID file, returns 0 if not found.
func ReadPID(name string) (int, error) {
	pidPath, err := PIDFile(name)
	if err != nil {
		return 0, err
	}
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, nil // no PID file = not running
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, nil
	}
	return pid, nil
}

// IsRunning checks whether a process with the given PID is alive.
func IsRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	return procExists(pid)
}

// StartOptions configures how a server is started.
type StartOptions struct {
	Name       string
	Command    string   // e.g. "python server.py"
	WorkDir    string   // e.g. ~/.pharos/store/test-echo-server/0.1.0
	Env        []string // additional env vars as KEY=VALUE
	Port       int      // override port (http-sse only)
	Foreground bool     // run in foreground (block terminal)
}

// StartResult holds the outcome of starting a server.
type StartResult struct {
	PID  int
	Port int
}

// Start launches an MCP server process.
func Start(opts StartOptions) (*StartResult, error) {
	// Check if already running
	pid, _ := ReadPID(opts.Name)
	if pid > 0 && IsRunning(pid) {
		return nil, fmt.Errorf("server %q is already running (PID %d); use 'pharos stop' first", opts.Name, pid)
	}

	// Parse the command into program + args
	parts := strings.Fields(opts.Command)
	if len(parts) == 0 {
		return nil, fmt.Errorf("no command specified in manifest")
	}

	// Verify the executable exists on PATH. "python" may resolve to
	// python3 at spawn time; remaining args are never rewritten.
	exe, err := ResolveSpawnExe(parts[0])
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(exe, parts[1:]...)
	cmd.Dir = opts.WorkDir

	// Inherit current env + add extra vars
	cmd.Env = append(os.Environ(), opts.Env...)

	if opts.Foreground {
		// Stream stdout/stderr to terminal
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("server exited: %w", err)
		}
		return &StartResult{PID: cmd.Process.Pid}, nil
	}

	// Refuse to start if the declared listen port is already taken.
	// Doing this before exec avoids writing a PID file for a process that
	// cannot bind, and avoids printing a false "Started".
	if opts.Port > 0 && isPortOpen(opts.Port) {
		return nil, fmt.Errorf("port %d already in use", opts.Port)
	}

	// Background mode: redirect stdout/stderr to log files
	logDir := filepath.Dir(opts.WorkDir)
	logFile := filepath.Join(logDir, opts.Name+".log")
	lf, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("create log file: %w", err)
	}
	cmd.Stdout = lf
	cmd.Stderr = lf

	// Set process group so we can kill the whole tree
	setProcGroup(cmd)

	if err := cmd.Start(); err != nil {
		lf.Close()
		return nil, fmt.Errorf("failed to start: %w", err)
	}
	lf.Close()

	// Reap the child so a crash does not leave a zombie that still
	// answers signal 0 (IsRunning would otherwise lie).
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	pid = cmd.Process.Pid

	// Write PID file
	pidPath, err := PIDFile(opts.Name)
	if err != nil {
		abandonStart(pid, "", waitCh)
		return nil, err
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		abandonStart(pid, "", waitCh)
		return nil, fmt.Errorf("write PID file: %w", err)
	}

	if opts.Port > 0 {
		if err := waitForListen(pid, opts.Port, waitCh, startListenWait); err != nil {
			abandonStart(pid, pidPath, waitCh)
			return nil, err
		}
	} else if err := waitForAlive(pid, opts.Name, waitCh, startAliveWait); err != nil {
		abandonStart(pid, pidPath, waitCh)
		return nil, err
	}

	return &StartResult{PID: pid, Port: opts.Port}, nil
}

const (
	startAliveWait    = time.Second
	startListenWait   = 5 * time.Second
	startPollInterval = 50 * time.Millisecond
)

func waitForAlive(pid int, name string, waitCh <-chan error, d time.Duration) error {
	deadline := time.Now().Add(d)
	for {
		if err := startWaitErr(waitCh); err != nil {
			return fmt.Errorf("server %q exited immediately: %w", name, err)
		}
		if !IsRunning(pid) {
			return fmt.Errorf("server %q exited immediately", name)
		}
		if !time.Now().Before(deadline) {
			return nil
		}
		time.Sleep(startPollInterval)
	}
}

func waitForListen(pid, port int, waitCh <-chan error, d time.Duration) error {
	deadline := time.Now().Add(d)
	for {
		if err := startWaitErr(waitCh); err != nil {
			return fmt.Errorf("process exited before port %d accepted connections: %w", port, err)
		}
		if !IsRunning(pid) {
			return fmt.Errorf("process exited before port %d accepted connections", port)
		}
		if isPortOpen(port) {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("timed out waiting for port %d (127.0.0.1) to accept connections", port)
		}
		time.Sleep(startPollInterval)
	}
}

func startWaitErr(waitCh <-chan error) error {
	select {
	case err := <-waitCh:
		if err != nil {
			return err
		}
		return fmt.Errorf("process exited")
	default:
		return nil
	}
}

// abandonStart kills a still-running child (process group) and removes
// the PID file so a failed start never looks like success.
func abandonStart(pid int, pidPath string, waitCh <-chan error) {
	if pid > 0 {
		_ = killProc(pid)
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			select {
			case <-waitCh:
				deadline = time.Time{}
			default:
				if !IsRunning(pid) {
					deadline = time.Time{}
				} else {
					time.Sleep(20 * time.Millisecond)
				}
			}
		}
	}
	if pidPath != "" {
		_ = os.Remove(pidPath)
	}
}

// StopOptions configures how a server is stopped.
type StopOptions struct {
	Name    string
	Force   bool // SIGKILL after timeout
	Timeout int  // seconds to wait for graceful shutdown
}

// Stop terminates a running MCP server.
func Stop(opts StopOptions) error {
	pid, err := ReadPID(opts.Name)
	if err != nil {
		return fmt.Errorf("read PID: %w", err)
	}
	if pid == 0 || !IsRunning(pid) {
		// Clean up stale PID file if it exists
		pidPath, _ := PIDFile(opts.Name)
		os.Remove(pidPath)
		return fmt.Errorf("server %q is not running", opts.Name)
	}

	// Send SIGTERM to the process group
	termProc(pid)

	// Wait for it to exit
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5
	}
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	for time.Now().Before(deadline) {
		if !IsRunning(pid) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Force kill if still running
	if IsRunning(pid) {
		if !opts.Force {
			return fmt.Errorf("server %q did not stop within %ds; use --force to SIGKILL", opts.Name, timeout)
		}
		killProc(pid)
		// Wait a moment for cleanup
		time.Sleep(500 * time.Millisecond)
	}

	// Clean up PID file
	pidPath, _ := PIDFile(opts.Name)
	os.Remove(pidPath)

	return nil
}

// StopAll stops all running Pharos-managed servers.
func StopAll(force bool, timeout int) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	runDir := filepath.Join(home, ".pharos", "run")
	entries, err := os.ReadDir(runDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var stopped []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pid") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".pid")
		if err := Stop(StopOptions{Name: name, Force: force, Timeout: timeout}); err == nil {
			stopped = append(stopped, name)
		}
	}
	return stopped, nil
}

// ProbeStatus checks the runtime status of an installed server.
func ProbeStatus(name string, port int) ProcessStatus {
	status := ProcessStatus{}

	pid, _ := ReadPID(name)
	if pid > 0 && IsRunning(pid) {
		status.Running = true
		status.PID = pid
		status.Port = port
		status.Memory = readMemory(pid)
		status.Uptime = readUptime(pid)
		return status
	}

	// If there's a PID file but process is dead, clean it up
	if pid > 0 {
		pidPath, _ := PIDFile(name)
		os.Remove(pidPath)
	}

	// For http-sse servers, try probing the port even without a PID file
	if port > 0 {
		if isPortOpen(port) {
			status.Running = true
			status.Port = port
		}
	}

	return status
}

// isPortOpen checks if a TCP port is accepting connections.
func isPortOpen(port int) bool {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// readMemory reads RSS (Resident Set Size) from /proc/<pid>/status on Linux.
func readMemory(pid int) int64 {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			// Format: "VmRSS:\t 12345 kB"
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, err := strconv.ParseInt(fields[1], 10, 64)
				if err == nil {
					return kb * 1024 // convert to bytes
				}
			}
		}
	}
	return 0
}

// readUptime reads the process start time and computes elapsed duration.
func readUptime(pid int) string {
	// Read /proc/<pid>/stat — field 22 is starttime in clock ticks
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(data))
	if len(fields) < 22 {
		return ""
	}
	starttime, err := strconv.ParseInt(fields[21], 10, 64)
	if err != nil {
		return ""
	}

	// Read uptime (system boot time)
	uptimeData, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return ""
	}
	uptimeFields := strings.Fields(string(uptimeData))
	if len(uptimeFields) == 0 {
		return ""
	}
	uptimeSec, err := strconv.ParseFloat(uptimeFields[0], 64)
	if err != nil {
		return ""
	}

	// Clock ticks per second is usually 100
	const clkTck = 100.0
	startSec := float64(starttime) / clkTck
	elapsed := uptimeSec - startSec
	if elapsed < 0 {
		return ""
	}

	return formatDuration(time.Duration(elapsed * float64(time.Second)))
}

// formatDuration formats a duration as a human-readable string.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

// RunningPIDs returns the names of all servers with active PID files.
func RunningPIDs() map[string]int {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	runDir := filepath.Join(home, ".pharos", "run")
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return nil
	}
	result := make(map[string]int)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pid") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".pid")
		pid, _ := ReadPID(name)
		if pid > 0 && IsRunning(pid) {
			result[name] = pid
		}
	}
	return result
}

// ExtractPort parses a port number from a manifest endpoint URL or command string.
func ExtractPort(endpoint string) int {
	// Try parsing as URL first
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		_, portStr, err := net.SplitHostPort(endpoint[strings.Index(endpoint, "://")+3:])
		if err == nil {
			p, _ := strconv.Atoi(portStr)
			return p
		}
	}
	// Try finding a --port flag or :PORT pattern
	for _, part := range strings.Fields(endpoint) {
		if p, err := strconv.Atoi(part); err == nil && p > 0 && p < 65536 {
			return p
		}
	}
	return 0
}

// PIDFileData is written to disk to track running servers (for JSON debugging).
type PIDFileData struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"startedAt"`
	Name      string    `json:"name"`
}

// WritePIDFileJSON writes a richer PID file with metadata.
func WritePIDFileJSON(name string, pid int) error {
	pidPath, err := PIDFile(name)
	if err != nil {
		return err
	}
	data := PIDFileData{
		PID:       pid,
		StartedAt: time.Now(),
		Name:      name,
	}
	raw, _ := json.Marshal(data)
	// Write PID as plain integer for simple parsing, but also keep JSON nearby
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return err
	}
	jsonPath := pidPath + ".json"
	return os.WriteFile(jsonPath, raw, 0o644)
}

// ResolveSpawnExe returns the executable name to exec after a PATH lookup.
// If the first argv token is exactly "python" and LookPath("python") fails
// but LookPath("python3") succeeds, python3 is used. Remaining args are
// not part of this function and must be left unchanged by the caller.
// npx, uvx, docker, and any other token are never rewritten.
func ResolveSpawnExe(exe string) (string, error) {
	if exe == "" {
		return "", fmt.Errorf("empty executable")
	}
	if path, err := exec.LookPath(exe); err == nil {
		return path, nil
	}
	if exe == "python" {
		if path, err := exec.LookPath("python3"); err == nil {
			return path, nil
		}
		return "", missingExeError(exe)
	}
	return "", missingExeError(exe)
}

func missingExeError(exe string) error {
	hint := executableHint(exe)
	if hint != "" {
		return fmt.Errorf("executable %q not found in $PATH: %s", exe, hint)
	}
	return fmt.Errorf("executable %q not found in $PATH", exe)
}

// ExecutableHint returns a user-facing hint for common missing executables.
// Spawn-time python→python3 substitution happens in ResolveSpawnExe; this
// hint is only used when neither interpreter is on PATH. Exported so cmd
// packages can use it for pre-install checks.
var executableHints = map[string]string{
	"python": "install Python 3 so python3 is on PATH",
	"node":   "install Node.js (Ubuntu/Debian: sudo apt install nodejs npm)",
	"npm":    "install npm (Ubuntu/Debian: sudo apt install npm)",
	"npx":    "install npx (Ubuntu/Debian: sudo apt install npm)",
	"uv":     "install uv (curl -LsSf https://astral.sh/uv/install.sh | sh)",
	"uvx":    "install uv (curl -LsSf https://astral.sh/uv/install.sh | sh)",
	"docker": "install Docker (see https://docs.docker.com/engine/install/)",
}

func ExecutableHint(name string) string {
	return executableHints[name]
}

// executableHint is an unexported alias for use within the runtime package.
func executableHint(name string) string {
	return ExecutableHint(name)
}
