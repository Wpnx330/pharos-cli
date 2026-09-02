package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/install"
)

// ---issue #20: update must rewrite client configs ------------------------
// Contributors: these tests are the contract. You can verify the behavior with:
//
//	pharos install thirtyiri/cursor-talk-to-figma
//	pharos update
//
// ...and are focused on shared config sockets without native trust stores yet.

const updateTestServerJSON = `{
  "mcpServers": {
    "test-srv": {"command": "/old/store/test-srv/1.0.0/venv/bin/python", "args": ["-m", "srv"]},
    "other-srv": {"command": "/keep/me", "args": []}
  },
  "editorTheme": "solarized",
  "userEmail": "keepme@example.com"
}`

const updateTestNoEntryJSON = `{
  "mcpServers": {
    "other-srv": {"command": "/x", "args": []}
  },
  "editorTheme": "mono"
}`

// writeTempConfig writescontent and returns its path.
func writeUpdateTempConfig(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestConfigReferencesServer covers hit/miss/absent.
func TestConfigReferencesServer(t *testing.T) {
	dir := t.TempDir()
	with := writeUpdateTempConfig(t, dir, "a.json", updateTestServerJSON)
	without := writeUpdateTempConfig(t, dir, "b.json", updateTestNoEntryJSON)
	missing := filepath.Join(dir, "nope.json")

	if !configReferencesServer(with, clientconfig.FormatMcpServers, "test-srv") {
		t.Error("a.json should reference test-srv")
	}
	if configReferencesServer(without, clientconfig.FormatMcpServers, "test-srv") {
		t.Error("b.json should NOT reference test-srv")
	}
	if configReferencesServer(missing, clientconfig.FormatMcpServers, "test-srv") {
		t.Error("missing file must not be an error, only false")
	}
}

// TestBackupConfigOverwritesOneGeneration: .bak created; second call replaces it (no .bak.1).
func TestBackupConfigOverwritesOneGeneration(t *testing.T) {
	dir := t.TempDir()
	p := writeUpdateTempConfig(t, dir, "cfg.json", `{"a":1}`)
	bak := p + ".bak"

	if err := backupConfigFile(p); err != nil {
		t.Fatalf("first backup: %v", err)
	}
	if _, err := os.Stat(bak); err != nil {
		t.Fatalf("no .bak: %v", err)
	}
	if err := os.WriteFile(p, []byte(`{"a":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := backupConfigFile(p); err != nil {
		t.Fatalf("second backup: %v", err)
	}
	if _, err := os.Stat(bak + ".1"); err == nil {
		t.Error("second backup must OVERWRITE .bak, not create .bak.1")
	}
}

// rewriteTestClient builds a Client pointing at a real temp file.
func rewriteTestClient(id clientconfig.ClientID, path, format string, existing bool) clientconfig.Client {
	return clientconfig.Client{ID: id, Name: "Test" + string(id), Path: path, Format: format, Existing: existing}
}

// newTestServerCfg = store layout of the NEW version (post-update).
func newTestServerCfg() clientconfig.ServerConfig {
	return clientconfig.ServerConfig{
		Command: "/new/store/test-srv/2.0.0/venv/bin/python",
		Args:    []string{"-m", "srv"},
	}
}

// TestUpdateRewritesAffectedClientOnly: entry rewritten, bystander untouched,
// non-MCP keys byte-preserved.
func TestUpdateRewritesAffectedClientOnly(t *testing.T) {
	dir := t.TempDir()
	with := writeUpdateTempConfig(t, dir, "affected.json", updateTestServerJSON)
	without := writeUpdateTempConfig(t, dir, "bystander.json", updateTestNoEntryJSON)
	before, _ := os.ReadFile(without)

	clients := []clientconfig.Client{
		rewriteTestClient(clientconfig.ClientCursor, with, clientconfig.FormatMcpServers, true),
		rewriteTestClient(clientconfig.ClientClaudeDesktop, without, clientconfig.FormatMcpServers, true),
	}
	updated, errs := rewriteClientsForUpdate("test-srv", newTestServerCfg(), clients, nil)
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(updated) != 1 || updated[0] != with {
		t.Fatalf("updated = %v, want [%s]", updated, with)
	}

	data, _ := os.ReadFile(with)
	if !containsStr(string(data), "/new/store/test-srv/2.0.0") {
		t.Errorf("entry not rewritten:\n%s", data)
	}
	if !containsStr(string(data), "solarized") || !containsStr(string(data), "keepme@example.com") {
		t.Errorf("non-MCP keys lost:\n%s", data)
	}
	if !containsStr(string(data), "other-srv") {
		t.Errorf("sibling server lost:\n%s", data)
	}

	after, _ := os.ReadFile(without)
	if string(before) != string(after) {
		t.Errorf("bystander was modified:\n%s", after)
	}

	// .bak convention: exactly one generation, beside the rewritten file.
	if _, err := os.Stat(with + ".bak"); err != nil {
		t.Errorf("update must leave .bak: %v", err)
	}
}

// TestUpdateRewriteIdempotent: second run is byte-identical.
func TestUpdateRewriteIdempotent(t *testing.T) {
	dir := t.TempDir()
	p := writeUpdateTempConfig(t, dir, "c.json", updateTestServerJSON)
	cli := []clientconfig.Client{rewriteTestClient(clientconfig.ClientCursor, p, clientconfig.FormatMcpServers, true)}

	if _, errs := rewriteClientsForUpdate("test-srv", newTestServerCfg(), cli, nil); len(errs) != 0 {
		t.Fatalf("first: %v", errs)
	}
	one, _ := os.ReadFile(p)
	if _, errs := rewriteClientsForUpdate("test-srv", newTestServerCfg(), cli, nil); len(errs) != 0 {
		t.Fatalf("second: %v", errs)
	}
	two, _ := os.ReadFile(p)
	if string(one) != string(two) {
		t.Errorf("not idempotent:\none=%s\ntwo=%s", one, two)
	}
}

// TestUpdateRewriteContinuesOnBadConfig: one malformed file must not abort the others.
func TestUpdateRewriteContinuesOnBadConfig(t *testing.T) {
	dir := t.TempDir()
	bad := writeUpdateTempConfig(t, dir, "broken.json", `{notjson`)
	good := writeUpdateTempConfig(t, dir, "fine.json", updateTestServerJSON)

	clients := []clientconfig.Client{
		rewriteTestClient(clientconfig.ClientClaudeDesktop, bad, clientconfig.FormatMcpServers, true),
		rewriteTestClient(clientconfig.ClientCursor, good, clientconfig.FormatMcpServers, true),
	}
	updated, errs := rewriteClientsForUpdate("test-srv", newTestServerCfg(), clients, nil)
	if len(updated) != 1 || updated[0] != good {
		t.Fatalf("good config must still be rewritten; got %v", updated)
	}
	if len(errs) != 1 {
		t.Fatalf("want exactly 1 collected error, got %v", errs)
	}
}

// errstrins? no-op.
var _ = time.Second

// --- helpers used by tests above (tiny, local) ---------------------------

func containsStr(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}

// guard: install importUsed by production签名 contract.
var _ = install.KindStdio
