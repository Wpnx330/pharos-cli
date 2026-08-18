package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/install"
	"github.com/Wpnx330/pharos-cli/internal/runtime"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

// listDash is the contract placeholder for metrics that do not apply
// (kind 1 remote bookmarks; idle stdio ports).
const listDash = "—"

// defaultKind2ListenPort is used when a kind-2 HTTP server has no --port
// in its command/bin and no persisted/daemon port. test-echo-server
// (python server.py) listens on 8765.
const defaultKind2ListenPort = 8765

// Kind numbers match install.Kind (KindNone=0, remote=1, local HTTP=2, stdio=3).
const (
	kindUnknown   = int(install.KindNone)
	kindRemote    = int(install.KindRemoteHTTP)
	kindLocalHTTP = int(install.KindLocalHTTP)
	kindStdio     = int(install.KindStdio)
)

const (
	startActionLaunch         = "launch"
	startActionRefuseRemote   = "refuse-remote"
	startActionRefuseStdio    = "refuse-stdio"
	startActionAlreadyRunning = "already-running"
	startActionError          = "error"
)

// packageLaunch is the launch-relevant view of an installed package,
// assembled from existing metadata fields (transport, location) plus
// optional endpoint/bin/command/runtime/package if present on disk.
type packageLaunch struct {
	Endpoint  string
	Bin       string
	Command   string
	Runtime   string
	Package   string
	Transport string
	Location  string
	Kind      install.Kind
}

type listRow struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Transport    string `json:"transport"`
	Kind         int    `json:"kind"`
	Status       string `json:"status"`
	Endpoint     string `json:"endpoint,omitempty"`
	Port         string `json:"port"`
	Size         string `json:"size"`
	Memory       string `json:"memory"`
	Uptime       string `json:"uptime"`
	Idle         string `json:"idle,omitempty"`
	LastActivity string `json:"lastActivity,omitempty"`
}

type startPlan struct {
	Action  string
	Command string
	WorkDir string
	Port    int
	Message string
}

type removePlan struct {
	Kind           int
	StopProcess    bool
	DeleteStore    bool
	DeleteBookmark bool
	DeleteConfig   bool
	DeleteTarball  bool
	Location       string
}

type launchFileFields struct {
	Endpoint  string `json:"endpoint"`
	Bin       string `json:"bin"`
	Command   string `json:"command"`
	Runtime   string `json:"runtime"`
	Package   string `json:"package"`
	Transport string `json:"transport"`
	Location  string `json:"location"`
}

func isRemoteLaunchURL(s string) bool {
	return install.IsHTTPEndpoint(s)
}

func classifyInputOf(l packageLaunch) install.ClassifyInput {
	return install.ClassifyInput{
		Transport: l.Transport,
		Endpoint:  l.Endpoint,
		Bin:       l.Bin,
		Command:   l.Command,
		Runtime:   l.Runtime,
		Package:   l.Package,
	}
}

// inferLaunchKind delegates to install.ClassifyKind (T1a). Installed
// leftovers with incomplete metadata (HTTP bookmark, no endpoint field
// yet) fall back to kind 1 / 3 so list/remove still work.
func inferLaunchKind(l packageLaunch) int {
	if l.Kind == install.KindRemoteHTTP || l.Kind == install.KindLocalHTTP || l.Kind == install.KindStdio {
		return int(l.Kind)
	}
	k := install.ClassifyKind(classifyInputOf(l))
	if k != install.KindNone {
		return int(k)
	}
	if install.IsHTTPFamily(l.Transport) || install.IsHTTPEndpoint(l.Endpoint) {
		return kindRemote
	}
	t := strings.ToLower(strings.TrimSpace(l.Transport))
	if t == "stdio" || t == "" {
		return kindStdio
	}
	return kindUnknown
}

// listenPort is the TCP port list should probe for a local HTTP (kind 2)
// server. Order: ExtractPort(command/bin), then persisted/daemon port,
// then 8765 for kind-2 HTTP-family with no declared port.
func listenPort(launch packageLaunch, persistedPort int) int {
	if p := runtime.ExtractPort(resolveLaunchCommand(launch)); p > 0 {
		return p
	}
	if p := runtime.ExtractPort(strings.TrimSpace(launch.Endpoint)); p > 0 {
		return p
	}
	if persistedPort > 0 {
		return persistedPort
	}
	if inferLaunchKind(launch) == kindLocalHTTP {
		return defaultKind2ListenPort
	}
	return 0
}

func kind2ListenPort(name string, launch packageLaunch, daemon map[string]daemonServerState) int {
	// Daemon port is the proxy (typically 8421). Display/probe PORT is the
	// backing listen port from command/bin or the kind-2 default (8765).
	_ = name
	_ = daemon
	return listenPort(launch, 0)
}

func daemonStateIsRunning(ds daemonServerState) bool {
	return strings.ToLower(strings.TrimSpace(ds.State)) == "running"
}

// applyKind2DaemonRunning treats a kind-2 row as running when daemon.json
// says state==running. Never copies ds.Port — that is the daemon proxy
// (8421), not the backing listen port. Never call this for kind 1.
func applyKind2DaemonRunning(name string, st runtime.ProcessStatus, daemon map[string]daemonServerState) runtime.ProcessStatus {
	ds, ok := daemon[name]
	if !ok || !daemonStateIsRunning(ds) {
		return st
	}
	st.Running = true
	return st
}

func resolveLaunchCommand(l packageLaunch) string {
	if c := strings.TrimSpace(l.Command); c != "" {
		return c
	}
	if b := strings.TrimSpace(l.Bin); b != "" {
		return b
	}
	rt := strings.ToLower(strings.TrimSpace(l.Runtime))
	pkg := strings.TrimSpace(l.Package)
	if pkg == "" {
		return ""
	}
	switch rt {
	case "npx":
		return "npx -y " + pkg
	case "uvx":
		return "uvx " + pkg
	case "docker":
		return "docker run --rm -i " + pkg
	case "python":
		return "python3 -m " + pkg
	default:
		return ""
	}
}

func loadPackageLaunch(storeDir string, pkg install.InstalledPackage) packageLaunch {
	l := packageLaunch{
		Endpoint:  pkg.Endpoint,
		Bin:       pkg.Bin,
		Command:   pkg.Command,
		Runtime:   pkg.Runtime,
		Package:   pkg.Package,
		Transport: pkg.Transport,
		Location:  pkg.Location,
		Kind:      pkg.Kind,
	}
	var files []string
	if pkg.Location != "" {
		files = append(files,
			filepath.Join(pkg.Location, ".pharos-installed.json"),
			filepath.Join(pkg.Location, "pharos.json"),
		)
	}
	if storeDir != "" && pkg.Name != "" && pkg.Version != "" {
		dir := filepath.Join(storeDir, pkg.Name, pkg.Version)
		files = append(files,
			filepath.Join(dir, ".pharos-installed.json"),
			filepath.Join(dir, "pharos.json"),
		)
	}
	for _, p := range files {
		mergeLaunchFile(&l, p)
	}
	return l
}

func mergeLaunchFile(l *packageLaunch, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var f launchFileFields
	if json.Unmarshal(data, &f) != nil {
		return
	}
	if l.Endpoint == "" {
		l.Endpoint = f.Endpoint
	}
	if l.Bin == "" {
		l.Bin = f.Bin
	}
	if l.Command == "" {
		l.Command = f.Command
	}
	if l.Runtime == "" {
		l.Runtime = f.Runtime
	}
	if l.Package == "" {
		l.Package = f.Package
	}
	if l.Transport == "" {
		l.Transport = f.Transport
	}
	if l.Location == "" {
		l.Location = f.Location
	}
}

func buildListRow(pkg install.InstalledPackage, launch packageLaunch, st runtime.ProcessStatus, size int64, daemon map[string]daemonServerState) listRow {
	if launch.Transport == "" {
		launch.Transport = pkg.Transport
	}
	if launch.Location == "" {
		launch.Location = pkg.Location
	}
	kind := inferLaunchKind(launch)
	row := listRow{
		Name:      pkg.Name,
		Version:   pkg.Version,
		Transport: pkg.Transport,
		Kind:      kind,
		Port:      listDash,
		Size:      listDash,
		Memory:    listDash,
		Uptime:    listDash,
		Idle:      listDash,
		LastActivity: listDash,
	}
	if row.Transport == "" {
		row.Transport = launch.Transport
	}

	switch kind {
	case kindRemote:
		row.Endpoint = strings.TrimSpace(launch.Endpoint)
		row.Status = "registered"
		if ds, ok := daemon[pkg.Name]; ok {
			state := strings.ToLower(strings.TrimSpace(ds.State))
			if state == "connected" || state == "live" || state == "active" {
				row.Status = "connected"
			}
		}
	case kindLocalHTTP:
		st = applyKind2DaemonRunning(pkg.Name, st, daemon)
		if st.Running {
			row.Status = "running"
			port := st.Port
			if port == 0 {
				port = listenPort(launch, 0)
			}
			if port > 0 {
				row.Port = fmt.Sprintf("%d", port)
			}
			if st.Memory > 0 {
				row.Memory = ui.FormatBytes(st.Memory)
			}
			if st.Uptime != "" {
				row.Uptime = st.Uptime
			}
		} else {
			// Main list never shows daemon "unloaded".
			row.Status = "stopped"
		}
		if size > 0 {
			row.Size = ui.FormatBytes(size)
		}
	default: // kind 3 / unknown
		if st.Running {
			row.Status = "running"
			if st.Uptime != "" {
				row.Uptime = st.Uptime
			}
			if st.Memory > 0 {
				row.Memory = ui.FormatBytes(st.Memory)
			}
		} else {
			row.Status = "idle"
		}
		if size > 0 {
			row.Size = ui.FormatBytes(size)
		}
	}

	if ds, ok := daemon[pkg.Name]; ok && ds.LastActivity != "" && kind != kindRemote {
		if t, err := time.Parse(time.RFC3339, ds.LastActivity); err == nil {
			row.Idle = formatDuration(time.Since(t))
			row.LastActivity = formatTimeAgo(t)
		}
	}
	return row
}

func marshalListJSON(rows []listRow) ([]byte, error) {
	return json.MarshalIndent(rows, "", "  ")
}

func planStart(pkg install.InstalledPackage, launch packageLaunch, foreground bool) startPlan {
	if launch.Transport == "" {
		launch.Transport = pkg.Transport
	}
	if launch.Location == "" {
		launch.Location = pkg.Location
	}

	if isRemoteLaunchURL(launch.Endpoint) || isRemoteLaunchURL(launch.Command) || isRemoteLaunchURL(launch.Bin) {
		return startPlan{
			Action: startActionRefuseRemote,
			Message: fmt.Sprintf("%s is a remote server — it lives at a publisher URL. Pharos will not start a remote URL as a local process.", pkg.Name),
		}
	}

	kind := inferLaunchKind(launch)
	if kind == kindRemote {
		return startPlan{
			Action: startActionRefuseRemote,
			Message: fmt.Sprintf("%s is a remote HTTP server (kind 1). There is no local process to start.", pkg.Name),
		}
	}

	if kind == kindStdio && !foreground {
		return startPlan{
			Action: startActionRefuseStdio,
			Message: fmt.Sprintf("%s is a stdio server — it launches when an MCP client connects. Use --foreground to debug.", pkg.Name),
		}
	}

	cmdLine := resolveLaunchCommand(launch)
	if isRemoteLaunchURL(cmdLine) {
		return startPlan{
			Action:  startActionRefuseRemote,
			Message: "refusing to execute a remote URL as a shell command",
		}
	}
	if cmdLine == "" {
		return startPlan{
			Action:  startActionError,
			Message: "manifest has no command or bin field — cannot determine how to start the server",
		}
	}

	port := 0
	if kind == kindLocalHTTP {
		port = runtime.ExtractPort(cmdLine)
	}
	workDir := launch.Location
	if workDir == "" {
		workDir = pkg.Location
	}
	return startPlan{
		Action:  startActionLaunch,
		Command: cmdLine,
		WorkDir: workDir,
		Port:    port,
	}
}

// resolveKind2StartAction overlays the kind-2 already-up no-op on a
// start plan. Kind 1 refuse-remote and kind 3 refuse-stdio are left
// unchanged. When the listen port is accepting or daemon.json says
// running, start must not call runtime.Start (avoids "port already in use").
func resolveKind2StartAction(name string, launch packageLaunch, plan startPlan, st runtime.ProcessStatus, daemon map[string]daemonServerState) string {
	if plan.Action != startActionLaunch {
		return plan.Action
	}
	if inferLaunchKind(launch) != kindLocalHTTP {
		return plan.Action
	}
	st = applyKind2DaemonRunning(name, st, daemon)
	if st.Running {
		return startActionAlreadyRunning
	}
	return startActionLaunch
}

func confinedPackageDir(storeDir, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("invalid package name %q", name)
	}
	if filepath.IsAbs(name) || filepath.IsAbs(filepath.FromSlash(name)) {
		return "", fmt.Errorf("package name must not be an absolute path")
	}
	// Allow scoped names (com.invokera/world-time) but reject any ".." hop.
	normalized := filepath.ToSlash(strings.ReplaceAll(name, `\`, "/"))
	if strings.HasPrefix(normalized, "/") {
		return "", fmt.Errorf("package name must not be an absolute path")
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid package name %q", name)
		}
	}
	full := filepath.Join(append([]string{storeDir}, strings.Split(normalized, "/")...)...)
	if !pathInsideStore(storeDir, full) {
		return "", fmt.Errorf("resolved path escapes store")
	}
	return full, nil
}

func pathInsideStore(storeDir, target string) bool {
	storeAbs, err := filepath.Abs(storeDir)
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	storeAbs = filepath.Clean(storeAbs)
	targetAbs = filepath.Clean(targetAbs)
	if targetAbs == storeAbs {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(targetAbs, storeAbs+sep)
}

func safeRemoveStorePath(storeDir, target string) error {
	storeAbs, err := filepath.Abs(storeDir)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	storeAbs = filepath.Clean(storeAbs)
	targetAbs = filepath.Clean(targetAbs)
	if targetAbs == storeAbs {
		return fmt.Errorf("refusing to delete store root")
	}
	if !pathInsideStore(storeAbs, targetAbs) {
		return fmt.Errorf("refusing to delete path outside store: %s", target)
	}
	return os.RemoveAll(targetAbs)
}

func planRemove(storeDir, name string, launch packageLaunch) (removePlan, error) {
	kind := inferLaunchKind(launch)
	plan := removePlan{
		Kind:           kind,
		DeleteBookmark: true,
		DeleteConfig:   true,
	}

	if _, err := confinedPackageDir(storeDir, name); err != nil {
		return plan, err
	}

	switch kind {
	case kindRemote:
		plan.StopProcess = false
		plan.DeleteTarball = false
		plan.DeleteStore = true // metadata bookmark only
	case kindLocalHTTP:
		plan.StopProcess = true
		plan.DeleteStore = true
		if launch.Location != "" && pathInsideStore(storeDir, launch.Location) {
			plan.DeleteTarball = true
			plan.Location = launch.Location
		}
	default:
		hasExtract := launch.Location != "" && pathInsideStore(storeDir, launch.Location)
		plan.StopProcess = hasExtract
		plan.DeleteStore = true
		if hasExtract {
			plan.DeleteTarball = true
			plan.Location = launch.Location
		} else {
			plan.DeleteTarball = false
		}
	}
	return plan, nil
}
