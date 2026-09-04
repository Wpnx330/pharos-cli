package runtime

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// useTempHome points HOME and USERPROFILE at a per-test temp dir so
// ~/.pharos/run/ operations are isolated on every GOOS (os.UserHomeDir
// reads USERPROFILE on windows). Returns the temp dir.
func useTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

// runDirFor returns the expected ~/.pharos/run directory under the given home.
func runDirFor(home string) string {
	return filepath.Join(home, ".pharos", "run")
}

// --- PIDFile ---

func TestPIDFile_CreatesDirAndReturnsPath(t *testing.T) {
	home := useTempHome(t)

	got, err := PIDFile("myserver")
	if err != nil {
		t.Fatalf("PIDFile returned error: %v", err)
	}

	want := filepath.Join(runDirFor(home), "myserver.pid")
	if got != want {
		t.Errorf("PIDFile path = %q, want %q", got, want)
	}

	// Directory should have been created.
	info, err := os.Stat(runDirFor(home))
	if err != nil {
		t.Fatalf("run dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("run dir is not a directory")
	}
}

func TestPIDFile_DifferentNames(t *testing.T) {
	useTempHome(t)

	for _, name := range []string{"a", "server-1", "my_server"} {
		t.Run(name, func(t *testing.T) {
			got, err := PIDFile(name)
			if err != nil {
				t.Fatalf("PIDFile(%q) error: %v", name, err)
			}
			if !strings.HasSuffix(got, name+".pid") {
				t.Errorf("PIDFile(%q) = %q, want suffix %q", name, got, name+".pid")
			}
		})
	}
}

// --- ReadPID ---

func TestReadPID_NoFileReturnsZero(t *testing.T) {
	useTempHome(t)

	pid, err := ReadPID("nonexistent")
	if err != nil {
		t.Fatalf("ReadPID returned error for missing file: %v", err)
	}
	if pid != 0 {
		t.Errorf("ReadPID = %d, want 0 for missing file", pid)
	}
}

func TestReadPID_ValidPID(t *testing.T) {
	home := useTempHome(t)

	pidPath, err := PIDFile("svc")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidPath, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}

	pid, err := ReadPID("svc")
	if err != nil {
		t.Fatalf("ReadPID error: %v", err)
	}
	if pid != 12345 {
		t.Errorf("ReadPID = %d, want 12345", pid)
	}
	_ = home
}

func TestReadPID_WhitespaceTrimmed(t *testing.T) {
	useTempHome(t)

	pidPath, err := PIDFile("svc")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidPath, []byte("  6789  \n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pid, err := ReadPID("svc")
	if err != nil {
		t.Fatal(err)
	}
	if pid != 6789 {
		t.Errorf("ReadPID = %d, want 6789 (whitespace should be trimmed)", pid)
	}
}

func TestReadPID_InvalidContentReturnsZero(t *testing.T) {
	useTempHome(t)

	pidPath, err := PIDFile("svc")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidPath, []byte("not-a-number"), 0o644); err != nil {
		t.Fatal(err)
	}

	pid, err := ReadPID("svc")
	if err != nil {
		t.Fatalf("ReadPID should not error on invalid content: %v", err)
	}
	if pid != 0 {
		t.Errorf("ReadPID = %d, want 0 for invalid content", pid)
	}
}

// --- IsRunning ---

func TestIsRunning_ZeroAndNegativePID(t *testing.T) {
	for _, pid := range []int{0, -1, -100} {
		if IsRunning(pid) {
			t.Errorf("IsRunning(%d) = true, want false", pid)
		}
	}
}

func TestIsRunning_CurrentProcess(t *testing.T) {
	pid := os.Getpid()
	if !IsRunning(pid) {
		t.Errorf("IsRunning(self=%d) = false, want true", pid)
	}
}

func TestIsRunning_LikelyDeadPID(t *testing.T) {
	// A very high PID is extremely unlikely to exist on a fresh test process.
	if IsRunning(999999) {
		t.Errorf("IsRunning(999999) = true; expected false for likely-dead PID")
	}
}

// --- Start (background mode) ---

func TestStart_EmptyCommandReturnsError(t *testing.T) {
	useTempHome(t)

	_, err := Start(StartOptions{
		Name:    "svc",
		Command: "   ",
		WorkDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("Start with empty command should error")
	}
	if !strings.Contains(err.Error(), "no command") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStart_AlreadyRunningReturnsError(t *testing.T) {
	useTempHome(t)

	// Pre-write a PID file pointing at the current (alive) process.
	pidPath, err := PIDFile("svc")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = Start(StartOptions{
		Name:    "svc",
		Command: "sleep 10",
		WorkDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("Start should error when server already running")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStart_BackgroundStartsProcessAndWritesPID(t *testing.T) {
	skipIfHelpersMissing(t)
	home := useTempHome(t)
	work := t.TempDir()

	res, err := Start(StartOptions{
		Name:    "svc",
		Command: "sleep 30",
		WorkDir: work,
	})
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = Stop(StopOptions{Name: "svc", Force: true, Timeout: 1})
	})

	if res.PID <= 0 {
		t.Errorf("StartResult.PID = %d, want > 0", res.PID)
	}
	if res.Port != 0 {
		t.Errorf("StartResult.Port = %d, want 0 (no listen wait for sleep)", res.Port)
	}

	// PID file should exist with the returned PID.
	pid, err := ReadPID("svc")
	if err != nil {
		t.Fatal(err)
	}
	if pid != res.PID {
		t.Errorf("ReadPID = %d, want %d", pid, res.PID)
	}

	// Process should actually be running.
	if !IsRunning(res.PID) {
		t.Errorf("started process %d is not running", res.PID)
	}

	// Log file should have been created in the parent of WorkDir.
	logFile := filepath.Join(filepath.Dir(work), "svc.log")
	if _, err := os.Stat(logFile); err != nil {
		t.Errorf("log file not created at %s: %v", logFile, err)
	}
	_ = home
}

func TestStart_ImmediateExitReturnsErrorAndRemovesPID(t *testing.T) {
	skipIfHelpersMissing(t)
	useTempHome(t)
	work := t.TempDir()

	res, err := Start(StartOptions{
		Name:    "dead-on-arrival",
		Command: "false",
		WorkDir: work,
	})
	if err == nil {
		if res != nil {
			_ = Stop(StopOptions{Name: "dead-on-arrival", Force: true, Timeout: 1})
		}
		t.Fatal("Start of immediately-exiting command must return an error")
	}
	if res != nil {
		t.Errorf("StartResult = %+v, want nil on failure", res)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "exited") &&
		!strings.Contains(strings.ToLower(err.Error()), "died") {
		t.Errorf("error = %v, want it to mention the process exited", err)
	}

	pidPath, pathErr := PIDFile("dead-on-arrival")
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	if _, statErr := os.Stat(pidPath); !os.IsNotExist(statErr) {
		t.Errorf("PID file should be removed after immediate exit, stat err = %v", statErr)
	}
	pid, _ := ReadPID("dead-on-arrival")
	if pid != 0 {
		t.Errorf("ReadPID = %d, want 0 after failed start", pid)
	}
}

func TestStart_StaysUpThenStop(t *testing.T) {
	skipIfHelpersMissing(t)
	useTempHome(t)
	work := t.TempDir()

	res, err := Start(StartOptions{
		Name:    "keeper",
		Command: "sleep 30",
		WorkDir: work,
	})
	if err != nil {
		t.Fatalf("Start of long-lived process failed: %v", err)
	}
	if res == nil || res.PID <= 0 {
		t.Fatalf("StartResult = %+v, want a live PID", res)
	}
	if !IsRunning(res.PID) {
		t.Fatalf("started process %d is not running", res.PID)
	}
	if err := Stop(StopOptions{Name: "keeper", Force: true, Timeout: 1}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestStart_PortAlreadyInUseFailsBeforeLaunch(t *testing.T) {
	useTempHome(t)
	work := t.TempDir()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	res, err := Start(StartOptions{
		Name:    "port-taken",
		Command: "sleep 30",
		WorkDir: work,
		Port:    port,
	})
	if err == nil {
		if res != nil {
			_ = Stop(StopOptions{Name: "port-taken", Force: true, Timeout: 1})
		}
		t.Fatal("Start must fail when the requested port is already in use")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("port %d already in use", port)) {
		t.Errorf("error = %v, want 'port %d already in use'", err, port)
	}
	if res != nil {
		t.Errorf("StartResult = %+v, want nil when port is in use", res)
	}
	pid, _ := ReadPID("port-taken")
	if pid != 0 {
		t.Errorf("ReadPID = %d, want 0 (must not write a success PID)", pid)
	}
}

func TestStart_WaitsForPortThenSucceeds(t *testing.T) {
	useTempHome(t)
	work := t.TempDir()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	// Compile a tiny helper first so Start's listen wait is not racing `go run`.
	// windows exec.LookPath only resolves PATHEXT executables, so the
	// built helper carries a .exe suffix there.
	helper := filepath.Join(work, "listen.go")
	bin := filepath.Join(work, "listenbin")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	src := fmt.Sprintf(`package main
import ("net"; "time")
func main() {
	ln, err := net.Listen("tcp", "127.0.0.1:%d")
	if err != nil { panic(err) }
	defer ln.Close()
	time.Sleep(60 * time.Second)
}
`, port)
	if err := os.WriteFile(helper, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", bin, helper)
	if out, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("go build helper: %v\n%s", buildErr, out)
	}

	res, err := Start(StartOptions{
		Name:    "listener",
		Command: bin,
		WorkDir: work,
		Port:    port,
	})
	if err != nil {
		t.Fatalf("Start of listener failed: %v", err)
	}
	t.Cleanup(func() {
		_ = Stop(StopOptions{Name: "listener", Force: true, Timeout: 2})
	})
	if res.PID <= 0 {
		t.Errorf("StartResult.PID = %d, want > 0", res.PID)
	}
	if res.Port != port {
		t.Errorf("StartResult.Port = %d, want %d", res.Port, port)
	}
	if !isPortOpen(port) {
		t.Errorf("port %d is not accepting after successful Start", port)
	}
}

func TestStart_ProcessAliveButPortNeverOpensFails(t *testing.T) {
	useTempHome(t)
	work := t.TempDir()

	// Pick a free port that nothing will bind.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	res, err := Start(StartOptions{
		Name:    "no-listen",
		Command: "sleep 30",
		WorkDir: work,
		Port:    port,
	})
	if err == nil {
		if res != nil {
			_ = Stop(StopOptions{Name: "no-listen", Force: true, Timeout: 1})
		}
		t.Fatal("Start must fail when the process stays up but never listens")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "port") {
		t.Errorf("error = %v, want it to mention the port", err)
	}
	pid, _ := ReadPID("no-listen")
	if pid != 0 {
		t.Errorf("ReadPID = %d, want 0 after failed listen wait", pid)
	}
}

// --- Stop ---

func TestStop_NotRunningCleansStalePIDAndErrors(t *testing.T) {
	useTempHome(t)

	// Write a stale PID file pointing to a dead PID.
	pidPath, err := PIDFile("svc")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidPath, []byte("999999"), 0o644); err != nil {
		t.Fatal(err)
	}

	err = Stop(StopOptions{Name: "svc", Timeout: 1})
	if err == nil {
		t.Fatal("Stop on not-running server should error")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("unexpected error: %v", err)
	}

	// Stale PID file should have been removed.
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Errorf("stale PID file should have been removed, stat err = %v", err)
	}
}

func TestStop_NoPIDFileErrors(t *testing.T) {
	useTempHome(t)

	err := Stop(StopOptions{Name: "ghost", Timeout: 1})
	if err == nil {
		t.Fatal("Stop with no PID file should error")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestStop_ForceStopCleansPIDFile verifies that Stop with Force:true returns
// nil and removes the PID file. We do NOT assert IsRunning(pid)==false here
// because the started process is a direct child of the test process and
// becomes a zombie after being killed (nobody calls Wait to reap it); signal 0
// to a zombie succeeds, so IsRunning would still report true. In real usage
// the CLI exits after Stop, so init reaps the zombie. That OS-level reaping
// quirk is outside the scope of the runtime package's PID-file contract.
func TestStop_ForceStopCleansPIDFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group signals not supported on windows")
	}
	useTempHome(t)

	work := t.TempDir()
	if _, err := Start(StartOptions{
		Name:    "svc",
		Command: "sleep 60",
		WorkDir: work,
	}); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if err := Stop(StopOptions{Name: "svc", Force: true, Timeout: 1}); err != nil {
		t.Fatalf("Stop(force) failed: %v", err)
	}

	pidPath, err := PIDFile("svc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Errorf("PID file should be removed after Stop, stat err = %v", err)
	}
}

// TestStop_GracefulTimeoutOnIgnoringProcess exercises the non-force timeout
// path: a process that ignores SIGTERM must cause Stop to return the
// "did not stop" error. Uses bash with an ignored TERM trap so the process
// genuinely stays alive (not a zombie artifact).
func TestStop_GracefulTimeoutOnIgnoringProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group signals not supported on windows")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	useTempHome(t)

	work := t.TempDir()
	// Start splits the command with Fields, so quotes never reach bash.
	// Write a real script that ignores SIGTERM instead.
	script := filepath.Join(work, "ignore-term.sh")
	if err := os.WriteFile(script, []byte("#!/bin/bash\ntrap '' TERM\nwhile true; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(StartOptions{
		Name:    "svc",
		Command: script,
		WorkDir: work,
	}); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = Stop(StopOptions{Name: "svc", Force: true, Timeout: 1})
	})

	err := Stop(StopOptions{Name: "svc", Timeout: 2})
	if err == nil {
		t.Fatal("Stop should error when process does not stop within timeout")
	}
	if !strings.Contains(err.Error(), "did not stop") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- StopAll ---

func TestStopAll_NoRunDirReturnsNil(t *testing.T) {
	useTempHome(t)

	stopped, err := StopAll(false, 1)
	if err != nil {
		t.Fatalf("StopAll with no run dir should not error: %v", err)
	}
	if stopped != nil {
		t.Errorf("StopAll = %v, want nil", stopped)
	}
}

func TestStopAll_StopsRunningServers(t *testing.T) {
	useTempHome(t)

	work := t.TempDir()
	if _, err := Start(StartOptions{Name: "a", Command: "sleep 30", WorkDir: work}); err != nil {
		t.Fatalf("Start a: %v", err)
	}
	if _, err := Start(StartOptions{Name: "b", Command: "sleep 30", WorkDir: work}); err != nil {
		t.Fatalf("Start b: %v", err)
	}
	t.Cleanup(func() {
		_ = Stop(StopOptions{Name: "a", Force: true, Timeout: 1})
		_ = Stop(StopOptions{Name: "b", Force: true, Timeout: 1})
	})

	stopped, err := StopAll(true, 2)
	if err != nil {
		t.Fatalf("StopAll error: %v", err)
	}
	if len(stopped) != 2 {
		t.Errorf("StopAll stopped %d servers, want 2 (%v)", len(stopped), stopped)
	}
}

func TestStopAll_IgnoresNonPIDFiles(t *testing.T) {
	home := useTempHome(t)

	// Create run dir with a non-.pid file.
	if err := os.MkdirAll(runDirFor(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDirFor(home), "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	stopped, err := StopAll(false, 1)
	if err != nil {
		t.Fatalf("StopAll error: %v", err)
	}
	if len(stopped) != 0 {
		t.Errorf("StopAll = %v, want empty", stopped)
	}
}

// --- ProbeStatus ---

func TestProbeStatus_RunningProcess(t *testing.T) {
	useTempHome(t)

	work := t.TempDir()
	res, err := Start(StartOptions{Name: "svc", Command: "sleep 30", WorkDir: work})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = Stop(StopOptions{Name: "svc", Force: true, Timeout: 1})
	})

	st := ProbeStatus("svc", 0)
	if !st.Running {
		t.Error("ProbeStatus.Running = false, want true")
	}
	if st.PID != res.PID {
		t.Errorf("ProbeStatus.PID = %d, want %d", st.PID, res.PID)
	}
}

func TestProbeStatus_DeadPIDCleanedUp(t *testing.T) {
	useTempHome(t)

	pidPath, err := PIDFile("svc")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidPath, []byte("999999"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := ProbeStatus("svc", 0)
	if st.Running {
		t.Error("ProbeStatus.Running = true, want false for dead PID")
	}
	// Stale PID file should be removed.
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Errorf("stale PID file should be removed, stat err = %v", err)
	}
}

func TestProbeStatus_NotRunningNoPort(t *testing.T) {
	useTempHome(t)

	st := ProbeStatus("ghost", 0)
	if st.Running {
		t.Error("ProbeStatus.Running = true, want false")
	}
}

func TestProbeStatus_PortOpenNoPID(t *testing.T) {
	useTempHome(t)

	// Start a local listener on a free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	port := ln.Addr().(*net.TCPAddr).Port
	st := ProbeStatus("ghost", port)
	if !st.Running {
		t.Error("ProbeStatus.Running = false, want true (port is open)")
	}
	if st.Port != port {
		t.Errorf("ProbeStatus.Port = %d, want %d", st.Port, port)
	}
}

func TestProbeStatus_PortClosedNoPID(t *testing.T) {
	useTempHome(t)

	// Pick a free port then close it so nothing is listening.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	st := ProbeStatus("ghost", port)
	if st.Running {
		t.Error("ProbeStatus.Running = true, want false (port closed, no PID)")
	}
}

// --- isPortOpen ---

func TestIsPortOpen_OpenPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	port := ln.Addr().(*net.TCPAddr).Port
	if !isPortOpen(port) {
		t.Errorf("isPortOpen(%d) = false, want true", port)
	}
}

func TestIsPortOpen_ClosedPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if isPortOpen(port) {
		t.Errorf("isPortOpen(%d) = true; expected false for closed port", port)
	}
}

// --- readMemory / readUptime (Linux /proc only) ---

func TestReadMemory_CurrentProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("readMemory only works on Linux")
	}
	mem := readMemory(os.Getpid())
	if mem <= 0 {
		t.Errorf("readMemory(self) = %d, want > 0", mem)
	}
}

func TestReadMemory_NonexistentPID(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("readMemory only works on Linux")
	}
	mem := readMemory(999999)
	if mem != 0 {
		t.Errorf("readMemory(999999) = %d, want 0", mem)
	}
}

func TestReadUptime_CurrentProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("readUptime only works on Linux")
	}
	up := readUptime(os.Getpid())
	if up == "" {
		t.Error("readUptime(self) = empty, want non-empty")
	}
}

func TestReadUptime_NonexistentPID(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("readUptime only works on Linux")
	}
	up := readUptime(999999)
	if up != "" {
		t.Errorf("readUptime(999999) = %q, want empty", up)
	}
}

// --- formatDuration ---

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"zero", 0, "0s"},
		{"seconds", 45 * time.Second, "45s"},
		{"just under a minute", 59 * time.Second, "59s"},
		{"one minute", 60 * time.Second, "1m0s"},
		{"minutes and seconds", 2*time.Minute + 30*time.Second, "2m30s"},
		{"just under an hour", 59*time.Minute + 59*time.Second, "59m59s"},
		{"one hour", time.Hour, "1h0m"},
		{"hours and minutes", 3*time.Hour + 15*time.Minute, "3h15m"},
		{"many hours", 26 * time.Hour, "26h0m"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatDuration(tc.d); got != tc.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}

// --- RunningPIDs ---

func TestRunningPIDs_NoDir(t *testing.T) {
	useTempHome(t)

	m := RunningPIDs()
	if m != nil {
		t.Errorf("RunningPIDs with no dir = %v, want nil", m)
	}
}

func TestRunningPIDs_ReturnsAliveOnly(t *testing.T) {
	skipIfHelpersMissing(t)
	useTempHome(t)

	work := t.TempDir()
	res, err := Start(StartOptions{Name: "alive", Command: "sleep 30", WorkDir: work})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = Stop(StopOptions{Name: "alive", Force: true, Timeout: 1})
	})

	// Also write a stale PID file that should NOT appear.
	pidPath, err := PIDFile("dead")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidPath, []byte("999999"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := RunningPIDs()
	if m == nil {
		t.Fatal("RunningPIDs = nil, want map")
	}
	pid, ok := m["alive"]
	if !ok {
		t.Error("RunningPIDs missing 'alive'")
	}
	if pid != res.PID {
		t.Errorf("RunningPIDs['alive'] = %d, want %d", pid, res.PID)
	}
	if _, ok := m["dead"]; ok {
		t.Error("RunningPIDs should not include dead PID")
	}
}

// --- ExtractPort ---

func TestExtractPort(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     int
	}{
		// URL-form inputs: SplitHostPort is called on the host:port segment.
		// A trailing path makes SplitHostPort fail, so the port is NOT
		// extracted — this documents the current (limited) behavior.
		{"http with port and path", "http://localhost:8080/sse", 0},
		{"https with port and path", "https://example.com:9000/sse", 0},
		{"http with ip, port, no path", "http://127.0.0.1:5555", 5555},
		{"http default no port in url", "http://localhost/sse", 0},
		// Bare / command-form inputs: the first bare integer token wins.
		{"bare number", "3000", 3000},
		{"command with port arg", "python server.py --port 4200", 4200},
		{"empty string", "", 0},
		{"no port anywhere", "python server.py", 0},
		{"port out of range", "99999", 0},
		{"zero port", "0", 0},
		{"negative", "-5", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractPort(tc.endpoint)
			if got != tc.want {
				t.Errorf("ExtractPort(%q) = %d, want %d", tc.endpoint, got, tc.want)
			}
		})
	}
}

// --- WritePIDFileJSON ---

func TestWritePIDFileJSON_WritesBothFiles(t *testing.T) {
	home := useTempHome(t)

	if err := WritePIDFileJSON("svc", 12345); err != nil {
		t.Fatalf("WritePIDFileJSON: %v", err)
	}

	// Plain PID file should contain the integer.
	pidPath, err := PIDFile("svc")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("PID file not written: %v", err)
	}
	if strings.TrimSpace(string(raw)) != "12345" {
		t.Errorf("PID file content = %q, want 12345", string(raw))
	}

	// JSON sidecar should parse into PIDFileData.
	jsonPath := pidPath + ".json"
	jraw, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("JSON file not written: %v", err)
	}
	var data PIDFileData
	if err := json.Unmarshal(jraw, &data); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if data.PID != 12345 {
		t.Errorf("JSON PID = %d, want 12345", data.PID)
	}
	if data.Name != "svc" {
		t.Errorf("JSON Name = %q, want svc", data.Name)
	}
	if data.StartedAt.IsZero() {
		t.Error("JSON StartedAt is zero, want a timestamp")
	}
	_ = home
}

func TestWritePIDFileJSON_PIDFileReadableByReadPID(t *testing.T) {
	useTempHome(t)

	if err := WritePIDFileJSON("svc", 999); err != nil {
		t.Fatal(err)
	}
	pid, err := ReadPID("svc")
	if err != nil {
		t.Fatal(err)
	}
	if pid != 999 {
		t.Errorf("ReadPID after WritePIDFileJSON = %d, want 999", pid)
	}
}

// --- ProcessStatus JSON ---

func TestProcessStatus_JSONRoundTrip(t *testing.T) {
	s := ProcessStatus{
		Running: true,
		PID:     4242,
		Port:    8080,
		Memory:  12345678,
		Uptime:  "1h2m",
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got ProcessStatus
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got != s {
		t.Errorf("JSON round trip mismatch: got %+v, want %+v", got, s)
	}
}

func TestProcessStatus_JSONOmitsZeroFields(t *testing.T) {
	s := ProcessStatus{Running: false}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	str := string(raw)
	for _, field := range []string{`"pid"`, `"port"`, `"memory"`, `"uptime"`} {
		if strings.Contains(str, field) {
			t.Errorf("zero-value JSON should omit %s, got %s", field, str)
		}
	}
}

// --- Start foreground mode (lightweight) ---

func TestStart_ForegroundRunsAndExits(t *testing.T) {
	skipIfHelpersMissing(t)
	useTempHome(t)

	// A command that exits immediately on its own.
	res, err := Start(StartOptions{
		Name:       "svc",
		Command:    "true",
		WorkDir:    t.TempDir(),
		Foreground: true,
	})
	if err != nil {
		t.Fatalf("Start foreground failed: %v", err)
	}
	if res.PID <= 0 {
		t.Errorf("StartResult.PID = %d, want > 0", res.PID)
	}
}

func TestStart_ForegroundFailureReturnsError(t *testing.T) {
	skipIfHelpersMissing(t)
	useTempHome(t)

	// 'false' exits with status 1.
	_, err := Start(StartOptions{
		Name:       "svc",
		Command:    "false",
		WorkDir:    t.TempDir(),
		Foreground: true,
	})
	if err == nil {
		t.Fatal("Start foreground with failing command should error")
	}
	if !strings.Contains(err.Error(), "exited") {
		t.Errorf("unexpected error: %v", err)
	}
}

// skipIfHelpersMissing skips the test if any of the external helper commands
// (sleep, true, false) required by the runtime tests are not on PATH.
func skipIfHelpersMissing(t *testing.T) {
	t.Helper()
	for _, cmd := range []string{"sleep", "true", "false"} {
		if _, err := exec.LookPath(cmd); err != nil {
			t.Skipf("required helper %q not on PATH: %v", cmd, err)
		}
	}
}

func ExampleExtractPort() {
	fmt.Println(ExtractPort("3000"))
	// Output: 3000
}

// --- spawn-time python → python3 ---

func writePathExe(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

const spawnArgLogger = "#!/bin/sh\n{\n  printf 'exe=%s\\n' \"$0\"\n  for a in \"$@\"; do printf 'arg=%s\\n' \"$a\"; done\n} > \"$PHAROS_SPAWN_LOG\"\nexec /bin/sleep 30\n"

// spawnStubName returns the file name a PATH stub must have so
// exec.LookPath resolves it on the current GOOS: bare name on unix,
// a PATHEXT suffix (.bat) on windows — the stub is only ever looked
// up, never executed, by ResolveSpawnExe.
func spawnStubName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".bat"
	}
	return base
}

func TestResolveSpawnExe_PythonMissingUsesPython3(t *testing.T) {
	dir := t.TempDir()
	stub := spawnStubName("python3")
	writePathExe(t, dir, stub, "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir)

	got, err := ResolveSpawnExe("python")
	if err != nil {
		t.Fatalf("ResolveSpawnExe: %v", err)
	}
	want := filepath.Join(dir, stub)
	if got != want {
		t.Fatalf("ResolveSpawnExe = %q, want %q", got, want)
	}
}

func TestResolveSpawnExe_PythonPresentKeepsPython(t *testing.T) {
	dir := t.TempDir()
	stub := spawnStubName("python")
	writePathExe(t, dir, stub, "#!/bin/sh\nexit 0\n")
	writePathExe(t, dir, spawnStubName("python3"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir)

	got, err := ResolveSpawnExe("python")
	if err != nil {
		t.Fatalf("ResolveSpawnExe: %v", err)
	}
	want := filepath.Join(dir, stub)
	if got != want {
		t.Fatalf("ResolveSpawnExe = %q, want %q when python is on PATH", got, want)
	}
}

func TestResolveSpawnExe_NeitherPythonErrorsWithoutAptHint(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := ResolveSpawnExe("python")
	if err == nil {
		t.Fatal("expected error when neither python nor python3 is on PATH")
	}
	msg := err.Error()
	if !strings.Contains(msg, "python") {
		t.Errorf("error should mention python: %v", err)
	}
	if !strings.Contains(msg, "python3") {
		t.Errorf("error should mention python3 as the missing interpreter: %v", err)
	}
	if strings.Contains(msg, "python-is-python3") || strings.Contains(msg, "apt install") {
		t.Errorf("must not tell the user to apt-install python-is-python3: %v", err)
	}
}

func TestResolveSpawnExe_DoesNotRewriteOtherRuntimes(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, exe := range []string{"npx", "uvx", "docker"} {
		got, err := ResolveSpawnExe(exe)
		if err == nil {
			t.Fatalf("%s: expected missing-exe error, got %q", exe, got)
		}
		if got != "" {
			t.Errorf("%s: got %q on error, want empty", exe, got)
		}
		if strings.Contains(err.Error(), "python3") {
			t.Errorf("%s: must not fall back to python3: %v", exe, err)
		}
	}
}

func TestStart_PythonMissingUsesPython3KeepsArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH exe stubs are POSIX scripts")
	}
	useTempHome(t)
	work := t.TempDir()
	bin := t.TempDir()
	writePathExe(t, bin, "python3", spawnArgLogger)
	t.Setenv("PATH", bin)

	logPath := filepath.Join(work, "spawned.txt")
	res, err := Start(StartOptions{
		Name:    "echo",
		Command: "python server.py",
		WorkDir: work,
		Env:     []string{"PHAROS_SPAWN_LOG=" + logPath},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = Stop(StopOptions{Name: "echo", Force: true, Timeout: 1})
	})
	if res.PID <= 0 {
		t.Fatalf("PID = %d, want > 0", res.PID)
	}

	body, err := waitFile(logPath, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "exe="+filepath.Join(bin, "python3")) {
		t.Errorf("spawned exe = %q, want python3", body)
	}
	if !strings.Contains(body, "arg=server.py") {
		t.Errorf("args lost server.py: %q", body)
	}
	if strings.Contains(body, "test-echo-server") {
		t.Errorf("must not rewrite args to package name: %q", body)
	}
}

func TestStart_PythonPresentKeepsPython(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH exe stubs are POSIX scripts")
	}
	useTempHome(t)
	work := t.TempDir()
	bin := t.TempDir()
	writePathExe(t, bin, "python", spawnArgLogger)
	writePathExe(t, bin, "python3", "#!/bin/sh\necho unexpected-python3 > \"$PHAROS_SPAWN_LOG\"\nexec /bin/sleep 30\n")
	t.Setenv("PATH", bin)

	logPath := filepath.Join(work, "spawned.txt")
	if _, err := Start(StartOptions{
		Name:    "echo",
		Command: "python server.py",
		WorkDir: work,
		Env:     []string{"PHAROS_SPAWN_LOG=" + logPath},
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = Stop(StopOptions{Name: "echo", Force: true, Timeout: 1})
	})

	body, err := waitFile(logPath, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "exe="+filepath.Join(bin, "python")) {
		t.Errorf("spawned exe = %q, want python", body)
	}
	if strings.Contains(body, "unexpected-python3") || strings.Contains(body, filepath.Join(bin, "python3")) {
		t.Errorf("must not substitute python3 when python is on PATH: %q", body)
	}
	if !strings.Contains(body, "arg=server.py") {
		t.Errorf("args lost server.py: %q", body)
	}
}

func waitFile(path string, d time.Duration) (string, error) {
	deadline := time.Now().Add(d)
	var last error
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil && len(b) > 0 {
			return string(b), nil
		}
		last = err
		time.Sleep(20 * time.Millisecond)
	}
	if last == nil {
		last = fmt.Errorf("timeout waiting for %s", path)
	}
	return "", last
}
