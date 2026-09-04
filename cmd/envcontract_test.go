package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// =============================================================
// envcontract.go — value parsing
// =============================================================

// TestEnvContractValueParsing pins the liberal value grammar: empty/unset is
// false, "1"/"true"/"yes" (any case, any surrounding space) is true, and
// everything else — including "0", "false", "no", and garbage — is false
// with no error.
func TestEnvContractValueParsing(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},        // empty = false (same as unset)
		{"1", true},        // canonical
		{"true", true},     // canonical
		{"yes", true},      // canonical
		{"TRUE", true},     // case-insensitive
		{"Yes", true},      // case-insensitive
		{"  1  ", true},    // trimmed
		{"0", false},       // explicit false
		{"false", false},   // explicit false
		{"no", false},      // explicit false
		{"FALSE", false},   // case-insensitive false
		{"on", false},      // unrecognized = false, never an error
		{"garbage", false}, // unrecognized = false, never an error
		{"2", false},       // unrecognized = false, never an error
	}
	for _, tt := range tests {
		for _, name := range []string{"PHAROS_NON_INTERACTIVE", "PHAROS_ASSUME_YES", "PHAROS_JSON"} {
			t.Setenv(name, tt.value)
			var got bool
			switch name {
			case "PHAROS_NON_INTERACTIVE":
				got = NonInteractive()
			case "PHAROS_ASSUME_YES":
				got = AssumeYes()
			case "PHAROS_JSON":
				got = envTruthy(name) // JSONRequested also consults flags; test the env half directly
			}
			if got != tt.want {
				t.Errorf("%s=%q parsed %v, want %v", name, tt.value, got, tt.want)
			}
		}
	}
}

// TestJSONRequestedFlagAndEnvEquivalence proves the contract's core promise:
// --json and PHAROS_JSON=1 produce byte-identical, valid JSON output.
func TestJSONRequestedFlagAndEnvEquivalence(t *testing.T) {
	isolateHome(t)
	flagOut, _ := runContract(t, nil, "version", "--json")
	envOut, _ := runContract(t, map[string]string{"PHAROS_JSON": "1"}, "version")

	if flagOut != envOut {
		t.Errorf("--json and PHAROS_JSON=1 disagree:\nflag: %q\nenv:  %q", flagOut, envOut)
	}
	if !json.Valid([]byte(strings.TrimSpace(flagOut))) {
		t.Errorf("version JSON is not valid: %q", flagOut)
	}
}

// TestJSONRequestedSeesFlagVar verifies JSONRequested honors the running
// command's own --json flag variable even without PHAROS_JSON set.
func TestJSONRequestedSeesFlagVar(t *testing.T) {
	t.Setenv("PHAROS_JSON", "")
	orig := jsonFlag
	jsonFlag = true
	t.Cleanup(func() { jsonFlag = orig })
	if !JSONRequested() {
		t.Error("JSONRequested() = false with jsonFlag=true, want true")
	}
}

// TestRequireNonInteractiveMessage pins the guidance format: the message
// MUST name the flag (or env var) that fixes the situation.
func TestRequireNonInteractiveMessage(t *testing.T) {
	err := RequireNonInteractive("init", "--yes or PHAROS_ASSUME_YES=1")
	want := "init requires --yes or PHAROS_ASSUME_YES=1 in non-interactive mode"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	var nie *NonInteractiveError
	if !errors.As(err, &nie) {
		t.Error("RequireNonInteractive must return a *NonInteractiveError")
	}
	if nie.Command != "init" || nie.Fix != "--yes or PHAROS_ASSUME_YES=1" {
		t.Errorf("fields = %q/%q, want init / --yes or PHAROS_ASSUME_YES=1", nie.Command, nie.Fix)
	}
}

// =============================================================
// Contract harness — runs the real command tree like the CLI would
// =============================================================

// contractCase is one row of the agent-contract table.
type contractCase struct {
	name    string
	args    []string
	env     map[string]string
	setup   func(t *testing.T)
	wantErr string   // substring expected in the combined output ("" = no error)
	wantOut []string // substrings expected in the combined output
	jsonOut bool     // stdout must be valid JSON
	skip    string   // documented exception: reason this row cannot run in-process
}

// runContract executes `pharos <args...>` in-process through the real cobra
// tree. Env entries are applied with t.Setenv (and therefore restored); every
// contract var not listed is forced empty so rows never inherit each other's
// state. Returns (stdout, combined stdout+stderr+execute-error).
func runContract(t *testing.T, env map[string]string, args ...string) (string, string) {
	t.Helper()

	// Force all contract vars empty, then apply the row's overrides.
	for _, name := range []string{"PHAROS_NON_INTERACTIVE", "PHAROS_ASSUME_YES", "PHAROS_JSON"} {
		t.Setenv(name, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}

	// Cobra flag variables are package-level and sticky between runs; reset
	// them synchronously (so this run starts clean) and after (so this run
	// does not leak into later tests).
	resetAllFlags()
	t.Cleanup(resetAllFlags)

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW

	outCh := make(chan string, 1)
	errCh := make(chan string, 1)
	go func() {
		var buf strings.Builder
		_, _ = io.Copy(&buf, outR)
		outCh <- buf.String()
	}()
	go func() {
		var buf strings.Builder
		_, _ = io.Copy(&buf, errR)
		errCh <- buf.String()
	}()

	rootCmd.SetArgs(args)
	execErr := rootCmd.Execute()

	os.Stdout, os.Stderr = origOut, origErr
	_ = outW.Close()
	_ = errW.Close()
	stdout := <-outCh
	stderr := <-errCh
	_ = outR.Close()
	_ = errR.Close()

	combined := stdout + stderr
	if execErr != nil {
		// Cobra has already printed "Error: ..." for RunE errors; include the
		// returned error text so substring assertions see it exactly once
		// more even if the writer changed.
		combined += "\n" + execErr.Error() + "\n"
	}
	return stdout, combined
}

// resetAllFlags restores every registered flag in the tree to its default so
// runs do not leak state into each other. Array-valued flags are skipped:
// pflag appends on Set, so they cannot be restored without Replace support.
func resetAllFlags() {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if !f.Changed {
				return
			}
			switch f.Value.Type() {
			case "stringArray", "stringSlice":
				return
			}
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		})
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
}

// runContractCase applies setup, runs the command, and asserts the row.
func runContractCase(t *testing.T, cc contractCase) {
	t.Helper()
	if cc.skip != "" {
		t.Skip(cc.skip)
	}
	if cc.setup != nil {
		cc.setup(t)
	}
	stdout, combined := runContract(t, cc.env, cc.args...)

	if cc.wantErr != "" && !strings.Contains(combined, cc.wantErr) {
		t.Errorf("pharos %s output %q does not contain guidance %q", strings.Join(cc.args, " "), combined, cc.wantErr)
	}
	for _, want := range cc.wantOut {
		if !strings.Contains(combined, want) {
			t.Errorf("pharos %s output %q does not contain %q", strings.Join(cc.args, " "), combined, want)
		}
	}
	if cc.jsonOut {
		trimmed := strings.TrimSpace(stdout)
		if !json.Valid([]byte(trimmed)) {
			t.Errorf("pharos %s with JSON requested did not emit valid JSON on stdout: %.200q", strings.Join(cc.args, " "), trimmed)
		}
	}
}

// isolateHome points HOME/USERPROFILE at a fresh temp dir so config,
// credentials, daemon state, and the store never touch the real home.
// APPDATA/LOCALAPPDATA follow so windows shell-folder candidates (Claude
// Desktop, Zed, Windsurf, MSIX scan) resolve under the isolated tree too.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	return home
}

// inTempDir moves the working directory to a fresh temp dir (for commands
// that write into cwd: init, lock, package, publish, update).
func inTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

// contractManifest writes a minimal valid stdio pharos.json into the
// current directory.
func contractManifest(t *testing.T) {
	t.Helper()
	m := `{
  "name": "contract-server",
  "version": "0.1.0",
  "description": "contract test server",
  "transport": "stdio",
  "runtime": "node",
  "command": "node server.js",
  "capabilities": ["tools"],
  "license": "MIT"
}`
	if err := os.WriteFile("pharos.json", []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}
	// The declared command's entrypoint is auto-detected into the tarball
	// file list, so it must exist on disk.
	if err := os.WriteFile("server.js", []byte("// contract test entrypoint\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// contractRegistry is a stand-in PHAROS registry serving every endpoint the
// contract table touches. It also isolates HOME and points the CLI's config
// at the stand-in, so a row must call it before any helper that expects the
// isolated home (fakeCredentials, fakeGenericClient, fakeLockfile...).
// All traffic stays on localhost — no external network access.
func contractRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	home := isolateHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, "/v1/health"):
			_, _ = io.WriteString(w, `{"status":"ok","version":"contract"}`)
		case strings.HasPrefix(path, "/v1/auth/me"):
			_, _ = io.WriteString(w, `{"sub":"u1","namespace":"tester","scope":"write"}`)
		case strings.HasPrefix(path, "/v1/advisories/"):
			_, _ = io.WriteString(w, `[]`)
		case strings.HasPrefix(path, "/v1/oauth/servers/"):
			_, _ = io.WriteString(w, `{}`)
		case strings.Contains(path, "/status"):
			_, _ = io.WriteString(w, `{}`) // PATCH .../versions/<v>/status
		case strings.HasPrefix(path, "/v1/packages/echo-server"):
			_, _ = io.WriteString(w, `{
				"name": "echo-server",
				"title": "Echo Server",
				"description": "contract test package",
				"created_at": "2026-01-01T00:00:00Z",
				"modified_at": "2026-01-01T00:00:00Z",
				"dist_tags": {"latest": "1.0.0"},
				"versions": [
					{
						"version": "1.0.0",
						"status": "active",
						"created_at": "2026-01-01T00:00:00Z",
						"manifest": {
							"name": "echo-server",
							"version": "1.0.0",
							"transport": "stdio",
							"capabilities": ["tools"]
						}
					}
				]
			}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	dir := filepath.Join(home, ".pharos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"registry":` + strconv.Quote(srv.URL) + `}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return srv
}

// contractHome returns the HOME previously isolated by isolateHome (or
// contractRegistry) for this test. Helpers must not re-isolate — that would
// orphan the registry config written by contractRegistry.
func contractHome(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return home
}

// fakeGenericClient plants a Generic MCP client config (the only built-in
// candidate under an isolated HOME) referencing the registry's echo-server.
// Requires a prior contractRegistry call. On WSL dev machines other
// Windows-side clients are also detected; those servers simply resolve as
// "unresolved" against the stand-in.
func fakeGenericClient(t *testing.T) {
	t.Helper()
	dir := filepath.Join(contractHome(t), ".config", "mcp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"mcpServers": {"echo-server": {"command": "node", "args": ["server.js"]}}}`
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeLockfile writes a pharos.lock pinning echo-server at the registry's
// latest version (up-to-date path). Requires a prior contractRegistry call.
func fakeLockfile(t *testing.T) {
	t.Helper()
	dir := inTempDir(t)
	lf := `{"version":1,"servers":{"echo-server":{"version":"1.0.0","integrity":"sha512-x","transport":"stdio","resolved":"","installedAt":"2026-01-01T00:00:00Z"}}}`
	if err := os.WriteFile(filepath.Join(dir, "pharos.lock"), []byte(lf), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeLockfileStale writes a pharos.lock pinning echo-server below the
// registry's latest version (update-available path; pair with --dry-run so
// no artifact download happens). Requires a prior contractRegistry call.
func fakeLockfileStale(t *testing.T) {
	t.Helper()
	dir := inTempDir(t)
	lf := `{"version":1,"servers":{"echo-server":{"version":"0.9.0","integrity":"sha512-x","transport":"stdio","resolved":"","installedAt":"2026-01-01T00:00:00Z"}}}`
	if err := os.WriteFile(filepath.Join(dir, "pharos.lock"), []byte(lf), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeRunningDaemon plants a daemon.pid + daemon.json pair that Status()
// reports as running. The PID is this test process (always alive); the
// per-server PID stays 0 so the process-memory read is skipped.
func fakeRunningDaemon(t *testing.T) {
	t.Helper()
	home := isolateHome(t)
	dir := filepath.Join(home, ".pharos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pid := strconv.Itoa(os.Getpid())
	// NOTE: no trailing newline — readDaemonPID parses the file with
	// strconv.Atoi over the whole contents.
	if err := os.WriteFile(filepath.Join(dir, "daemon.pid"), []byte(pid), 0o600); err != nil {
		t.Fatal(err)
	}
	state := `{
  "pid": ` + pid + `,
  "startedAt": "2026-01-01T00:00:00Z",
  "servers": {
    "echo-server": {"state": "running", "pid": 0, "port": 8765, "startedAt": "2026-01-01T00:00:00Z", "lastActivity": "2026-01-01T00:00:00Z", "idleTimeout": 30}
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "daemon.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeStopDaemon plants a daemon.pid pointing at a real throwaway process so
// `daemon stop` can terminate it for real. The process is started through a
// shell background job so it is reparented to init — a direct child would
// linger as a zombie after SIGTERM and isProcessAlive (signal 0) would keep
// reporting it alive. Unix only (process signaling).
func fakeStopDaemon(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("daemon stop contract row uses process signaling; unix only")
	}
	home := isolateHome(t)
	dir := filepath.Join(home, ".pharos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Redirect the job's stdout so it does not hold the capture pipe open
	// (Output() would otherwise block until the sleep exits), and use a
	// generous runtime so the pid is still alive when daemon stop runs.
	out, err := exec.Command("sh", "-c", "sleep 60 >/dev/null 2>&1 & echo $!").Output()
	if err != nil {
		t.Fatalf("spawn dummy daemon process: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 0 {
		t.Fatalf("parse dummy daemon pid from %q: %v", string(out), err)
	}
	t.Cleanup(func() {
		if proc, perr := os.FindProcess(pid); perr == nil {
			_ = proc.Kill()
		}
	})
	// NOTE: no trailing newline — readDaemonPID parses with strconv.Atoi.
	if err := os.WriteFile(filepath.Join(dir, "daemon.pid"), []byte(strconv.Itoa(pid)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fakeCredentials plants a credentials file so whoami's auth.Load succeeds.
// Requires a prior contractRegistry call (shares its isolated home).
func fakeCredentials(t *testing.T) {
	t.Helper()
	dir := filepath.Join(contractHome(t), ".pharos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	creds := `{"token":"test-token","stored_at":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fakeStorePackage plants a minimal installed-package directory so `remove`
// has something to remove (remove exits 1 when nothing was removed).
func fakeStorePackage(t *testing.T) {
	t.Helper()
	home := isolateHome(t)
	pkgDir := filepath.Join(home, ".pharos", "store", "echo-srv", "1.0.0")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := `{"name":"echo-srv","version":"1.0.0","transport":"stdio"}`
	if err := os.WriteFile(filepath.Join(pkgDir, "pharos.json"), []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}
}

// =============================================================
// The contract table
// =============================================================

// TestAgentContractTable is the formal agent contract: every command must
// complete under PHAROS_NON_INTERACTIVE=1 without hanging, guidance errors
// must name the fixing flag, and JSON-requesting commands must emit valid
// JSON. Documented exceptions carry a skip reason. No external network is
// ever contacted — registry rows use the localhost contractRegistry.
func TestAgentContractTable(t *testing.T) {
	cases := []contractCase{
		// ── Discovery / info (registry rows use a localhost stand-in) ──
		{
			name:    "search runs non-interactively against the registry",
			args:    []string{"search", "echo", "--json"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { pointCLIAtRegistry(t, startTestRegistry(t, &[]url.Values{}).URL) },
			jsonOut: true,
			wantOut: []string{"echo-stdio"},
		},
		{
			name:    "info runs non-interactively and emits JSON",
			args:    []string{"info", "echo-server", "--json"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { contractRegistry(t) },
			jsonOut: true,
			wantOut: []string{"echo-server"},
		},
		{
			name:    "health emits JSON under PHAROS_JSON env",
			args:    []string{"health"},
			env:     map[string]string{"PHAROS_JSON": "1"},
			setup:   func(t *testing.T) { contractRegistry(t) },
			jsonOut: true,
		},
		{
			name:    "doctor emits JSON and completes offline checks",
			args:    []string{"doctor", "--json"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { contractRegistry(t) },
			jsonOut: true,
			wantOut: []string{"healthy"},
		},
		{
			name:    "whoami emits JSON with credentials present",
			args:    []string{"whoami", "--json"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { contractRegistry(t); fakeCredentials(t) },
			jsonOut: true,
			wantOut: []string{"tester"},
		},
		{
			name:    "import completes with no clients detected",
			args:    []string{"import", "--json"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { contractRegistry(t) },
			jsonOut: true,
			wantOut: []string{`"resolved": 0`},
		},
		{
			name:  "import resolves a detected generic client config",
			args:  []string{"import", "--json"},
			env:   map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup: func(t *testing.T) { contractRegistry(t); inTempDir(t); fakeGenericClient(t) },
			wantOut: []string{
				`"status": "resolved"`,
			},
			jsonOut: true,
		},
		{
			name:  "update reports an up-to-date server as JSON",
			args:  []string{"update", "--json"},
			env:   map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup: func(t *testing.T) { contractRegistry(t); fakeLockfile(t) },
			wantOut: []string{
				`"up_to_date": 1`,
				`"action": "up_to_date"`,
			},
			jsonOut: true,
		},
		{
			name:  "update --dry-run emits update_available JSON",
			args:  []string{"update", "--dry-run", "--json"},
			env:   map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup: func(t *testing.T) { contractRegistry(t); fakeLockfileStale(t) },
			wantOut: []string{
				`"dry_run": true`,
				`"action": "update_available"`,
			},
			jsonOut: true,
		},

		// ── Package lifecycle ──
		{
			name:    "init under NON_INTERACTIVE without --yes aborts with guidance naming the fix",
			args:    []string{"init"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { isolateHome(t); inTempDir(t) },
			wantErr: "init requires --yes or PHAROS_ASSUME_YES=1 in non-interactive mode",
		},
		{
			name: "init --yes completes non-interactively",
			args: []string{"init", "--yes"},
			env:  map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup: func(t *testing.T) {
				isolateHome(t)
				inTempDir(t)
			},
			wantOut: []string{"pharos.json"},
		},
		{
			name: "init honors PHAROS_ASSUME_YES without NON_INTERACTIVE",
			args: []string{"init"},
			env:  map[string]string{"PHAROS_ASSUME_YES": "1"},
			setup: func(t *testing.T) {
				isolateHome(t)
				inTempDir(t)
			},
			wantOut: []string{"pharos.json"},
		},
		{
			name:    "purge under NON_INTERACTIVE without yes aborts with guidance (destructive)",
			args:    []string{"purge", "echo-server", "--version", "1.0.0"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { contractRegistry(t) },
			wantErr: "purge requires --yes or PHAROS_ASSUME_YES=1 in non-interactive mode",
		},
		{
			name:    "purge honors PHAROS_ASSUME_YES as confirmation",
			args:    []string{"purge", "echo-server", "--version", "1.0.0"},
			env:     map[string]string{"PHAROS_ASSUME_YES": "1"},
			setup:   func(t *testing.T) { contractRegistry(t) },
			wantOut: []string{"Purged echo-server@1.0.0"},
		},
		{
			name:    "unpublish under NON_INTERACTIVE without yes aborts with guidance",
			args:    []string{"unpublish", "echo-server", "--version", "1.0.0"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { contractRegistry(t) },
			wantErr: "unpublish requires --yes or PHAROS_ASSUME_YES=1 in non-interactive mode",
		},
		{
			name:    "unpublish honors PHAROS_ASSUME_YES as confirmation",
			args:    []string{"unpublish", "echo-server", "--version", "1.0.0"},
			env:     map[string]string{"PHAROS_ASSUME_YES": "1"},
			setup:   func(t *testing.T) { contractRegistry(t) },
			wantOut: []string{"Unpublished echo-server@1.0.0"},
		},
		{
			name:    "republish emits JSON",
			args:    []string{"republish", "echo-server", "--version", "1.0.0", "--json"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { contractRegistry(t) },
			wantOut: []string{`"status": "active"`},
			jsonOut: true,
		},
		{
			name: "package creates a tarball non-interactively",
			args: []string{"package"},
			env:  map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup: func(t *testing.T) {
				isolateHome(t)
				inTempDir(t)
				contractManifest(t)
			},
			wantOut: []string{"Created:"},
		},
		{
			name: "publish --dry-run validates offline",
			args: []string{"publish", "--dry-run"},
			env:  map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup: func(t *testing.T) {
				isolateHome(t)
				inTempDir(t)
				contractManifest(t)
			},
			wantOut: []string{"Dry-run validation OK"},
		},
		{
			name: "lock writes the lockfile for a dependency-free manifest",
			args: []string{"lock"},
			env:  map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup: func(t *testing.T) {
				isolateHome(t)
				inTempDir(t)
				contractManifest(t)
			},
			wantOut: []string{"lockfile"},
		},

		// ── Local management ──
		{
			name:    "list runs non-interactively on an empty store",
			args:    []string{"list"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { isolateHome(t) },
			wantOut: []string{"No packages installed."},
		},
		{
			name:    "list --json emits an empty JSON array",
			args:    []string{"list", "--json"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { isolateHome(t) },
			wantOut: []string{"[]"},
			jsonOut: true,
		},
		{
			name:    "list --running completes non-interactively",
			args:    []string{"list", "--running"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { isolateHome(t) },
			wantOut: []string{"No packages installed."},
		},
		{
			name:  "remove deletes a planted store package",
			args:  []string{"remove", "echo-srv"},
			env:   map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup: fakeStorePackage,
			wantOut: []string{
				"Removed bookmark metadata",
				"Removed:",
			},
		},
		{
			name:    "stop of an unknown server reports without hanging",
			args:    []string{"stop", "no-such-server"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { isolateHome(t) },
			wantOut: []string{"Error"},
		},
		{
			name:    "start of an uninstalled server reports without hanging",
			args:    []string{"start", "no-such-server"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { isolateHome(t) },
			wantOut: []string{"not installed"},
		},

		// ── Daemon (local process supervisor) ──
		{
			name:    "daemon status reports not-running without a daemon",
			args:    []string{"daemon", "status"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { isolateHome(t) },
			wantOut: []string{"Daemon is not running."},
		},
		{
			name:    "daemon status --json emits running:false when down",
			args:    []string{"daemon", "status", "--json"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { isolateHome(t) },
			wantOut: []string{`"running": false`},
			jsonOut: true,
		},
		{
			name:    "daemon status --json reflects a running daemon",
			args:    []string{"daemon", "status", "--json"},
			env:     map[string]string{"PHAROS_JSON": "1"},
			setup:   fakeRunningDaemon,
			wantOut: []string{`"running": true`, "echo-server"},
			jsonOut: true,
		},
		{
			name:    "daemon log reports a missing log without hanging",
			args:    []string{"daemon", "log"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { isolateHome(t) },
			wantOut: []string{"No daemon log found"},
		},
		{
			name:    "daemon autostart status view is non-interactive",
			args:    []string{"daemon", "autostart"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { isolateHome(t) },
			wantOut: []string{"autostart"},
		},
		{
			name:    "daemon stop terminates the planted daemon process",
			args:    []string{"daemon", "stop"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   fakeStopDaemon,
			wantOut: []string{"daemon stopped"},
		},

		// ── Config / auth / system ──
		{
			name:    "config get completes non-interactively",
			args:    []string{"config", "registry"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { isolateHome(t) },
			wantOut: []string{"registry:"},
		},
		{
			name:    "config get --json emits key/value JSON",
			args:    []string{"config", "registry", "--json"},
			env:     map[string]string{"PHAROS_JSON": "1"},
			setup:   func(t *testing.T) { isolateHome(t) },
			wantOut: []string{`"key": "registry"`},
			jsonOut: true,
		},
		{
			name:    "config set --json emits saved:true",
			args:    []string{"config", "registry", "http://127.0.0.1:1", "--json"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { isolateHome(t) },
			wantOut: []string{`"saved": true`},
			jsonOut: true,
		},
		{
			name:    "config list-clients completes non-interactively",
			args:    []string{"config", "list-clients"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { isolateHome(t) },
			wantOut: []string{"BUILT-IN"},
		},
		{
			name:    "login under NON_INTERACTIVE aborts the browser flow with guidance",
			args:    []string{"login"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { isolateHome(t) },
			wantErr: "login requires --manual",
		},
		{
			name: "oauth configure completes against the registry",
			args: []string{"oauth", "configure", "echo-server", "--auth-url", "https://example.test/authorize", "--client-id", "abc"},
			env:  map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup: func(t *testing.T) {
				contractRegistry(t)
			},
			wantOut: []string{"OAuth configured successfully"},
		},
		{
			name:  "completion generates a script non-interactively",
			args:  []string{"completion", "bash"},
			env:   map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup: func(t *testing.T) { isolateHome(t) },
			// Cobra's built-in completion command writes to the os.Stdout
			// handle it captures at package init, which predates this
			// harness's pipe swap — in-process capture yields
			// "write |1: file already closed". The command IS the
			// non-interactive path by definition (pure stdout script).
			skip: "contract exception: cobra builtin binds os.Stdout at init; non-interactive by definition (pure stdout script)",
		},
		{
			name:    "version runs non-interactively",
			args:    []string{"version"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { isolateHome(t) },
			wantOut: []string{"pharos version"},
		},
		{
			name:    "version --json emits valid JSON",
			args:    []string{"version", "--json"},
			env:     map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
			setup:   func(t *testing.T) { isolateHome(t) },
			wantOut: []string{`"version"`},
			jsonOut: true,
		},

		// ── Documented exceptions (cannot run in-process; behavior is
		// covered by their dedicated suites) ──
		{
			name: "install downloads registry artifacts (covered by install_test.go)",
			args: []string{"install", "echo-server"},
			skip: "contract exception: downloads + verifies tarballs from the registry; artifact pipeline covered by install_test.go",
		},
		{
			name: "daemon start re-execs the binary as a background daemon",
			args: []string{"daemon", "start"},
			skip: "contract exception: re-execs os.Executable() as a detached background daemon; covered by daemon integration flows",
		},
		{
			name: "daemon restart spawns a background daemon",
			args: []string{"daemon", "restart"},
			skip: "contract exception: stop + start re-exec; covered by daemon integration flows",
		},
		{
			name: "publish uploads a tarball to the registry",
			args: []string{"publish"},
			skip: "contract exception: 4-phase upload to the registry; the --dry-run row above covers the offline validation path",
		},
	}

	for _, cc := range cases {
		t.Run(cc.name, func(t *testing.T) {
			runContractCase(t, cc)
		})
	}
}

// =============================================================
// llm.txt golden file
// =============================================================

// TestLLMTxtGoldenFile byte-compares the generated reference against the
// committed docs/llm.txt. If this fails after a command/flag/annotation
// change, regenerate with: go run . llmtxt
func TestLLMTxtGoldenFile(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("..", "docs", "llm.txt"))
	if err != nil {
		t.Fatalf("read docs/llm.txt: %v", err)
	}
	got := GenerateLLMTxt()
	// Git may check the golden file out with CRLF on windows (autocrlf);
	// compare content, not checkout line endings.
	normalized := strings.ReplaceAll(string(want), "\r\n", "\n")
	if normalized != got {
		t.Errorf("docs/llm.txt is stale — regenerate with `go run . llmtxt`.\nDiff hint: want %d bytes (committed), got %d bytes (generated)", len(want), len(got))
	}
}

// TestLLMTxtDeterministic generates the reference twice and requires
// byte-identical output (stable ordering is part of the contract).
func TestLLMTxtDeterministic(t *testing.T) {
	a := GenerateLLMTxt()
	b := GenerateLLMTxt()
	if a != b {
		t.Error("GenerateLLMTxt() is not deterministic between calls")
	}
}

// TestLLMTxtCoversEveryCommand walks the real tree and asserts every
// non-hidden command appears in the generated reference.
func TestLLMTxtCoversEveryCommand(t *testing.T) {
	got := GenerateLLMTxt()
	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		if c.Hidden {
			return
		}
		if !strings.Contains(got, "# COMMAND: "+path+"\n") {
			t.Errorf("llm.txt is missing section for %q", path)
		}
		for _, sub := range c.Commands() {
			walk(sub, path+" "+sub.Name())
		}
	}
	walk(rootCmd, "pharos")
}
