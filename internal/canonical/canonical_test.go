package canonical

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// setHomeEnv points every home-dir env var prod path resolution reads at
// dir, so tests are hermetic on every GOOS: HOME (unix), USERPROFILE
// (windows os.UserHomeDir).
func setHomeEnv(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

// helper: point HOME at a temp dir and return the canonical config path
func setupTest(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	setHomeEnv(t, home)
	return filepath.Join(home, ".pharos", "mcp.json")
}

func TestLoadEmpty(t *testing.T) {
	setupTest(t)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != CurrentVersion {
		t.Errorf("expected version %d, got %d", CurrentVersion, cfg.Version)
	}
	if cfg.Schema != SchemaURL {
		t.Errorf("expected schema %s, got %s", SchemaURL, cfg.Schema)
	}
	if len(cfg.Servers) != 0 {
		t.Errorf("expected 0 servers, got %d", len(cfg.Servers))
	}
}

func TestAddAndLoadServer(t *testing.T) {
	setupTest(t)

	srv := Server{
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"-y", "pkg"},
		Env:       map[string]string{"API_KEY": "${secret:KEY}"},
		Package: PackageInfo{
			Name:    "test-server",
			Version: "1.0.0",
			Source:  "pharos",
		},
		Enabled: true,
	}

	if err := AddServer("test-server", srv); err != nil {
		t.Fatal(err)
	}

	// Verify it was written
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	got, exists := cfg.Servers["test-server"]
	if !exists {
		t.Fatal("server not found after add")
	}
	if got.Transport != "stdio" {
		t.Errorf("expected transport stdio, got %s", got.Transport)
	}
	if got.Command != "npx" {
		t.Errorf("expected command npx, got %s", got.Command)
	}
	if got.Package.Name != "test-server" {
		t.Errorf("expected package name test-server, got %s", got.Package.Name)
	}
	if got.Package.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", got.Package.Version)
	}
	if got.Enabled != true {
		t.Error("expected enabled true")
	}
	if got.InstalledAt == "" {
		t.Error("expected installedAt to be set")
	}
}

func TestAddServerOverwrites(t *testing.T) {
	setupTest(t)

	srv1 := Server{
		Transport: "stdio",
		Command:   "npx",
		Package:   PackageInfo{Name: "pkg", Version: "1.0.0", Source: "pharos"},
		Enabled:   true,
	}
	srv2 := Server{
		Transport: "http-sse",
		URL:       "https://example.com/mcp",
		Package:   PackageInfo{Name: "pkg", Version: "2.0.0", Source: "pharos"},
		Enabled:   true,
	}

	AddServer("pkg", srv1)
	AddServer("pkg", srv2)

	cfg, _ := Load()
	got := cfg.Servers["pkg"]
	if got.Transport != "http-sse" {
		t.Errorf("expected transport http-sse (overwrite), got %s", got.Transport)
	}
	if got.Package.Version != "2.0.0" {
		t.Errorf("expected version 2.0.0 (overwrite), got %s", got.Package.Version)
	}
}

func TestRemoveServer(t *testing.T) {
	setupTest(t)

	srv := Server{
		Transport: "stdio",
		Command:   "npx",
		Package:   PackageInfo{Name: "test", Version: "1.0.0", Source: "pharos"},
		Enabled:   true,
	}
	AddServer("test", srv)

	removed, err := RemoveServer("test")
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Error("expected removed=true")
	}

	cfg, _ := Load()
	if _, exists := cfg.Servers["test"]; exists {
		t.Error("server still exists after remove")
	}
}

func TestRemoveServerNotFound(t *testing.T) {
	setupTest(t)

	removed, err := RemoveServer("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if removed {
		t.Error("expected removed=false for nonexistent server")
	}
}

func TestSetEnabled(t *testing.T) {
	setupTest(t)

	srv := Server{
		Transport: "stdio",
		Command:   "npx",
		Package:   PackageInfo{Name: "test", Version: "1.0.0", Source: "pharos"},
		Enabled:   true,
	}
	AddServer("test", srv)

	if err := SetEnabled("test", false); err != nil {
		t.Fatal(err)
	}

	cfg, _ := Load()
	got := cfg.Servers["test"]
	if got.Enabled != false {
		t.Error("expected enabled=false after SetEnabled(false)")
	}

	if err := SetEnabled("test", true); err != nil {
		t.Fatal(err)
	}

	cfg, _ = Load()
	got = cfg.Servers["test"]
	if got.Enabled != true {
		t.Error("expected enabled=true after SetEnabled(true)")
	}
}

func TestSetEnabledNotFound(t *testing.T) {
	setupTest(t)

	err := SetEnabled("nonexistent", true)
	if err == nil {
		t.Fatal("expected error for nonexistent server")
	}
}

func TestGetServer(t *testing.T) {
	setupTest(t)

	srv := Server{
		Transport: "http-sse",
		URL:       "https://example.com/mcp",
		Package:   PackageInfo{Name: "remote", Version: "1.0.0", Source: "pharos"},
		Enabled:   true,
	}
	AddServer("remote", srv)

	got, err := GetServer("remote")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected server, got nil")
	}
	if got.URL != "https://example.com/mcp" {
		t.Errorf("expected URL, got %s", got.URL)
	}
}

func TestGetServerNotFound(t *testing.T) {
	setupTest(t)

	got, err := GetServer("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil for nonexistent server")
	}
}

func TestListServers(t *testing.T) {
	setupTest(t)

	AddServer("zebra", Server{Transport: "stdio", Command: "npx", Package: PackageInfo{Name: "zebra", Version: "1.0", Source: "pharos"}, Enabled: true})
	AddServer("alpha", Server{Transport: "stdio", Command: "npx", Package: PackageInfo{Name: "alpha", Version: "1.0", Source: "pharos"}, Enabled: true})
	AddServer("mike", Server{Transport: "stdio", Command: "npx", Package: PackageInfo{Name: "mike", Version: "1.0", Source: "pharos"}, Enabled: true})

	names, err := ListServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 3 {
		t.Fatalf("expected 3 servers, got %d", len(names))
	}
	// Should be sorted
	if names[0] != "alpha" || names[1] != "mike" || names[2] != "zebra" {
		t.Errorf("expected sorted [alpha mike zebra], got %v", names)
	}
}

func TestSaveCreatesDirectories(t *testing.T) {
	home := t.TempDir()
	setHomeEnv(t, home)

	cfg := &Config{
		Schema:  SchemaURL,
		Version: CurrentVersion,
		Servers: make(map[string]Server),
	}

	// ~/.pharos/ should not exist yet
	pharosDir := filepath.Join(home, ".pharos")
	if _, err := os.Stat(pharosDir); err == nil {
		t.Fatal("~/.pharos should not exist yet")
	}

	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}

	// File should exist now
	configPath := filepath.Join(pharosDir, "mcp.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	// Verify it's valid JSON with correct structure
	data, _ := os.ReadFile(configPath)
	var parsed Config
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed.Version != CurrentVersion {
		t.Errorf("expected version %d in file, got %d", CurrentVersion, parsed.Version)
	}
}

func TestRemoteServerNoCommand(t *testing.T) {
	setupTest(t)

	// Pure remote server: no command, no args, just URL
	srv := Server{
		Transport: "http-sse",
		URL:       "https://weather.example.com/mcp",
		Package: PackageInfo{
			Name:    "remote-weather",
			Version: "1.3.0",
			Source:  "pharos",
			// Integrity intentionally empty for remote
		},
		Enabled: true,
	}

	if err := AddServer("remote-weather", srv); err != nil {
		t.Fatal(err)
	}

	got, _ := GetServer("remote-weather")
	if got == nil {
		t.Fatal("server not found")
	}
	if got.Command != "" {
		t.Errorf("expected empty command for remote, got %s", got.Command)
	}
	if got.URL != "https://weather.example.com/mcp" {
		t.Errorf("expected URL, got %s", got.URL)
	}
	if got.Package.Integrity != "" {
		t.Errorf("expected empty integrity for remote, got %s", got.Package.Integrity)
	}

	// Verify JSON omits empty fields
	cfg, _ := Load()
	data, _ := json.Marshal(cfg.Servers["remote-weather"])
	var raw map[string]json.RawMessage
	json.Unmarshal(data, &raw)
	if _, hasCommand := raw["command"]; hasCommand {
		t.Error("expected command field to be omitted for remote server")
	}
	if _, hasArgs := raw["args"]; hasArgs {
		t.Error("expected args field to be omitted for remote server")
	}
}
