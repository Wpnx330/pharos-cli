package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/canonical"
	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
)

// ── drift test harness ──────────────────────────────────────────────────────
//
// Every drift test runs inside the envcontract harness: isolateHome points
// HOME/USERPROFILE at a fresh temp dir (canonical config + client configs),
// inTempDir moves cwd to a fresh temp dir (pharos.lock — lockfile.DefaultPath
// prefers the cwd), and PHAROS_WINDOWS_USERS_ROOT is pointed at a nonexistent
// dir so WSL2-side clients of the real machine are never detected.

func driftIsolate(t *testing.T) string {
	t.Helper()
	home := isolateHome(t)
	inTempDir(t)
	t.Setenv("PHAROS_WINDOWS_USERS_ROOT", filepath.Join(t.TempDir(), "absent"))
	return home
}

func plantDriftLockfile(t *testing.T, servers map[string]lockfile.ServerEntry) {
	t.Helper()
	lf := lockfile.New()
	for name, entry := range servers {
		lf.Set(name, entry)
	}
	if err := lf.Save("pharos.lock"); err != nil {
		t.Fatal(err)
	}
}

func plantDriftCanonical(t *testing.T, servers map[string]canonical.Server) {
	t.Helper()
	cfg := &canonical.Config{Servers: servers}
	if err := canonical.Save(cfg); err != nil {
		t.Fatal(err)
	}
}

// plantDriftServer writes a server entry into a client config through the
// real merge path, exactly as `pharos install` writes it.
func plantDriftServer(t *testing.T, c clientconfig.Client, name string, cfg clientconfig.ServerConfig) {
	t.Helper()
	if err := clientconfig.MergeServer(c, name, cfg); err != nil {
		t.Fatal(err)
	}
}

func driftLockEntry() lockfile.ServerEntry {
	return lockfile.ServerEntry{
		Version:     "1.0.0",
		Integrity:   "sha512-x",
		Transport:   "stdio",
		InstalledAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// driftStdioCanonical is the canonical record for a pharos-installed stdio
// server; re-deriving it must yield driftStdioCfg (the client shape below).
func driftStdioCanonical(name, command string, args []string) canonical.Server {
	return canonical.Server{
		Transport:   "stdio",
		Command:     command,
		Args:        args,
		Env:         map[string]string{"PHAROS_DRIFT": "one"},
		Package:     canonical.PackageInfo{Name: name, Version: "1.0.0", Source: "pharos"},
		Enabled:     true,
		InstalledAt: "2026-01-01T00:00:00Z",
	}
}

func driftRemoteCanonical(endpoint string) canonical.Server {
	return canonical.Server{
		Transport:   "http",
		URL:         endpoint,
		Package:     canonical.PackageInfo{Name: "drift-server", Version: "1.0.0", Source: "pharos"},
		Enabled:     true,
		InstalledAt: "2026-01-01T00:00:00Z",
	}
}

func driftKind2Canonical(args []string) canonical.Server {
	return canonical.Server{
		Transport:   "http",
		Command:     "node",
		Args:        args,
		Package:     canonical.PackageInfo{Name: "drift-server", Version: "1.0.0", Source: "pharos"},
		Enabled:     true,
		InstalledAt: "2026-01-01T00:00:00Z",
	}
}

// driftStdioCfg is the client-side ServerConfig install writes for
// driftStdioCanonical (node server.js with one env var). Type mirrors
// install.BuildServerConfig, which always sets the normalized transport.
var driftStdioCfg = clientconfig.ServerConfig{
	Command: "node",
	Args:    []string{"server.js"},
	Env:     map[string]string{"PHAROS_DRIFT": "one"},
	Type:    "stdio",
}

// handEditJSON rewrites a JSON config file through a mutation callback —
// the "hand edit" a user makes out from under pharos.
func handEditJSON(t *testing.T, path string, mutate func(root map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	mutate(root)
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func driftFindCheck(checks []doctorCheck, name string) *doctorCheck {
	for i := range checks {
		if checks[i].Name == name {
			return &checks[i]
		}
	}
	return nil
}

// driftGenericClient returns the built-in Generic MCP client for the
// isolated home (the only mcpServers-format client detected there).
func driftGenericClient(home string) clientconfig.Client {
	return clientconfig.Client{
		ID:       clientconfig.ClientGeneric,
		Name:     "Generic MCP",
		Path:     filepath.Join(home, ".config", "mcp", "mcp.json"),
		Format:   clientconfig.FormatMcpServers,
		Existing: true,
	}
}

// ── scenarios ───────────────────────────────────────────────────────────────

func TestDoctorDrift_CleanStateOK(t *testing.T) {
	home := driftIsolate(t)
	c := driftGenericClient(home)
	plantDriftServer(t, c, "drift-server", driftStdioCfg)
	plantDriftLockfile(t, map[string]lockfile.ServerEntry{"drift-server": driftLockEntry()})
	plantDriftCanonical(t, map[string]canonical.Server{
		"drift-server": driftStdioCanonical("drift-server", "node", []string{"server.js"}),
	})

	checks, note := runDriftChecks()
	if note != "" {
		t.Fatalf("expected no note for a managed client, got %q", note)
	}
	if len(checks) != 1 {
		t.Fatalf("expected exactly 1 drift check, got %d: %+v", len(checks), checks)
	}
	got := checks[0]
	if got.Name != "Config drift: Generic MCP" {
		t.Errorf("check name = %q, want %q", got.Name, "Config drift: Generic MCP")
	}
	if got.Status != "ok" {
		t.Errorf("status = %q (%q), want ok", got.Status, got.Error)
	}
	if len(got.Findings) != 0 {
		t.Errorf("expected no findings, got %+v", got.Findings)
	}
	if got.Detail != "1 managed server(s) match" {
		t.Errorf("detail = %q, want %q", got.Detail, "1 managed server(s) match")
	}
}

func TestDoctorDrift_ModifiedEnvNamesField(t *testing.T) {
	home := driftIsolate(t)
	c := driftGenericClient(home)
	plantDriftServer(t, c, "drift-server", driftStdioCfg)
	plantDriftLockfile(t, map[string]lockfile.ServerEntry{"drift-server": driftLockEntry()})
	plantDriftCanonical(t, map[string]canonical.Server{
		"drift-server": driftStdioCanonical("drift-server", "node", []string{"server.js"}),
	})

	handEditJSON(t, c.Path, func(root map[string]any) {
		servers := root["mcpServers"].(map[string]any)
		entry := servers["drift-server"].(map[string]any)
		env := entry["env"].(map[string]any)
		env["PHAROS_DRIFT"] = "two"
	})

	checks, _ := runDriftChecks()
	got := driftFindCheck(checks, "Config drift: Generic MCP")
	if got == nil {
		t.Fatalf("drift check missing: %+v", checks)
	}
	if got.Status != "fail" {
		t.Fatalf("status = %q, want fail", got.Status)
	}
	if len(got.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(got.Findings), got.Findings)
	}
	f := got.Findings[0]
	if f.Kind != "modified" || f.Severity != "error" {
		t.Errorf("kind/severity = %q/%q, want modified/error", f.Kind, f.Severity)
	}
	if f.Field != "env.PHAROS_DRIFT" {
		t.Errorf("field = %q, want env.PHAROS_DRIFT", f.Field)
	}
	if f.Expected != `"one"` || f.Actual != `"two"` {
		t.Errorf("expected/actual = %q/%q, want %q/%q", f.Expected, f.Actual, `"one"`, `"two"`)
	}
	if !strings.Contains(f.Message, "drift-server") || !strings.Contains(f.Message, "env.PHAROS_DRIFT") {
		t.Errorf("message %q must name the server and the field", f.Message)
	}
}

func TestDoctorDrift_MissingServer(t *testing.T) {
	home := driftIsolate(t)
	c := driftGenericClient(home)
	plantDriftServer(t, c, "drift-a", clientconfig.ServerConfig{
		Command: "node", Args: []string{"server.js"}, Env: map[string]string{"PHAROS_DRIFT": "one"},
	})
	plantDriftServer(t, c, "drift-b", clientconfig.ServerConfig{
		Command: "python3", Args: []string{"app.py"}, Env: map[string]string{"PHAROS_DRIFT": "one"},
	})
	plantDriftLockfile(t, map[string]lockfile.ServerEntry{
		"drift-a": driftLockEntry(),
		"drift-b": driftLockEntry(),
	})
	plantDriftCanonical(t, map[string]canonical.Server{
		"drift-a": driftStdioCanonical("drift-a", "node", []string{"server.js"}),
		"drift-b": driftStdioCanonical("drift-b", "python3", []string{"app.py"}),
	})

	// Hand-remove drift-a through the real removal path.
	if err := clientconfig.RemoveServer(c, "drift-a"); err != nil {
		t.Fatal(err)
	}

	checks, _ := runDriftChecks()
	got := driftFindCheck(checks, "Config drift: Generic MCP")
	if got == nil {
		t.Fatalf("drift check missing: %+v", checks)
	}
	if got.Status != "fail" {
		t.Fatalf("status = %q, want fail", got.Status)
	}
	if len(got.Findings) != 1 {
		t.Fatalf("expected 1 finding (missing drift-a), got %d: %+v", len(got.Findings), got.Findings)
	}
	f := got.Findings[0]
	if f.Kind != "missing" || f.Server != "drift-a" || f.Severity != "error" {
		t.Errorf("finding = %+v, want missing/error for drift-a", f)
	}
	if !strings.Contains(f.Message, "missing") {
		t.Errorf("message %q should say the server is missing", f.Message)
	}
}

func TestDoctorDrift_ExtraUnmanagedIsInfo(t *testing.T) {
	home := driftIsolate(t)
	c := driftGenericClient(home)
	plantDriftServer(t, c, "drift-server", driftStdioCfg)
	plantDriftLockfile(t, map[string]lockfile.ServerEntry{"drift-server": driftLockEntry()})
	plantDriftCanonical(t, map[string]canonical.Server{
		"drift-server": driftStdioCanonical("drift-server", "node", []string{"server.js"}),
	})

	handEditJSON(t, c.Path, func(root map[string]any) {
		servers := root["mcpServers"].(map[string]any)
		servers["hand-added"] = map[string]any{"command": "foo"}
	})

	checks, _ := runDriftChecks()
	got := driftFindCheck(checks, "Config drift: Generic MCP")
	if got == nil {
		t.Fatalf("drift check missing: %+v", checks)
	}
	// INFO-level extras must not fail the check.
	if got.Status != "ok" {
		t.Fatalf("status = %q (%q), want ok — extra findings are informational", got.Status, got.Error)
	}
	if len(got.Findings) != 1 {
		t.Fatalf("expected 1 info finding, got %d: %+v", len(got.Findings), got.Findings)
	}
	f := got.Findings[0]
	if f.Kind != "extra" || f.Severity != "info" || f.Server != "hand-added" {
		t.Errorf("finding = %+v, want extra/info for hand-added", f)
	}
	if !strings.Contains(f.Message, "unmanaged (hand-added?)") {
		t.Errorf("message %q should flag the server as unmanaged", f.Message)
	}
}

func TestDoctorDrift_UnmanagedClientOmitted(t *testing.T) {
	home := driftIsolate(t)
	c := driftGenericClient(home)
	// Config references only a hand-added server; pharos manages
	// drift-server, which this client has never seen.
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Path, []byte(`{"mcpServers": {"other": {"command": "foo"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	plantDriftLockfile(t, map[string]lockfile.ServerEntry{"drift-server": driftLockEntry()})
	plantDriftCanonical(t, map[string]canonical.Server{
		"drift-server": driftStdioCanonical("drift-server", "node", []string{"server.js"}),
	})

	checks, note := runDriftChecks()
	if len(checks) != 0 {
		t.Errorf("expected no drift checks for a client pharos never wrote to, got %+v", checks)
	}
	if !strings.Contains(note, "nothing to compare") {
		t.Errorf("note = %q, want a nothing-to-compare note", note)
	}
}

func TestDoctorDrift_NoManagedServersOmitted(t *testing.T) {
	home := driftIsolate(t)
	c := driftGenericClient(home)
	plantDriftServer(t, c, "drift-server", driftStdioCfg)
	plantDriftCanonical(t, map[string]canonical.Server{
		"drift-server": driftStdioCanonical("drift-server", "node", []string{"server.js"}),
	})
	// No lockfile planted: nothing is managed, nothing to diff.

	checks, note := runDriftChecks()
	if len(checks) != 0 {
		t.Errorf("expected no drift checks without a lockfile, got %+v", checks)
	}
	if !strings.Contains(note, "no pharos-managed servers") {
		t.Errorf("note = %q, want the no-managed-servers note", note)
	}
}

func TestDoctorDrift_UnparsableConfigFails(t *testing.T) {
	home := driftIsolate(t)
	c := driftGenericClient(home)
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	plantDriftLockfile(t, map[string]lockfile.ServerEntry{"drift-server": driftLockEntry()})
	plantDriftCanonical(t, map[string]canonical.Server{
		"drift-server": driftStdioCanonical("drift-server", "node", []string{"server.js"}),
	})

	checks, _ := runDriftChecks()
	got := driftFindCheck(checks, "Config drift: Generic MCP")
	if got == nil {
		t.Fatalf("drift check missing: %+v", checks)
	}
	if got.Status != "fail" || !strings.Contains(got.Error, "unreadable") {
		t.Errorf("status/error = %q/%q, want fail/unreadable", got.Status, got.Error)
	}
}

func TestDoctorDrift_CorruptLockfileFails(t *testing.T) {
	driftIsolate(t)
	if err := os.WriteFile("pharos.lock", []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	checks, note := runDriftChecks()
	if note != "" {
		t.Errorf("expected no note on a corrupt lockfile, got %q", note)
	}
	if len(checks) != 1 {
		t.Fatalf("expected exactly 1 failing drift check, got %+v", checks)
	}
	if checks[0].Name != "Config drift" || checks[0].Status != "fail" {
		t.Errorf("check = %+v, want Config drift/fail", checks[0])
	}
	if !strings.Contains(checks[0].Error, "pharos.lock") {
		t.Errorf("error %q should name pharos.lock", checks[0].Error)
	}
}

func TestDoctorDrift_ClaudeDesktopRemoteSkipped(t *testing.T) {
	home := driftIsolate(t)
	c := clientconfig.Client{
		ID:       clientconfig.ClientClaudeDesktop,
		Name:     "Claude Desktop",
		Path:     filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"),
		Format:   clientconfig.FormatMcpServers,
		Existing: true,
	}
	// Desktop only ever gets the stdio server; the remote one is a
	// Settings → Connectors bookmark pharos never writes here.
	plantDriftServer(t, c, "drift-local", driftStdioCfg)
	plantDriftLockfile(t, map[string]lockfile.ServerEntry{
		"drift-remote": driftLockEntry(),
		"drift-local":  driftLockEntry(),
	})
	plantDriftCanonical(t, map[string]canonical.Server{
		"drift-remote": driftRemoteCanonical("https://api.example.com/mcp"),
		"drift-local":  driftStdioCanonical("drift-local", "node", []string{"server.js"}),
	})

	checks, _ := runDriftChecks()
	got := driftFindCheck(checks, "Config drift: Claude Desktop")
	if got == nil {
		t.Fatalf("drift check missing: %+v", checks)
	}
	if got.Status != "ok" {
		t.Fatalf("status = %q (%q), want ok", got.Status, got.Error)
	}
	for _, f := range got.Findings {
		if f.Kind == "missing" && f.Server == "drift-remote" {
			t.Errorf("remote server must be skipped for Claude Desktop, got finding %+v", f)
		}
	}
	if got.Detail != "1 managed server(s) match" {
		t.Errorf("detail = %q, want only drift-local counted", got.Detail)
	}
}

// TestDoctorDrift_FormatRoundTrip plants every serialization shape through
// the real merge path and asserts doctor --diff reads it back as clean.
// The planted ServerConfig literals are independent of the derivation; if
// re-derivation ever diverges from what install writes, these fail.
func TestDoctorDrift_FormatRoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		client func(home string) clientconfig.Client
		plant  clientconfig.ServerConfig
		canon  canonical.Server
	}{
		{
			name:   "generic mcpServers stdio",
			client: driftGenericClient,
			plant:  driftStdioCfg,
			canon:  driftStdioCanonical("drift-server", "node", []string{"server.js"}),
		},
		{
			name: "cursor stdio",
			client: func(home string) clientconfig.Client {
				return clientconfig.Client{ID: clientconfig.ClientCursor, Name: "Cursor",
					Path: filepath.Join(home, ".cursor", "mcp.json"), Format: clientconfig.FormatMcpServers, Existing: true}
			},
			plant: driftStdioCfg,
			canon: driftStdioCanonical("drift-server", "node", []string{"server.js"}),
		},
		{
			name: "cursor remote http",
			client: func(home string) clientconfig.Client {
				return clientconfig.Client{ID: clientconfig.ClientCursor, Name: "Cursor",
					Path: filepath.Join(home, ".cursor", "mcp.json"), Format: clientconfig.FormatMcpServers, Existing: true}
			},
			plant: clientconfig.ServerConfig{URL: "https://api.example.com/mcp", Type: "http"},
			canon: driftRemoteCanonical("https://api.example.com/mcp"),
		},
		{
			name: "cursor remote sse",
			client: func(home string) clientconfig.Client {
				return clientconfig.Client{ID: clientconfig.ClientCursor, Name: "Cursor",
					Path: filepath.Join(home, ".cursor", "mcp.json"), Format: clientconfig.FormatMcpServers, Existing: true}
			},
			plant: clientconfig.ServerConfig{URL: "https://api.example.com/sse", Type: "sse"},
			canon: func() canonical.Server {
				s := driftRemoteCanonical("https://api.example.com/sse")
				s.Transport = "sse"
				return s
			}(),
		},
		{
			name: "claude-code stdio (typed)",
			client: func(home string) clientconfig.Client {
				return clientconfig.Client{ID: clientconfig.ClientClaudeCode, Name: "Claude Code",
					Path: filepath.Join(home, ".claude.json"), Format: clientconfig.FormatMcpServers, Existing: true}
			},
			plant: driftStdioCfg,
			canon: driftStdioCanonical("drift-server", "node", []string{"server.js"}),
		},
		{
			name: "claude-desktop stdio",
			client: func(home string) clientconfig.Client {
				return clientconfig.Client{ID: clientconfig.ClientClaudeDesktop, Name: "Claude Desktop",
					Path: filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"), Format: clientconfig.FormatMcpServers, Existing: true}
			},
			plant: driftStdioCfg,
			canon: driftStdioCanonical("drift-server", "node", []string{"server.js"}),
		},
		{
			name: "opencode local (command array + env list)",
			client: func(home string) clientconfig.Client {
				return clientconfig.Client{ID: clientconfig.ClientOpenCode, Name: "OpenCode",
					Path: filepath.Join(home, ".config", "opencode", "opencode.json"), Format: clientconfig.FormatOpenCode, Existing: true}
			},
			plant: driftStdioCfg,
			canon: driftStdioCanonical("drift-server", "node", []string{"server.js"}),
		},
		{
			name: "opencode remote",
			client: func(home string) clientconfig.Client {
				return clientconfig.Client{ID: clientconfig.ClientOpenCode, Name: "OpenCode",
					Path: filepath.Join(home, ".config", "opencode", "opencode.json"), Format: clientconfig.FormatOpenCode, Existing: true}
			},
			plant: clientconfig.ServerConfig{URL: "https://api.example.com/mcp"},
			canon: driftRemoteCanonical("https://api.example.com/mcp"),
		},
		{
			name: "zed context_servers stdio",
			client: func(home string) clientconfig.Client {
				return clientconfig.Client{ID: clientconfig.ClientZed, Name: "Zed",
					Path: filepath.Join(home, ".config", "zed", "settings.json"), Format: clientconfig.FormatZed, Existing: true}
			},
			plant: driftStdioCfg,
			canon: driftStdioCanonical("drift-server", "node", []string{"server.js"}),
		},
		{
			name: "codex TOML stdio",
			client: func(home string) clientconfig.Client {
				return clientconfig.Client{ID: clientconfig.ClientCodex, Name: "Codex CLI",
					Path: filepath.Join(home, ".codex", "config.toml"), Format: clientconfig.FormatTOML, Existing: true}
			},
			plant: driftStdioCfg,
			canon: driftStdioCanonical("drift-server", "node", []string{"server.js"}),
		},
		{
			name: "grok TOML remote",
			client: func(home string) clientconfig.Client {
				return clientconfig.Client{ID: clientconfig.ClientGrok, Name: "Grok Build",
					Path: filepath.Join(home, ".grok", "config.toml"), Format: clientconfig.FormatTOML, Existing: true}
			},
			plant: clientconfig.ServerConfig{URL: "https://api.example.com/mcp"},
			canon: driftRemoteCanonical("https://api.example.com/mcp"),
		},
		{
			name: "hermes YAML stdio",
			client: func(home string) clientconfig.Client {
				return clientconfig.Client{ID: clientconfig.ClientHermes, Name: "Hermes Agent",
					Path: filepath.Join(home, ".hermes", "config.yaml"), Format: clientconfig.FormatHermes, Existing: true}
			},
			plant: driftStdioCfg,
			canon: driftStdioCanonical("drift-server", "node", []string{"server.js"}),
		},
		{
			name: "aider YAML stdio",
			client: func(home string) clientconfig.Client {
				return clientconfig.Client{ID: clientconfig.ClientAider, Name: "Aider",
					Path: filepath.Join(home, ".aider.conf.yml"), Format: clientconfig.FormatAider, Existing: true}
			},
			plant: driftStdioCfg,
			canon: driftStdioCanonical("drift-server", "node", []string{"server.js"}),
		},
		{
			name: "custom array client",
			client: func(home string) clientconfig.Client {
				return clientconfig.Client{ID: "myarray", Name: "myarray",
					Path: filepath.Join(home, ".myarray", "mcp.json"), Format: clientconfig.FormatArray, Existing: true}
			},
			plant: driftStdioCfg,
			canon: driftStdioCanonical("drift-server", "node", []string{"server.js"}),
		},
		{
			name: "kind2 localhost url with declared port",
			client: func(home string) clientconfig.Client {
				return clientconfig.Client{ID: clientconfig.ClientCursor, Name: "Cursor",
					Path: filepath.Join(home, ".cursor", "mcp.json"), Format: clientconfig.FormatMcpServers, Existing: true}
			},
			plant: clientconfig.ServerConfig{URL: "http://127.0.0.1:9000", Type: "http"},
			canon: driftKind2Canonical([]string{"server.js", "--port", "9000"}),
		},
		{
			name: "kind2 localhost default port",
			client: func(home string) clientconfig.Client {
				return clientconfig.Client{ID: clientconfig.ClientCursor, Name: "Cursor",
					Path: filepath.Join(home, ".cursor", "mcp.json"), Format: clientconfig.FormatMcpServers, Existing: true}
			},
			plant: clientconfig.ServerConfig{URL: "http://127.0.0.1:8765", Type: "http"},
			canon: driftKind2Canonical([]string{"server.js"}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := driftIsolate(t)
			c := tc.client(home)
			if string(c.ID) == "myarray" {
				// Custom clients must be registered in
				// ~/.pharos/config.json for Detect() to see them.
				plantCustomClient(t, home, string(c.ID), c.Path, c.Format)
			}
			plantDriftServer(t, c, "drift-server", tc.plant)
			plantDriftLockfile(t, map[string]lockfile.ServerEntry{"drift-server": driftLockEntry()})
			plantDriftCanonical(t, map[string]canonical.Server{"drift-server": tc.canon})

			checks, _ := runDriftChecks()
			got := driftFindCheck(checks, fmt.Sprintf("Config drift: %s", c.Name))
			if got == nil {
				t.Fatalf("drift check missing (checks: %+v)", checks)
			}
			if got.Status != "ok" {
				t.Fatalf("status = %q (%q), want ok; findings: %+v", got.Status, got.Error, got.Findings)
			}
			if len(got.Findings) != 0 {
				t.Errorf("expected no findings, got %+v", got.Findings)
			}
		})
	}
}

// plantCustomClient registers a custom client in ~/.pharos/config.json and
// creates its (empty) config file so Detect() reports it as existing and
// MergeServer can merge into it.
func plantCustomClient(t *testing.T, home, id, path, format string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".pharos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{
		"custom_clients": []map[string]string{{
			"id":     id,
			"path":   path,
			"format": format,
		}},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestDoctorDrift_ReformatIsNotDrift verifies the normalization contract:
// key order, whitespace, and empty-container spelling changes never count
// as drift.
func TestDoctorDrift_ReformatIsNotDrift(t *testing.T) {
	home := driftIsolate(t)
	c := driftGenericClient(home)
	plantDriftServer(t, c, "drift-server", driftStdioCfg)
	plantDriftLockfile(t, map[string]lockfile.ServerEntry{"drift-server": driftLockEntry()})
	plantDriftCanonical(t, map[string]canonical.Server{
		"drift-server": driftStdioCanonical("drift-server", "node", []string{"server.js"}),
	})

	handEditJSON(t, c.Path, func(root map[string]any) {
		// Rebuild the same entry with different key order and one extra
		// user key; JSON semantics are unchanged.
		root["mcpServers"] = map[string]any{
			"drift-server": map[string]any{
				"env":     map[string]any{"PHAROS_DRIFT": "one"},
				"args":    []any{"server.js"},
				"command": "node",
				"extra":   "user key",
			},
		}
	})

	checks, _ := runDriftChecks()
	got := driftFindCheck(checks, "Config drift: Generic MCP")
	if got == nil {
		t.Fatalf("drift check missing: %+v", checks)
	}
	if got.Status != "ok" {
		t.Errorf("status = %q (%q), want ok; findings: %+v", got.Status, got.Error, got.Findings)
	}
}

// TestDoctorDrift_JSONReportParity runs the full `pharos doctor --diff`
// through the contract harness and asserts:
//   - drift findings appear in the doctor JSON report (additive shape)
//   - the human rendering of the same state shows exactly the same number
//     of finding lines (human vs JSON parity)
func TestDoctorDrift_JSONReportParity(t *testing.T) {
	contractRegistry(t) // isolates home + fake registry for the health check
	inTempDir(t)
	t.Setenv("PHAROS_WINDOWS_USERS_ROOT", filepath.Join(t.TempDir(), "absent"))

	home := contractHome(t)
	c := driftGenericClient(home)
	plantDriftServer(t, c, "drift-server", driftStdioCfg)
	plantDriftLockfile(t, map[string]lockfile.ServerEntry{"drift-server": driftLockEntry()})
	plantDriftCanonical(t, map[string]canonical.Server{
		"drift-server": driftStdioCanonical("drift-server", "node", []string{"server.js"}),
	})
	// Drift the config: changed env value + one hand-added server.
	handEditJSON(t, c.Path, func(root map[string]any) {
		servers := root["mcpServers"].(map[string]any)
		entry := servers["drift-server"].(map[string]any)
		env := entry["env"].(map[string]any)
		env["PHAROS_DRIFT"] = "two"
		servers["hand-added"] = map[string]any{"command": "foo"}
	})

	// JSON leg: the drift findings must appear in the doctor report.
	stdout, combined := runContract(t, map[string]string{
		"PHAROS_JSON":            "1",
		"PHAROS_NON_INTERACTIVE": "1",
	}, "doctor", "--diff")

	var report struct {
		Checks   []doctorCheck `json:"checks"`
		Failures int           `json:"failures"`
		Healthy  bool          `json:"healthy"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &report); err != nil {
		t.Fatalf("doctor --diff --json stdout is not a doctor report: %v\n%s", err, combined)
	}

	jsonFindings := 0
	var driftCheck *doctorCheck
	for i := range report.Checks {
		if report.Checks[i].Name == "Config drift: Generic MCP" {
			driftCheck = &report.Checks[i]
			jsonFindings += len(report.Checks[i].Findings)
			continue
		}
		// W1.1 additivity: non-drift checks carry no findings field.
		if report.Checks[i].Findings != nil {
			t.Errorf("check %q unexpectedly carries findings", report.Checks[i].Name)
		}
	}
	if driftCheck == nil {
		t.Fatalf("doctor JSON report has no drift check; stdout: %s", stdout)
	}
	if driftCheck.Status != "fail" {
		t.Errorf("drift status = %q, want fail", driftCheck.Status)
	}
	if jsonFindings != 2 {
		t.Fatalf("expected 2 JSON findings (modified + extra), got %d: %+v", jsonFindings, driftCheck.Findings)
	}
	var kinds []string
	for _, f := range driftCheck.Findings {
		kinds = append(kinds, f.Kind+"/"+f.Severity)
	}
	if strings.Join(kinds, ",") != "modified/error,extra/info" {
		t.Errorf("findings = %v, want modified/error,extra/info", kinds)
	}

	// Human leg: the same state renders the same findings as bullet lines.
	checks, _ := runDriftChecks()
	var buf bytes.Buffer
	printDoctorChecks(&buf, checks, "")
	human := buf.String()
	humanFindings := strings.Count(human, "        • ")
	if humanFindings != jsonFindings {
		t.Errorf("human findings = %d, JSON findings = %d — parity broken\n%s", humanFindings, jsonFindings, human)
	}
	for _, f := range driftCheck.Findings {
		if !strings.Contains(human, f.Message) {
			t.Errorf("human output missing finding message %q", f.Message)
		}
	}
}
