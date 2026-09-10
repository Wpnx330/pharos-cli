package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/canonical"
	"github.com/Wpnx330/pharos-cli/internal/install"
)

// =============================================================
// QA D-2 — `pharos update` must rewrite the canonical config
// =============================================================
//
// The lockfile gets the new version/integrity on update, but the canonical
// ~/.pharos/mcp.json used to keep the OLD version/integrity/command path —
// and binary-runtime commands embed the version directory, so daemon/try
// kept launching the stale binary. These tests drive the real update
// command (offline) and assert the canonical record + registered client
// configs both track the newly installed version.

// plantStaleCanonical writes a canonical config holding a stale record for
// the given server, shaped like a pre-fix update would leave behind.
func plantStaleCanonical(t *testing.T, name string, srv canonical.Server) {
	t.Helper()
	cfg := &canonical.Config{Servers: map[string]canonical.Server{name: srv}}
	if err := canonical.Save(cfg); err != nil {
		t.Fatal(err)
	}
}

// readCanonicalServer loads the canonical config and returns the named
// entry, failing the test when it is missing.
func readCanonicalServer(t *testing.T, name string) canonical.Server {
	t.Helper()
	cfg, err := canonical.Load()
	if err != nil {
		t.Fatal(err)
	}
	srv, ok := cfg.Servers[name]
	if !ok {
		t.Fatalf("server %q missing from canonical config:\n%+v", name, cfg.Servers)
	}
	return srv
}

// TestUpdateRewritesCanonicalConfigRemote: a kind-1 remote server updated
// 0.9.0 → 1.0.0. The canonical record must mirror the new version, carry
// the publisher URL (not the stale command line), and preserve the
// installedAt/enabled runtime state — plus a canonical row on the receipt.
func TestUpdateRewritesCanonicalConfigRemote(t *testing.T) {
	receiptRegistry(t)
	fakeGenericClient(t)
	fakeLockfileStale(t)
	canonPath := filepath.Join(contractHome(t), ".pharos", "mcp.json")
	plantStaleCanonical(t, "echo-server", canonical.Server{
		Transport:   "stdio",
		Command:     "node",
		Args:        []string{"old.js"},
		Package:     canonical.PackageInfo{Name: "echo-server", Version: "0.9.0", Integrity: "sha512-old", Source: "pharos"},
		Enabled:     false,
		InstalledAt: "2026-01-01T00:00:00Z",
	})

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"update", "--json")

	r := parseReceipt(t, stdout)
	if r.Status != "ok" {
		t.Errorf("status = %q, want ok (errors: %v)", r.Status, r.Errors)
	}
	// The canonical write is on the receipt (before/after hashes honest).
	fc := findFileChange(t, r, canonPath)
	if fc.Action != "modified" {
		t.Errorf("canonical action = %q, want modified (stale file planted)", fc.Action)
	}
	if fc.Client != "canonical" {
		t.Errorf("canonical client label = %q, want canonical", fc.Client)
	}

	srv := readCanonicalServer(t, "echo-server")
	if srv.Package.Version != "1.0.0" {
		t.Errorf("canonical version = %q, want 1.0.0 (the newly installed version)", srv.Package.Version)
	}
	if srv.Package.Integrity != "" {
		t.Errorf("canonical integrity = %q, want empty (kind-1 manifest carries none)", srv.Package.Integrity)
	}
	if srv.URL != "https://echo.example.test/sse" {
		t.Errorf("canonical url = %q, want the publisher endpoint", srv.URL)
	}
	if srv.Command != "" || len(srv.Args) != 0 {
		t.Errorf("canonical launch line = %q %v, want the stale command replaced by the URL", srv.Command, srv.Args)
	}
	if srv.Transport != "http-sse" {
		t.Errorf("canonical transport = %q, want http-sse (the new manifest's)", srv.Transport)
	}
	if srv.InstalledAt != "2026-01-01T00:00:00Z" {
		t.Errorf("canonical installedAt = %q, want the preserved pre-update stamp", srv.InstalledAt)
	}
	if srv.Enabled {
		t.Error("canonical enabled = true, want the preserved false (a user disable survives an update)")
	}

	// The registered client config was refreshed to the same shape the
	// install path writes (the URL, not the old command).
	cfgPath := filepath.Join(contractHome(t), ".config", "mcp", "mcp.json")
	cfgBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfgBytes), "https://echo.example.test/sse") {
		t.Errorf("client config not re-pointed at the new endpoint:\n%s", cfgBytes)
	}
	var doc struct {
		McpServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(cfgBytes, &doc); err != nil {
		t.Fatalf("client config unparsable: %v\n%s", err, cfgBytes)
	}
	if _, ok := doc.McpServers["echo-server"]; !ok {
		t.Errorf("client config lost the echo-server entry:\n%s", cfgBytes)
	}
}

// binaryUpdateRegistry is receiptRegistry with a stale-able kind-3
// binary-runtime package: bin-server latest 2.0.0, runtime "binary", bin
// "bin/server" — the launch line embeds the version directory, so a stale
// canonical record points daemon/try at the OLD binary. The tarball URL
// answers 404 (install persists the launch line without an artifact — the
// same offline path receiptRegistryWithDep uses).
func binaryUpdateRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	home := isolateHome(t)
	empty := t.TempDir()
	t.Setenv("PHAROS_WINDOWS_USERS_ROOT", empty)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/v1/packages/bin-server") {
			_, _ = io.WriteString(w, `{
				"name": "bin-server",
				"dist_tags": {"latest": "2.0.0"},
				"versions": [
					{
						"version": "2.0.0",
						"status": "active",
						"created_at": "2026-01-01T00:00:00Z",
						"manifest": {
							"name": "bin-server",
							"version": "2.0.0",
							"transport": "stdio",
							"runtime": "binary",
							"bin": "bin/server",
							"integrity": "sha512-new",
							"capabilities": ["tools"]
						}
					},
					{
						"version": "1.0.0",
						"status": "active",
						"created_at": "2026-01-01T00:00:00Z",
						"manifest": {
							"name": "bin-server",
							"version": "1.0.0",
							"transport": "stdio",
							"runtime": "binary",
							"bin": "bin/server",
							"integrity": "sha512-old",
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
	return finishReceiptRegistry(t, home, srv)
}

// TestUpdateRewritesCanonicalBinaryCommandPath: THE defect scenario. A
// binary-runtime server updated 1.0.0 → 2.0.0 must leave the canonical
// command pointing at the NEW version directory (and the client config
// with it), not the stale 1.0.0 binary the old update path left behind.
func TestUpdateRewritesCanonicalBinaryCommandPath(t *testing.T) {
	binaryUpdateRegistry(t)
	storeDir, err := install.DefaultStoreDir()
	if err != nil {
		t.Fatal(err)
	}

	// Client config referencing the server via the OLD version-dir command.
	// Built via json.Marshal, not string concatenation: on Windows the path
	// contains backslashes, which are invalid raw JSON string escapes — a
	// hand-concatenated template produces an unparsable file (CI windows
	// failure, run 34438032910) and the update rewrite would correctly
	// skip the unparsable config.
	mcpDir := filepath.Join(contractHome(t), ".config", "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(mcpDir, "mcp.json")
	oldCmd := filepath.Join(storeDir, "bin-server", "1.0.0", "bin", "server")
	plantDoc := map[string]any{
		"mcpServers": map[string]any{
			"bin-server": map[string]any{
				"command": oldCmd,
				"type":    "stdio",
			},
		},
	}
	plantBytes, err := json.MarshalIndent(plantDoc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, plantBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// Stale lockfile below the registry latest.
	inTempDir(t)
	lf := `{"version":1,"servers":{
		"bin-server":{"version":"1.0.0","integrity":"sha512-old","transport":"stdio","resolved":"","installedAt":"2026-01-01T00:00:00Z"}
	}}`
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "pharos.lock"), []byte(lf), 0o644); err != nil {
		t.Fatal(err)
	}

	// Stale canonical record: the pre-fix state (old version + old binary).
	plantStaleCanonical(t, "bin-server", canonical.Server{
		Transport: "stdio",
		Command:   oldCmd,
		Package:   canonical.PackageInfo{Name: "bin-server", Version: "1.0.0", Integrity: "sha512-old", Source: "pharos"},
		Enabled:   true,
	})

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"update", "--json")

	r := parseReceipt(t, stdout)
	if r.Status != "ok" {
		t.Errorf("status = %q, want ok (errors: %v)", r.Status, r.Errors)
	}
	if !strings.Contains(r.Package, "bin-server") {
		t.Errorf("package = %q, want the updated bin-server", r.Package)
	}

	srv := readCanonicalServer(t, "bin-server")
	wantCmd := filepath.Join(storeDir, "bin-server", "2.0.0", "bin", "server")
	if srv.Command != wantCmd {
		t.Errorf("canonical command = %q, want the NEW version-dir path %q (daemon/try must not run the stale binary)", srv.Command, wantCmd)
	}
	if strings.Contains(srv.Command, "1.0.0") {
		t.Errorf("canonical command %q still embeds the old version dir", srv.Command)
	}
	if srv.Package.Version != "2.0.0" || srv.Package.Integrity != "sha512-new" {
		t.Errorf("canonical package = %+v, want version 2.0.0 / integrity sha512-new", srv.Package)
	}
	if srv.Cwd != filepath.Join(storeDir, "bin-server", "2.0.0") {
		t.Errorf("canonical cwd = %q, want the new version dir", srv.Cwd)
	}

	// The registered client config tracks the same new binary. Parse the
	// rewritten file — a substring Contains would miss the JSON-escaped
	// backslashes Windows paths carry inside marshaled JSON.
	cfgBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfgDoc struct {
		McpServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(cfgBytes, &cfgDoc); err != nil {
		t.Fatalf("client config unparsable after rewrite: %v\n%s", err, cfgBytes)
	}
	raw, ok := cfgDoc.McpServers["bin-server"]
	if !ok {
		t.Fatalf("client config lost the bin-server entry:\n%s", cfgBytes)
	}
	var entry struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("bin-server entry unparsable: %v\n%s", err, raw)
	}
	if entry.Command != wantCmd {
		t.Errorf("client command = %q, want the NEW version-dir path %q", entry.Command, wantCmd)
	}
}
