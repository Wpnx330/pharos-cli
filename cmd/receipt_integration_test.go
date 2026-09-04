package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/receipt"
)

// =============================================================
// W1.2 deterministic install receipts — cmd integration
// =============================================================
//
// These tests drive the real install / remove / update commands through
// runContract against a localhost stand-in registry and assert the emitted
// receipt JSON: real before/after SHA-256 of the planted client config,
// correct server actions, backup_path on the update path, a lockfile entry
// on install/update, and none on remove. Everything is offline.
//
// PHAROS_WINDOWS_USERS_ROOT is pointed at an empty dir so client detection
// never walks the live /mnt/c/Users tree on WSL dev machines — only the
// isolated HOME's Generic MCP client exists.

// receiptRegistry is a stand-in PHAROS registry serving echo-server@1.0.0
// as a kind-1 remote (publisher endpoint) package — the only kind whose
// install/update needs no tarball download, so the full mutation path runs
// offline. It also isolates HOME and points the CLI's config at itself;
// call it before any helper that expects the isolated home.
func receiptRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	home := isolateHome(t)
	// Hermetic client detection: no Windows profiles to walk.
	empty := t.TempDir()
	t.Setenv("PHAROS_WINDOWS_USERS_ROOT", empty)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/v1/packages/echo-server") {
			_, _ = io.WriteString(w, `{
				"name": "echo-server",
				"title": "Echo Server",
				"description": "receipt test package",
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
							"transport": "http-sse",
							"endpoint": "https://echo.example.test/sse",
							"capabilities": ["tools"]
						}
					}
				]
			}`)
			return
		}
		http.NotFound(w, r)
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

// fakeGenericClientOther plants a Generic MCP config that references a
// DIFFERENT server, so an install of echo-server records "added" (not
// "replaced") while the client itself is detected.
func fakeGenericClientOther(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(contractHome(t), ".config", "mcp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"mcpServers": {"other": {"command": "node", "args": ["other.js"]}}}`
	path := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeClaudeDesktopConfig plants a Claude Desktop config (the remote-skip
// candidate) under the isolated home and returns its path. The path must
// match what clientconfig resolves on this GOOS: %APPDATA%\Claude on
// windows, ~/.config/Claude elsewhere (isolateHome points APPDATA at the
// isolated home's AppData/Roaming).
func fakeClaudeDesktopConfig(t *testing.T) string {
	t.Helper()
	home := contractHome(t)
	dir := filepath.Join(home, ".config", "Claude")
	if runtime.GOOS == "windows" {
		dir = filepath.Join(home, "AppData", "Roaming", "Claude")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "claude_desktop_config.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeReceiptConfig writes the isolated home's pharos config.json pointing
// at srv and returns the server (shared tail of the receiptRegistry* helpers).
func finishReceiptRegistry(t *testing.T, home string, srv *httptest.Server) *httptest.Server {
	t.Helper()
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

// receiptRegistryTwo is receiptRegistry plus a second stale-able package,
// beta-server (latest 2.0.0), so one update run can rewrite the SAME
// shared client config twice — the setup where a naive per-rewrite .bak
// would overwrite the pre-run generation with intermediate content.
func receiptRegistryTwo(t *testing.T) *httptest.Server {
	t.Helper()
	home := isolateHome(t)
	empty := t.TempDir()
	t.Setenv("PHAROS_WINDOWS_USERS_ROOT", empty)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/packages/echo-server"):
			_, _ = io.WriteString(w, `{
				"name": "echo-server",
				"dist_tags": {"latest": "1.0.0"},
				"versions": [
					{
						"version": "1.0.0",
						"status": "active",
						"created_at": "2026-01-01T00:00:00Z",
						"manifest": {
							"name": "echo-server",
							"version": "1.0.0",
							"transport": "http-sse",
							"endpoint": "https://echo.example.test/sse",
							"capabilities": ["tools"]
						}
					}
				]
			}`)
		case strings.HasPrefix(r.URL.Path, "/v1/packages/beta-server"):
			_, _ = io.WriteString(w, `{
				"name": "beta-server",
				"dist_tags": {"latest": "2.0.0"},
				"versions": [
					{
						"version": "2.0.0",
						"status": "active",
						"created_at": "2026-01-01T00:00:00Z",
						"manifest": {
							"name": "beta-server",
							"version": "2.0.0",
							"transport": "http-sse",
							"endpoint": "https://beta.example.test/sse",
							"capabilities": ["tools"]
						}
					},
					{
						"version": "1.0.0",
						"status": "active",
						"created_at": "2026-01-01T00:00:00Z",
						"manifest": {
							"name": "beta-server",
							"version": "1.0.0",
							"transport": "http-sse",
							"endpoint": "https://beta.example.test/sse",
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
	return finishReceiptRegistry(t, home, srv)
}

// receiptRegistryWithDep is receiptRegistry with echo-server declaring a
// stdio dependency (dep-server, runtime npx — no tarball needed, the
// registry answers 404 for tarballs and install persists the launch line).
// The primary is kind-1 remote, so Claude Desktop is SKIPPED for it while
// the stdio dependency still writes Desktop — the exact shape in which
// dependency config writes were previously invisible to receipts.
func receiptRegistryWithDep(t *testing.T) *httptest.Server {
	t.Helper()
	home := isolateHome(t)
	empty := t.TempDir()
	t.Setenv("PHAROS_WINDOWS_USERS_ROOT", empty)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/packages/echo-server"):
			_, _ = io.WriteString(w, `{
				"name": "echo-server",
				"dist_tags": {"latest": "1.0.0"},
				"versions": [
					{
						"version": "1.0.0",
						"status": "active",
						"created_at": "2026-01-01T00:00:00Z",
						"manifest": {
							"name": "echo-server",
							"version": "1.0.0",
							"transport": "http-sse",
							"endpoint": "https://echo.example.test/sse",
							"capabilities": ["tools"],
							"dependencies": [
								{"name": "dep-server", "version": "^1.0.0"}
							]
						}
					}
				]
			}`)
		case strings.HasPrefix(r.URL.Path, "/v1/packages/dep-server"):
			_, _ = io.WriteString(w, `{
				"name": "dep-server",
				"dist_tags": {"latest": "1.0.0"},
				"versions": [
					{
						"version": "1.0.0",
						"status": "active",
						"created_at": "2026-01-01T00:00:00Z",
						"manifest": {
							"name": "dep-server",
							"version": "1.0.0",
							"transport": "stdio",
							"runtime": "npx",
							"package": "@scope/dep-server",
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
	return finishReceiptRegistry(t, home, srv)
}

// sha256Of hashes a string the same way receipt.FileHash does.
func sha256Of(t *testing.T, s string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// parseReceipt requires stdout to be exactly one valid receipt JSON
// document (json.Unmarshal fails on any trailing garbage).
func parseReceipt(t *testing.T, stdout string) receipt.Receipt {
	t.Helper()
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		t.Fatal("stdout is empty — no receipt JSON emitted")
	}
	var r receipt.Receipt
	if err := json.Unmarshal([]byte(trimmed), &r); err != nil {
		t.Fatalf("stdout is not a single receipt JSON document: %v\n%s", err, trimmed)
	}
	return r
}

// findFileChange returns the FileChange for a path, failing if absent.
func findFileChange(t *testing.T, r receipt.Receipt, path string) receipt.FileChange {
	t.Helper()
	for _, f := range r.Files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("receipt has no FileChange for %s:\n%+v", path, r.Files)
	return receipt.FileChange{}
}

// assertTimestamp validates the RFC3339 UTC stamp.
func assertTimestamp(t *testing.T, r receipt.Receipt) {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, r.Timestamp)
	if err != nil {
		t.Fatalf("timestamp %q is not RFC3339: %v", r.Timestamp, err)
	}
	if _, offset := ts.Zone(); offset != 0 {
		t.Errorf("timestamp %q is not UTC", r.Timestamp)
	}
}

// TestInstallReceiptJSON drives a full offline kind-1 install under
// PHAROS_JSON=1 and verifies the receipt: config file modified with exact
// before/after hashes, server "added", lockfile entry present as "created",
// and stdout holding exactly one JSON document.
func TestInstallReceiptJSON(t *testing.T) {
	receiptRegistry(t)
	cfgPath := fakeGenericClientOther(t)
	dir := inTempDir(t)

	beforeBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeSHA := sha256Of(t, string(beforeBytes))

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"install", "echo-server", "--client", "generic")

	r := parseReceipt(t, stdout)
	assertTimestamp(t, r)
	if r.Command != "install" {
		t.Errorf("command = %q, want install", r.Command)
	}
	if r.Package != "echo-server" || r.Version != "1.0.0" {
		t.Errorf("package/version = %q/%q, want echo-server/1.0.0", r.Package, r.Version)
	}

	fc := findFileChange(t, r, cfgPath)
	if fc.Action != "modified" {
		t.Errorf("config action = %q, want modified", fc.Action)
	}
	if fc.Client != "Generic MCP" {
		t.Errorf("config client = %q, want Generic MCP", fc.Client)
	}
	if fc.BeforeSHA != beforeSHA {
		t.Errorf("before_sha256 = %q, want %q (the planted file's real hash)", fc.BeforeSHA, beforeSHA)
	}
	afterBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := sha256Of(t, string(afterBytes)); fc.AfterSHA != want {
		t.Errorf("after_sha256 = %q, want %q (the file's hash after install)", fc.AfterSHA, want)
	}
	if fc.AfterSHA == fc.BeforeSHA {
		t.Error("after hash equals before hash — config was not rewritten")
	}
	if fc.Backup != "" {
		t.Errorf("install must not take a .bak, got backup_path %q", fc.Backup)
	}

	if len(r.Servers) != 1 {
		t.Fatalf("servers = %+v, want exactly one entry", r.Servers)
	}
	sc := r.Servers[0]
	if sc.Client != "Generic MCP" || sc.Name != "echo-server" || sc.Action != "added" {
		t.Errorf("server change = %+v, want {Generic MCP echo-server added}", sc)
	}

	lockPath := filepath.Join(dir, "pharos.lock")
	lf := findFileChange(t, r, lockPath)
	if lf.Action != "created" {
		t.Errorf("lockfile action = %q, want created (no lockfile before)", lf.Action)
	}
	if lf.Client != "lockfile" {
		t.Errorf("lockfile client = %q, want lockfile", lf.Client)
	}
	if lf.BeforeSHA != "" {
		t.Errorf("lockfile before_sha256 = %q, want empty (file did not exist)", lf.BeforeSHA)
	}
	lfBytes, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := sha256Of(t, string(lfBytes)); lf.AfterSHA != want {
		t.Errorf("lockfile after_sha256 = %q, want %q", lf.AfterSHA, want)
	}
}

// TestRemoveReceiptJSON removes a planted server and verifies: config file
// modified with real hashes, server "removed", receipt version filled from
// the lockfile, and NO lockfile entry in the files list.
func TestRemoveReceiptJSON(t *testing.T) {
	receiptRegistry(t)
	fakeGenericClient(t)
	fakeLockfile(t)
	cfgPath := filepath.Join(contractHome(t), ".config", "mcp", "mcp.json")

	beforeBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeSHA := sha256Of(t, string(beforeBytes))

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"remove", "echo-server")

	r := parseReceipt(t, stdout)
	assertTimestamp(t, r)
	if r.Command != "remove" {
		t.Errorf("command = %q, want remove", r.Command)
	}
	if r.Package != "echo-server" {
		t.Errorf("package = %q, want echo-server", r.Package)
	}
	if r.Version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0 (from lockfile)", r.Version)
	}

	fc := findFileChange(t, r, cfgPath)
	if fc.Action != "modified" {
		t.Errorf("config action = %q, want modified", fc.Action)
	}
	if fc.BeforeSHA != beforeSHA {
		t.Errorf("before_sha256 = %q, want %q", fc.BeforeSHA, beforeSHA)
	}
	afterBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := sha256Of(t, string(afterBytes)); fc.AfterSHA != want {
		t.Errorf("after_sha256 = %q, want %q", fc.AfterSHA, want)
	}
	if strings.Contains(string(afterBytes), "echo-server") {
		t.Errorf("echo-server still present in config after remove:\n%s", afterBytes)
	}

	if len(r.Servers) != 1 {
		t.Fatalf("servers = %+v, want exactly one entry", r.Servers)
	}
	sc := r.Servers[0]
	if sc.Client != "Generic MCP" || sc.Name != "echo-server" || sc.Action != "removed" {
		t.Errorf("server change = %+v, want {Generic MCP echo-server removed}", sc)
	}

	for _, f := range r.Files {
		if strings.HasSuffix(f.Path, "pharos.lock") {
			t.Errorf("remove receipts must NOT contain a lockfile entry, got %+v", f)
		}
	}
}

// TestUpdateReceiptJSONWithBackup drives the update path against a stale
// lockfile and verifies: config rewritten with backup_path set to the .bak,
// server "replaced", and the lockfile bump entry present as "modified".
func TestUpdateReceiptJSONWithBackup(t *testing.T) {
	receiptRegistry(t)
	fakeGenericClient(t)
	fakeLockfileStale(t)
	cfgPath := filepath.Join(contractHome(t), ".config", "mcp", "mcp.json")

	// .bak content = the pre-update config (single-generation backup).
	beforeBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeSHA := sha256Of(t, string(beforeBytes))

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"update", "--json")

	r := parseReceipt(t, stdout)
	assertTimestamp(t, r)
	if r.Command != "update" {
		t.Errorf("command = %q, want update", r.Command)
	}
	if r.Package != "echo-server" || r.Version != "1.0.0" {
		t.Errorf("package/version = %q/%q, want echo-server/1.0.0", r.Package, r.Version)
	}

	fc := findFileChange(t, r, cfgPath)
	if fc.Action != "modified" {
		t.Errorf("config action = %q, want modified", fc.Action)
	}
	if fc.BeforeSHA != beforeSHA {
		t.Errorf("before_sha256 = %q, want %q", fc.BeforeSHA, beforeSHA)
	}
	afterBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := sha256Of(t, string(afterBytes)); fc.AfterSHA != want {
		t.Errorf("after_sha256 = %q, want %q", fc.AfterSHA, want)
	}
	wantBackup := cfgPath + ".bak"
	if fc.Backup != wantBackup {
		t.Errorf("backup_path = %q, want %q", fc.Backup, wantBackup)
	}
	bakBytes, err := os.ReadFile(wantBackup)
	if err != nil {
		t.Fatalf(".bak missing: %v", err)
	}
	if sha256Of(t, string(bakBytes)) != beforeSHA {
		t.Error(".bak does not hold the pre-update config bytes")
	}

	if len(r.Servers) != 1 {
		t.Fatalf("servers = %+v, want exactly one entry", r.Servers)
	}
	sc := r.Servers[0]
	if sc.Client != "Generic MCP" || sc.Name != "echo-server" || sc.Action != "replaced" {
		t.Errorf("server change = %+v, want {Generic MCP echo-server replaced}", sc)
	}

	// The lockfile bump entry: fakeLockfileStale wrote it in cwd.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(cwd, "pharos.lock")
	lf := findFileChange(t, r, lockPath)
	if lf.Action != "modified" {
		t.Errorf("lockfile action = %q, want modified (lockfile existed)", lf.Action)
	}
	if lf.BeforeSHA == "" {
		t.Error("lockfile before_sha256 empty, want the stale lockfile's hash")
	}
	lfBytes, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := sha256Of(t, string(lfBytes)); lf.AfterSHA != want {
		t.Errorf("lockfile after_sha256 = %q, want %q", lf.AfterSHA, want)
	}
}

// TestUpdateHumanReceiptSummary verifies the human (non-JSON) path prints
// the receipt summary after the usual progress output.
func TestUpdateHumanReceiptSummary(t *testing.T) {
	receiptRegistry(t)
	fakeGenericClient(t)
	fakeLockfileStale(t)

	_, combined := runContract(t,
		map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
		"update")
	for _, want := range []string{
		"✓ Updated echo-server@1.0.0",
		"  · ",
		"  modified",
	} {
		if !strings.Contains(combined, want) {
			t.Errorf("human receipt summary missing %q in:\n%s", want, combined)
		}
	}
}

// TestInstallHumanReceiptSummary verifies the human install path ends with
// the receipt summary one-liner and a per-file bullet.
func TestInstallHumanReceiptSummary(t *testing.T) {
	receiptRegistry(t)
	cfgPath := fakeGenericClientOther(t)
	inTempDir(t)

	_, combined := runContract(t,
		map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
		"install", "echo-server", "--client", "generic")
	for _, want := range []string{
		"✓ Installed echo-server@1.0.0",
		"  · " + cfgPath,
		"  modified  sha256 ",
	} {
		if !strings.Contains(combined, want) {
			t.Errorf("human receipt summary missing %q in:\n%s", want, combined)
		}
	}
}

// TestInstallReceiptSkippedClientAbsent proves "absence = untouched": a
// client whose write is genuinely attempted and refused (Claude Desktop
// cannot take a remote URL — the real SkipError path, since BOTH clients
// are selected) produces no receipt rows at all, while the client that
// was written still gets its rows.
func TestInstallReceiptSkippedClientAbsent(t *testing.T) {
	receiptRegistry(t)
	cfgPath := fakeGenericClientOther(t)
	desktopPath := fakeClaudeDesktopConfig(t)
	inTempDir(t)

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"install", "echo-server", "--client", "claude-desktop,generic")

	r := parseReceipt(t, stdout)
	for _, f := range r.Files {
		if f.Path == desktopPath {
			t.Errorf("skipped client must produce no FileChange, got %+v", f)
		}
	}
	for _, s := range r.Servers {
		if s.Client == "Claude Desktop" {
			t.Errorf("skipped client must produce no ServerChange, got %+v", s)
		}
	}
	// The written client is recorded normally — the skip must not
	// suppress receipts for the other selected clients.
	fc := findFileChange(t, r, cfgPath)
	if fc.Action != "modified" {
		t.Errorf("generic action = %q, want modified", fc.Action)
	}
	found := false
	for _, s := range r.Servers {
		if s.Client == "Generic MCP" && s.Name == "echo-server" && s.Action == "added" {
			found = true
		}
	}
	if !found {
		t.Errorf("servers = %+v, want a {Generic MCP echo-server added} entry", r.Servers)
	}
	if r.Status != "ok" {
		t.Errorf("status = %q, want ok (a skip is not a failure)", r.Status)
	}
}

// TestUpdateReceiptSharedConfigBakHoldsPreRun: TWO stale servers sharing
// ONE client config in a single update run. The second rewrite must not
// re-take the .bak — that would overwrite the pre-run generation with the
// intermediate content of the first rewrite, while the receipt's
// before_sha256 claims the pre-run bytes. The .bak must stay
// byte-identical to the receipt's before_sha256.
func TestUpdateReceiptSharedConfigBakHoldsPreRun(t *testing.T) {
	receiptRegistryTwo(t)
	// One shared Generic MCP config referencing BOTH stale servers.
	mcpDir := filepath.Join(contractHome(t), ".config", "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(mcpDir, "mcp.json")
	cfg := `{"mcpServers": {
		"echo-server": {"url": "https://echo.example.test/sse"},
		"beta-server": {"url": "https://beta.example.test/sse"}
	}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	// Stale lockfile pinning both below their registry latest (cwd is a
	// fresh temp dir, so the lockfile never touches the repo).
	inTempDir(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(cwd, "pharos.lock")
	lf := `{"version":1,"servers":{
		"echo-server":{"version":"0.9.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z"},
		"beta-server":{"version":"1.0.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z"}
	}}`
	if err := os.WriteFile(lockPath, []byte(lf), 0o644); err != nil {
		t.Fatal(err)
	}

	beforeBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeSHA := sha256Of(t, string(beforeBytes))

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"update", "--json")

	r := parseReceipt(t, stdout)
	assertTimestamp(t, r)
	if r.Status != "ok" {
		t.Errorf("status = %q, want ok (errors: %v)", r.Status, r.Errors)
	}
	if !strings.Contains(r.Package, "echo-server") || !strings.Contains(r.Package, "beta-server") {
		t.Errorf("package = %q, want both updated servers", r.Package)
	}

	fc := findFileChange(t, r, cfgPath)
	if fc.Action != "modified" {
		t.Errorf("config action = %q, want modified", fc.Action)
	}
	if fc.BeforeSHA != beforeSHA {
		t.Errorf("before_sha256 = %q, want %q (the planted pre-update file)", fc.BeforeSHA, beforeSHA)
	}
	// THE assertion: .bak holds the pre-run generation, byte-identical to
	// the receipt's before_sha256 — not the intermediate post-first-rewrite
	// content.
	bakBytes, err := os.ReadFile(cfgPath + ".bak")
	if err != nil {
		t.Fatalf(".bak missing: %v", err)
	}
	if sha256Of(t, string(bakBytes)) != beforeSHA {
		t.Error(".bak was overwritten with intermediate content by the second server's rewrite")
	}
	// One row per rewritten path — the shared config must not be listed twice.
	cfgRows := 0
	for _, f := range r.Files {
		if f.Path == cfgPath {
			cfgRows++
		}
	}
	if cfgRows != 1 {
		t.Errorf("shared config listed %d times in files, want exactly 1", cfgRows)
	}
	// Both server entries replaced in that one shared config.
	if len(r.Servers) != 2 {
		t.Fatalf("servers = %+v, want exactly two entries", r.Servers)
	}
	seen := map[string]bool{}
	for _, sc := range r.Servers {
		if sc.Client != "Generic MCP" || sc.Action != "replaced" {
			t.Errorf("server change = %+v, want {Generic MCP <name> replaced}", sc)
		}
		seen[sc.Name] = true
	}
	if !seen["echo-server"] || !seen["beta-server"] {
		t.Errorf("servers = %+v, want both echo-server and beta-server replaced", r.Servers)
	}

	// Lockfile bump entry present.
	lfc := findFileChange(t, r, lockPath)
	if lfc.Action != "modified" {
		t.Errorf("lockfile action = %q, want modified", lfc.Action)
	}
}

// TestInstallReceiptDepWritesRecorded: a primary kind-1 remote is SKIPPED
// on Claude Desktop, but its stdio dependency still writes Desktop. That
// dependency write (file row + ServerChange) must appear on the receipt —
// previously the file was modified with NO receipt row at all.
func TestInstallReceiptDepWritesRecorded(t *testing.T) {
	receiptRegistryWithDep(t)
	desktopPath := fakeClaudeDesktopConfig(t)
	inTempDir(t)

	beforeBytes, err := os.ReadFile(desktopPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeSHA := sha256Of(t, string(beforeBytes))

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"install", "echo-server", "--client", "claude-desktop")

	r := parseReceipt(t, stdout)
	assertTimestamp(t, r)
	if r.Status != "ok" {
		t.Errorf("status = %q, want ok (errors: %v)", r.Status, r.Errors)
	}

	// Primary remote on Desktop: real SkipError path → no echo-server rows.
	for _, s := range r.Servers {
		if s.Name == "echo-server" {
			t.Errorf("primary remote must be skipped on Desktop, got %+v", s)
		}
	}
	for _, f := range r.Files {
		if f.Path == desktopPath && f.Client != "Claude Desktop" {
			t.Errorf("unexpected desktop row %+v", f)
		}
	}

	// The dependency write IS recorded: Desktop file row + dep ServerChange.
	fc := findFileChange(t, r, desktopPath)
	if fc.Action != "modified" {
		t.Errorf("desktop action = %q, want modified", fc.Action)
	}
	if fc.BeforeSHA != beforeSHA {
		t.Errorf("desktop before_sha256 = %q, want %q (the planted file)", fc.BeforeSHA, beforeSHA)
	}
	afterBytes, err := os.ReadFile(desktopPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := sha256Of(t, string(afterBytes)); fc.AfterSHA != want {
		t.Errorf("desktop after_sha256 = %q, want %q", fc.AfterSHA, want)
	}
	if !strings.Contains(string(afterBytes), "dep-server") {
		t.Errorf("dep-server entry missing from Desktop config:\n%s", afterBytes)
	}
	if len(r.Servers) != 1 {
		t.Fatalf("servers = %+v, want exactly the dep entry", r.Servers)
	}
	sc := r.Servers[0]
	if sc.Client != "Claude Desktop" || sc.Name != "dep-server" || sc.Action != "added" {
		t.Errorf("server change = %+v, want {Claude Desktop dep-server added}", sc)
	}
}

// TestInstallReceiptDepWritesRecordedAlreadyInstalled: the same receipt
// duty on the "dependency already installed" branch of the dep loop —
// its config write must be recorded too.
func TestInstallReceiptDepWritesRecordedAlreadyInstalled(t *testing.T) {
	receiptRegistryWithDep(t)
	desktopPath := fakeClaudeDesktopConfig(t)
	// Plant the dependency as already installed in the store so the dep
	// loop takes the already-installed branch (which still writes configs).
	storeDir := filepath.Join(contractHome(t), ".pharos", "store", "dep-server", "1.0.0")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"name":"dep-server","version":"1.0.0","transport":"stdio","kind":3}`
	if err := os.WriteFile(filepath.Join(storeDir, ".pharos-installed.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	inTempDir(t)

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"install", "echo-server", "--client", "claude-desktop")

	r := parseReceipt(t, stdout)
	if r.Status != "ok" {
		t.Errorf("status = %q, want ok (errors: %v)", r.Status, r.Errors)
	}
	fc := findFileChange(t, r, desktopPath)
	if fc.Action != "modified" {
		t.Errorf("desktop action = %q, want modified", fc.Action)
	}
	found := false
	for _, sc := range r.Servers {
		if sc.Client == "Claude Desktop" && sc.Name == "dep-server" && sc.Action == "added" {
			found = true
		}
	}
	if !found {
		t.Errorf("servers = %+v, want {Claude Desktop dep-server added}", r.Servers)
	}
}

// TestInstallReceiptPartialOnConfigWriteFailure: a client config write
// failure marks the receipt "partial" with an error naming the client,
// while the config that could not be written records no FileChange
// (absence = untouched). The run continues — the lockfile row is present.
func TestInstallReceiptPartialOnConfigWriteFailure(t *testing.T) {
	receiptRegistry(t)
	cfgPath := fakeGenericClientOther(t)
	dir := inTempDir(t)

	// Force the config write to fail on every OS: park a DIRECTORY where
	// the config file lives — merges cannot write through it, and no
	// chmod is needed (windows ignores directory permission bits).
	if err := os.RemoveAll(cfgPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfgPath, 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"install", "echo-server", "--client", "generic")

	r := parseReceipt(t, stdout)
	assertTimestamp(t, r)
	if r.Status != "partial" {
		t.Errorf("status = %q, want partial", r.Status)
	}
	if len(r.Errors) != 1 {
		t.Fatalf("errors = %+v, want exactly one naming the client", r.Errors)
	}
	if !strings.Contains(r.Errors[0], "Generic MCP") {
		t.Errorf("error %q must name the failed client", r.Errors[0])
	}
	// absence = untouched: the failed config has no FileChange row.
	for _, f := range r.Files {
		if f.Path == cfgPath {
			t.Errorf("failed config must produce no FileChange, got %+v", f)
		}
	}
	// The run continued: the lockfile row is still recorded.
	lockPath := filepath.Join(dir, "pharos.lock")
	lf := findFileChange(t, r, lockPath)
	if lf.Action != "created" {
		t.Errorf("lockfile action = %q, want created (the run continued past the failure)", lf.Action)
	}
}

// TestUpdateReceiptEmittedOnLockfileSaveFailure: when the lockfile save
// fails after configs were rewritten and .baks taken, the built receipt
// must STILL be emitted — status "partial", the lockfile error recorded,
// and no lockfile row (the file was never modified). A bare return that
// drops the receipt would leave the config writes invisible.
func TestUpdateReceiptEmittedOnLockfileSaveFailure(t *testing.T) {
	receiptRegistry(t)
	fakeGenericClient(t)
	fakeLockfileStale(t)
	cfgPath := filepath.Join(contractHome(t), ".config", "mcp", "mcp.json")

	beforeBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeSHA := sha256Of(t, string(beforeBytes))

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(cwd, "pharos.lock")
	lockBefore, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	// Force the lockfile save to fail: read-only lockfile.
	if err := os.Chmod(lockPath, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(lockPath, 0o644) })
	// Probe: filesystems ignoring file permissions cannot inject this.
	if f, ferr := os.OpenFile(lockPath, os.O_WRONLY, 0); ferr == nil {
		_ = f.Close()
		_ = os.Chmod(lockPath, 0o644)
		t.Skip("filesystem ignores read-only file permissions; cannot force lockfile save failure")
	}

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"update", "--json")

	// THE assertion: a receipt IS emitted despite the save failure.
	r := parseReceipt(t, stdout)
	assertTimestamp(t, r)
	if r.Status != "partial" {
		t.Errorf("status = %q, want partial", r.Status)
	}
	if len(r.Errors) != 1 || !strings.Contains(r.Errors[0], "lockfile") {
		t.Errorf("errors = %+v, want one entry naming the lockfile failure", r.Errors)
	}

	// The config rewrite is still recorded with its .bak provenance.
	fc := findFileChange(t, r, cfgPath)
	if fc.Action != "modified" {
		t.Errorf("config action = %q, want modified", fc.Action)
	}
	if fc.BeforeSHA != beforeSHA {
		t.Errorf("before_sha256 = %q, want %q", fc.BeforeSHA, beforeSHA)
	}
	if fc.Backup != cfgPath+".bak" {
		t.Errorf("backup_path = %q, want %q", fc.Backup, cfgPath+".bak")
	}
	bakBytes, err := os.ReadFile(cfgPath + ".bak")
	if err != nil {
		t.Fatalf(".bak missing: %v", err)
	}
	if sha256Of(t, string(bakBytes)) != beforeSHA {
		t.Error(".bak does not hold the pre-update config bytes")
	}

	// The lockfile was never modified: no lockfile row, bytes unchanged.
	for _, f := range r.Files {
		if strings.HasSuffix(f.Path, "pharos.lock") {
			t.Errorf("lockfile row must be absent when the save failed, got %+v", f)
		}
	}
	lockAfter, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(lockAfter) != string(lockBefore) {
		t.Error("lockfile bytes changed despite the save failure")
	}
}
