package procs

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

func TestAliveSelf(t *testing.T) {
	self := os.Getpid()
	if !Alive(self) {
		t.Fatalf("Alive(%d) = false, want true for the current process", self)
	}
}

func TestAliveInvalidPIDs(t *testing.T) {
	for _, pid := range []int{0, -1, -42} {
		if Alive(pid) {
			t.Errorf("Alive(%d) = true, want false for non-positive PID", pid)
		}
	}
}

func TestAliveExitedProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows handle semantics can report a freshly exited process as alive")
	}
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn a process to probe liveness: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Skipf("wait for probe process failed: %v", err)
	}
	if Alive(cmd.Process.Pid) {
		t.Errorf("Alive(%d) = true for a reaped exited process, want false", cmd.Process.Pid)
	}
}

func TestRSSSelf(t *testing.T) {
	self := os.Getpid()
	got, ok := RSS(self)
	if !ok {
		if runtime.GOOS == "linux" {
			t.Fatalf("RSS(%d) probe failed on Linux — /proc/<pid>/status must be readable", self)
		}
		t.Skipf("RSS probe unavailable on %s in this environment", runtime.GOOS)
	}
	if got <= 0 {
		t.Fatalf("RSS(%d) = %d, want > 0", self, got)
	}
}

func TestRSSInvalidPID(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if _, ok := RSS(pid); ok {
			t.Errorf("RSS(%d) reported ok, want false for non-positive PID", pid)
		}
	}
}
