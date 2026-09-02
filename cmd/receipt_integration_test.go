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
// client whose write is skipped (Claude Desktop cannot take a remote URL)
// produces no receipt rows at all.
func TestInstallReceiptSkippedClientAbsent(t *testing.T) {
	receiptRegistry(t)
	fakeGenericClientOther(t)
	inTempDir(t)

	// Plant a Claude Desktop config whose parent dir exists so it is
	// detected; kind-1 remote installs skip Desktop (Connectors, not JSON).
	desktopDir := filepath.Join(contractHome(t), ".config", "Claude")
	if err := os.MkdirAll(desktopDir, 0o755); err != nil {
		t.Fatal(err)
	}
	desktopPath := filepath.Join(desktopDir, "claude_desktop_config.json")
	if err := os.WriteFile(desktopPath, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"install", "echo-server", "--client", "generic")

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
}
