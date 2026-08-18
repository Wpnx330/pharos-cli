package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/install"
	"github.com/Wpnx330/pharos-cli/internal/runtime"
)

func TestBuildListRowKind1RemoteBookmark(t *testing.T) {
	pkg := install.InstalledPackage{
		Name:      "com.invokera/world-time",
		Version:   "1.0.0",
		Transport: "streamable-http",
		Location:  "",
	}
	launch := packageLaunch{
		Endpoint:  "https://world-time.example/mcp",
		Transport: "streamable-http",
	}

	row := buildListRow(pkg, launch, runtime.ProcessStatus{}, 4096, nil)

	if row.Kind != 1 {
		t.Fatalf("kind = %d, want 1", row.Kind)
	}
	if row.Status != "registered" && row.Status != "remote" {
		t.Errorf("status = %q, want registered or remote", row.Status)
	}
	if row.Endpoint != "https://world-time.example/mcp" {
		t.Errorf("endpoint = %q, want publisher URL", row.Endpoint)
	}
	if row.Port != listDash || row.Size != listDash || row.Memory != listDash || row.Uptime != listDash {
		t.Errorf("kind 1 metrics = port=%q size=%q memory=%q uptime=%q, want all %q",
			row.Port, row.Size, row.Memory, row.Uptime, listDash)
	}
}

func TestBuildListRowKind1DoesNotFakeRunningFromRemoteURL(t *testing.T) {
	pkg := install.InstalledPackage{
		Name:      "test-echo-server",
		Version:   "0.2.5",
		Transport: "http-sse",
		Location:  "",
	}
	launch := packageLaunch{
		Endpoint:  "https://echo.example/sse",
		Bin:       "bin/echo",
		Transport: "http-sse",
	}
	// A remote URL must never be treated as a local running process.
	st := runtime.ProcessStatus{Running: true, Port: 443, Memory: 99, Uptime: "1h"}

	row := buildListRow(pkg, launch, st, 0, nil)

	if row.Kind != 1 {
		t.Fatalf("kind = %d, want 1 (endpoint + bin is kind 1)", row.Kind)
	}
	if row.Status == "running" {
		t.Error("kind 1 must not report running for a remote URL")
	}
	if row.Port != listDash || row.Memory != listDash || row.Uptime != listDash {
		t.Errorf("kind 1 leaked local metrics: port=%q memory=%q uptime=%q", row.Port, row.Memory, row.Uptime)
	}
}

func TestBuildListRowKind2StoppedAndRunning(t *testing.T) {
	pkg := install.InstalledPackage{
		Name:      "test-echo-server",
		Version:   "0.2.4",
		Transport: "http-sse",
		Location:  "/tmp/store/test-echo-server/0.2.4",
	}
	launch := packageLaunch{
		Bin:       "python server.py --port 8765",
		Transport: "http-sse",
	}

	stopped := buildListRow(pkg, launch, runtime.ProcessStatus{}, 2048, nil)
	if stopped.Kind != 2 {
		t.Fatalf("kind = %d, want 2", stopped.Kind)
	}
	if stopped.Status != "stopped" {
		t.Errorf("stopped status = %q, want stopped", stopped.Status)
	}
	if stopped.Size == listDash {
		t.Error("kind 2 should report on-disk size")
	}

	running := buildListRow(pkg, launch, runtime.ProcessStatus{
		Running: true,
		Port:    8765,
		Memory:  4096,
		Uptime:  "12s",
	}, 2048, nil)
	if running.Status != "running" {
		t.Errorf("running status = %q, want running", running.Status)
	}
	if running.Port != "8765" {
		t.Errorf("port = %q, want 8765", running.Port)
	}
	if running.Memory == listDash || running.Memory == "" {
		t.Errorf("memory = %q, want formatted bytes", running.Memory)
	}
	if running.Uptime != "12s" {
		t.Errorf("uptime = %q, want 12s", running.Uptime)
	}
}

func TestBuildListRowKind3IdleUnlessProcess(t *testing.T) {
	pkg := install.InstalledPackage{
		Name:      "ev4nv-models",
		Version:   "1.0.0",
		Transport: "stdio",
		Location:  "",
	}
	launch := packageLaunch{
		Command:   "npx -y ev4nv-models",
		Runtime:   "npx",
		Package:   "ev4nv-models",
		Transport: "stdio",
	}

	idle := buildListRow(pkg, launch, runtime.ProcessStatus{}, 0, nil)
	if idle.Kind != 3 {
		t.Fatalf("kind = %d, want 3", idle.Kind)
	}
	if idle.Status != "idle" {
		t.Errorf("status = %q, want idle", idle.Status)
	}

	child := buildListRow(pkg, launch, runtime.ProcessStatus{Running: true, Uptime: "3s"}, 0, nil)
	if child.Status != "running" {
		t.Errorf("child-up status = %q, want running", child.Status)
	}
}

func TestMarshalListJSON(t *testing.T) {
	rows := []listRow{{
		Name:      "com.invokera/world-time",
		Version:   "1.0.0",
		Transport: "streamable-http",
		Kind:      1,
		Status:    "registered",
		Endpoint:  "https://world-time.example/mcp",
		Port:      listDash,
		Size:      listDash,
		Memory:    listDash,
		Uptime:    listDash,
	}}

	raw, err := marshalListJSON(rows)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got []listRow
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	if len(got) != 1 || got[0].Kind != 1 || got[0].Endpoint == "" || got[0].Status != "registered" {
		t.Fatalf("json payload = %+v", got)
	}
	if !strings.Contains(string(raw), `"kind": 1`) && !strings.Contains(string(raw), `"kind":1`) {
		t.Errorf("json missing kind field: %s", raw)
	}
}

func TestLoadPackageLaunchPrefersPersistedFields(t *testing.T) {
	pkg := install.InstalledPackage{
		Name:      "com.invokera/world-time",
		Version:   "1.0.0",
		Transport: "streamable-http",
		Endpoint:  "https://world-time.example/mcp",
		Kind:      install.KindRemoteHTTP,
	}
	got := loadPackageLaunch("", pkg)
	if got.Endpoint != pkg.Endpoint {
		t.Errorf("endpoint = %q, want persisted field", got.Endpoint)
	}
	if inferLaunchKind(got) != kindRemote {
		t.Errorf("kind = %d, want 1", inferLaunchKind(got))
	}
}

func TestListTableColumnsNoKindEndpointLast(t *testing.T) {
	cols := listTableColumns()
	if len(cols) == 0 {
		t.Fatal("listTableColumns() returned no columns")
	}
	titles := make([]string, 0, len(cols))
	for _, c := range cols {
		if strings.EqualFold(c.Title, "KIND") {
			t.Errorf("human table must not include KIND title")
		}
		titles = append(titles, c.Title)
	}
	want := []string{"NAME", "VERSION", "TRANSPORT", "STATUS", "PORT", "SIZE", "MEMORY", "UPTIME", "IDLE", "LAST ACTIVITY", "ENDPOINT"}
	if len(titles) != len(want) {
		t.Fatalf("columns = %v, want %v", titles, want)
	}
	for i := range want {
		if titles[i] != want[i] {
			t.Fatalf("columns = %v, want %v", titles, want)
		}
	}
	if titles[len(titles)-1] != "ENDPOINT" {
		t.Errorf("last column = %q, want ENDPOINT", titles[len(titles)-1])
	}
}

func TestListenPortKind2Default8765WhenNoPortInBin(t *testing.T) {
	launch := packageLaunch{
		Bin:       "python server.py",
		Transport: "http-sse",
	}
	if inferLaunchKind(launch) != kindLocalHTTP {
		t.Fatalf("kind = %d, want 2", inferLaunchKind(launch))
	}
	got := listenPort(launch, 0)
	if got != 8765 {
		t.Errorf("listenPort(python server.py) = %d, want 8765", got)
	}

	running := buildListRow(
		install.InstalledPackage{Name: "test-echo-server", Version: "0.2.6", Transport: "http-sse"},
		launch,
		runtime.ProcessStatus{Running: true},
		1024,
		nil,
	)
	if running.Port != "8765" {
		t.Errorf("running row port = %q, want 8765 (default when bin has no --port)", running.Port)
	}
	if running.Status != "running" {
		t.Errorf("status = %q, want running", running.Status)
	}
}

func TestListenPortPrefersExtractedThenPersisted(t *testing.T) {
	fromCmd := listenPort(packageLaunch{
		Bin:       "python server.py --port 9001",
		Transport: "http-sse",
	}, 8765)
	if fromCmd != 9001 {
		t.Errorf("extracted port = %d, want 9001", fromCmd)
	}

	fromDaemon := listenPort(packageLaunch{
		Bin:       "python server.py",
		Transport: "http-sse",
	}, 8421)
	if fromDaemon != 8421 {
		t.Errorf("persisted/daemon port = %d, want 8421", fromDaemon)
	}
}

func TestBuildListRowKind2UsesDaemonRunningAndPort(t *testing.T) {
	pkg := install.InstalledPackage{
		Name:      "test-echo-server",
		Version:   "0.2.6",
		Transport: "http-sse",
		Location:  "/tmp/store/test-echo-server/0.2.6",
	}
	launch := packageLaunch{
		Bin:       "python server.py",
		Transport: "http-sse",
	}
	daemon := map[string]daemonServerState{
		"test-echo-server": {Name: "test-echo-server", State: "running", Port: 8765},
	}

	row := buildListRow(pkg, launch, runtime.ProcessStatus{}, 2048, daemon)
	if row.Kind != 2 {
		t.Fatalf("kind = %d, want 2", row.Kind)
	}
	if row.Status != "running" {
		t.Errorf("status = %q, want running (daemon state==running)", row.Status)
	}
	if row.Port != "8765" {
		t.Errorf("port = %q, want 8765 from daemon", row.Port)
	}
	if row.Status == "unloaded" {
		t.Error("main list must not show unloaded")
	}
}

func TestBuildListRowKind2DaemonUnloadedIsStopped(t *testing.T) {
	pkg := install.InstalledPackage{
		Name:      "test-echo-server",
		Version:   "0.2.6",
		Transport: "http-sse",
	}
	launch := packageLaunch{
		Bin:       "python server.py",
		Transport: "http-sse",
	}
	daemon := map[string]daemonServerState{
		"test-echo-server": {Name: "test-echo-server", State: "unloaded", Port: 8421},
	}
	row := buildListRow(pkg, launch, runtime.ProcessStatus{}, 0, daemon)
	if row.Status != "stopped" {
		t.Errorf("status = %q, want stopped (not unloaded)", row.Status)
	}
}

func TestBuildListRowKind1IgnoresDaemonRunning(t *testing.T) {
	pkg := install.InstalledPackage{
		Name:      "com.invokera/world-time",
		Version:   "1.0.0",
		Transport: "streamable-http",
	}
	launch := packageLaunch{
		Endpoint:  "https://world-time.example/mcp",
		Transport: "streamable-http",
	}
	daemon := map[string]daemonServerState{
		"com.invokera/world-time": {Name: "com.invokera/world-time", State: "running", Port: 8765},
	}
	row := buildListRow(pkg, launch, runtime.ProcessStatus{Running: true, Port: 8765}, 0, daemon)
	if row.Status == "running" {
		t.Error("kind 1 must not invent running from daemon state")
	}
	if row.Port != listDash {
		t.Errorf("kind 1 port = %q, want %q", row.Port, listDash)
	}
}

func TestInferLaunchKindFixtures(t *testing.T) {
	tests := []struct {
		name   string
		launch packageLaunch
		want   int
	}{
		{name: "F1 streamable-http + endpoint", launch: packageLaunch{Transport: "streamable-http", Endpoint: "https://ex/mcp"}, want: 1},
		{name: "F2 http-sse + endpoint + bin", launch: packageLaunch{Transport: "http-sse", Endpoint: "https://ex/sse", Bin: "bin/echo"}, want: 1},
		{name: "F3 http-sse + bin no endpoint", launch: packageLaunch{Transport: "http-sse", Bin: "bin/echo"}, want: 2},
		{name: "F4 stdio tarball", launch: packageLaunch{Transport: "stdio", Bin: "bin/server", Location: "/store/pkg/1.0.0"}, want: 3},
		{name: "F5 stdio npx no tarball", launch: packageLaunch{Transport: "stdio", Command: "npx -y pkg", Runtime: "npx", Package: "pkg"}, want: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferLaunchKind(tt.launch)
			if got != tt.want {
				t.Errorf("inferLaunchKind() = %d, want %d", got, tt.want)
			}
		})
	}
}
