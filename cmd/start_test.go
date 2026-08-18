package cmd

import (
	"net"
	"strings"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/install"
	"github.com/Wpnx330/pharos-cli/internal/runtime"
)

func TestPlanStartRefusesRemoteURLKind1(t *testing.T) {
	pkg := install.InstalledPackage{
		Name:      "com.invokera/world-time",
		Version:   "1.0.0",
		Transport: "streamable-http",
	}
	plan := planStart(pkg, packageLaunch{
		Endpoint:  "https://world-time.example/mcp",
		Transport: "streamable-http",
	}, false)

	if plan.Action != startActionRefuseRemote {
		t.Fatalf("action = %q, want %q", plan.Action, startActionRefuseRemote)
	}
	if plan.Command != "" {
		t.Errorf("command = %q, remote start must not produce a shell command", plan.Command)
	}
	if !strings.Contains(strings.ToLower(plan.Message), "remote") {
		t.Errorf("message = %q, want it to mention remote", plan.Message)
	}
}

func TestPlanStartRefusesHTTPCommandAsShell(t *testing.T) {
	pkg := install.InstalledPackage{
		Name:      "evil-remote",
		Version:   "1.0.0",
		Transport: "http-sse",
	}
	plan := planStart(pkg, packageLaunch{
		Command:   "https://evil.example/payload.sh",
		Transport: "http-sse",
	}, false)

	if plan.Action != startActionRefuseRemote {
		t.Fatalf("action = %q, want refuse remote (must not exec URL)", plan.Action)
	}
	if isRemoteLaunchURL(plan.Command) {
		t.Error("plan leaked a remote URL into Command")
	}
}

func TestPlanStartLaunchesKind2LocalHTTP(t *testing.T) {
	pkg := install.InstalledPackage{
		Name:      "test-echo-server",
		Version:   "0.2.4",
		Transport: "http-sse",
		Location:  "/store/test-echo-server/0.2.4",
	}
	plan := planStart(pkg, packageLaunch{
		Bin:       "python server.py --port 8765",
		Transport: "http-sse",
		Location:  pkg.Location,
	}, false)

	if plan.Action != startActionLaunch {
		t.Fatalf("action = %q, want %q", plan.Action, startActionLaunch)
	}
	if plan.Command != "python server.py --port 8765" {
		t.Errorf("command = %q", plan.Command)
	}
	if plan.WorkDir != pkg.Location {
		t.Errorf("workDir = %q, want package location", plan.WorkDir)
	}
	if plan.Port != 8765 {
		t.Errorf("port = %d, want 8765", plan.Port)
	}
}

func TestPlanStartStdioNeedsForeground(t *testing.T) {
	pkg := install.InstalledPackage{
		Name:      "ev4nv-models",
		Version:   "1.0.0",
		Transport: "stdio",
	}
	bg := planStart(pkg, packageLaunch{Command: "npx -y ev4nv-models", Transport: "stdio"}, false)
	if bg.Action != startActionRefuseStdio {
		t.Fatalf("background action = %q, want %q", bg.Action, startActionRefuseStdio)
	}

	fg := planStart(pkg, packageLaunch{Command: "npx -y ev4nv-models", Transport: "stdio"}, true)
	if fg.Action != startActionLaunch {
		t.Fatalf("foreground action = %q, want %q", fg.Action, startActionLaunch)
	}
}

func TestResolveStartListenPort(t *testing.T) {
	tests := []struct {
		name      string
		flagPort  int
		planPort  int
		localHTTP bool
		want      int
	}{
		{"flag wins", 9999, 8765, true, 9999},
		{"plan port when no flag", 0, 4321, true, 4321},
		{"local http default when extract found nothing", 0, 0, true, 8765},
		{"stdio or remote must not invent 8765", 0, 0, false, 0},
		{"remote with leftover plan port still uses plan", 0, 443, false, 443},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveStartListenPort(tt.flagPort, tt.planPort, tt.localHTTP)
			if got != tt.want {
				t.Errorf("resolveStartListenPort(%d, %d, %v) = %d, want %d",
					tt.flagPort, tt.planPort, tt.localHTTP, got, tt.want)
			}
		})
	}
}

func TestKind2StartAlreadyListeningIsAlreadyRunning(t *testing.T) {
	pkg := install.InstalledPackage{
		Name:      "test-echo-server",
		Version:   "0.2.4",
		Transport: "http-sse",
		Location:  "/store/test-echo-server/0.2.4",
	}
	launch := packageLaunch{
		Bin:       "python server.py",
		Transport: "http-sse",
		Location:  pkg.Location,
	}
	plan := planStart(pkg, launch, false)
	if plan.Action != startActionLaunch {
		t.Fatalf("plan action = %q, want launch before already-up overlay", plan.Action)
	}

	st := runtime.ProcessStatus{Running: true, Port: 8765}
	action := resolveKind2StartAction(pkg.Name, launch, plan, st, nil)
	if action != startActionAlreadyRunning {
		t.Fatalf("action = %q, want %q (already listening must not call runtime.Start)", action, startActionAlreadyRunning)
	}
}

func TestKind2StartDaemonOverlayIsAlreadyRunning(t *testing.T) {
	launch := packageLaunch{
		Bin:       "python server.py",
		Transport: "http-sse",
	}
	plan := startPlan{Action: startActionLaunch, Command: "python server.py", Port: 8765}
	daemon := map[string]daemonServerState{
		"test-echo-server": {Name: "test-echo-server", State: "running", Port: 8421},
	}

	action := resolveKind2StartAction("test-echo-server", launch, plan, runtime.ProcessStatus{}, daemon)
	if action != startActionAlreadyRunning {
		t.Fatalf("action = %q, want %q when daemon overlay says running", action, startActionAlreadyRunning)
	}
}

func TestKind2StartProbesOpenListenPortAsAlreadyRunning(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	launch := packageLaunch{
		Bin:       "python server.py",
		Transport: "http-sse",
	}
	plan := startPlan{Action: startActionLaunch, Command: "python server.py", Port: port}
	st := runtime.ProbeStatus("kind2-already-up-probe", port)
	if !st.Running {
		t.Fatalf("ProbeStatus should treat open listen port %d as running", port)
	}
	action := resolveKind2StartAction("kind2-already-up-probe", launch, plan, st, nil)
	if action != startActionAlreadyRunning {
		t.Fatalf("action = %q, want %q (open listen port must not call runtime.Start)", action, startActionAlreadyRunning)
	}
}

func TestKind2StartNotUpStillLaunches(t *testing.T) {
	launch := packageLaunch{
		Bin:       "python server.py",
		Transport: "http-sse",
	}
	plan := startPlan{Action: startActionLaunch, Command: "python server.py"}
	action := resolveKind2StartAction("test-echo-server", launch, plan, runtime.ProcessStatus{}, nil)
	if action != startActionLaunch {
		t.Fatalf("action = %q, want launch when nothing is listening", action)
	}
}

func TestKind1StartStillRefuseRemote(t *testing.T) {
	launch := packageLaunch{
		Endpoint:  "https://world-time.example/mcp",
		Transport: "streamable-http",
	}
	plan := startPlan{Action: startActionRefuseRemote, Message: "remote"}
	action := resolveKind2StartAction("com.invokera/world-time", launch, plan, runtime.ProcessStatus{Running: true}, nil)
	if action != startActionRefuseRemote {
		t.Fatalf("kind 1 action = %q, want refuse-remote", action)
	}
}

func TestKind3StartStillRefuseBackground(t *testing.T) {
	launch := packageLaunch{
		Command:   "npx -y ev4nv-models",
		Transport: "stdio",
	}
	plan := startPlan{Action: startActionRefuseStdio}
	action := resolveKind2StartAction("ev4nv-models", launch, plan, runtime.ProcessStatus{}, nil)
	if action != startActionRefuseStdio {
		t.Fatalf("kind 3 action = %q, want refuse-stdio", action)
	}
}

func TestIsRemoteLaunchURL(t *testing.T) {
	yes := []string{"https://ex.com/mcp", "HTTP://ex.com", "  https://x  "}
	no := []string{"", "python server.py", "npx -y pkg", "bin/echo", "http-sse"}
	for _, s := range yes {
		if !isRemoteLaunchURL(s) {
			t.Errorf("isRemoteLaunchURL(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if isRemoteLaunchURL(s) {
			t.Errorf("isRemoteLaunchURL(%q) = true, want false", s)
		}
	}
}
