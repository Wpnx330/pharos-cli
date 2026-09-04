package config

import (
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

// setupHome sets HOME to a temp dir so config is isolated.
func setupHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	setHomeEnv(t, dir)
	return dir
}

// TestLoadDefault verifies that Load returns defaults when no config file exists.
func TestLoadDefault(t *testing.T) {
	setupHome(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Registry != DefaultRegistry {
		t.Errorf("registry = %s, want %s", cfg.Registry, DefaultRegistry)
	}
	if cfg.Token != "" {
		t.Errorf("token should be empty by default")
	}
}

// TestSaveLoad verifies round-trip save and load.
func TestSaveLoad(t *testing.T) {
	setupHome(t)
	cfg := &Config{Registry: "https://custom.example.com", Token: "tok123"}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Registry != "https://custom.example.com" {
		t.Errorf("registry = %s", loaded.Registry)
	}
	if loaded.Token != "tok123" {
		t.Errorf("token = %s", loaded.Token)
	}
}

// TestGetSet verifies get and set operations.
func TestGetSet(t *testing.T) {
	cfg := Default()
	if err := cfg.Set("registry", "https://test.com"); err != nil {
		t.Fatal(err)
	}
	val, err := cfg.Get("registry")
	if err != nil {
		t.Fatal(err)
	}
	if val != "https://test.com" {
		t.Errorf("registry = %s", val)
	}
	if err := cfg.Set("token", "tok"); err != nil {
		t.Fatal(err)
	}
	val, _ = cfg.Get("token")
	if val != "tok" {
		t.Errorf("token = %s", val)
	}
}

// TestGetUnknownKey verifies error on unknown keys.
func TestGetUnknownKey(t *testing.T) {
	cfg := Default()
	_, err := cfg.Get("bogus")
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}

// TestSetUnknownKey verifies error on unknown keys.
func TestSetUnknownKey(t *testing.T) {
	cfg := Default()
	err := cfg.Set("bogus", "val")
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}

// TestSaveCreatesDir verifies that Save creates parent directories.
func TestSaveCreatesDir(t *testing.T) {
	setupHome(t)
	cfg := Default()
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(os.Getenv("HOME"), ".pharos", "config.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not created: %v", err)
	}
}

// TestAddCustomClient verifies that a custom client is added and
// persisted through Save/Load.
func TestAddCustomClient(t *testing.T) {
	setupHome(t)
	cfg := Default()
	if err := cfg.AddCustomClient("my-editor", "/home/user/.config/my-editor/mcp.json", "mcpServers"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.CustomClients) != 1 {
		t.Fatalf("expected 1 custom client, got %d", len(cfg.CustomClients))
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.CustomClients) != 1 {
		t.Fatalf("expected 1 custom client after load, got %d", len(loaded.CustomClients))
	}
	cc := loaded.CustomClients[0]
	if cc.ID != "my-editor" {
		t.Errorf("id = %s", cc.ID)
	}
	if cc.Path != "/home/user/.config/my-editor/mcp.json" {
		t.Errorf("path = %s", cc.Path)
	}
	if cc.Format != "mcpServers" {
		t.Errorf("format = %s", cc.Format)
	}
}

// TestAddCustomClientDefaultFormat verifies that an empty format
// defaults to "mcpServers".
func TestAddCustomClientDefaultFormat(t *testing.T) {
	cfg := Default()
	if err := cfg.AddCustomClient("ed", "/tmp/x.json", ""); err != nil {
		t.Fatal(err)
	}
	if cfg.CustomClients[0].Format != "mcpServers" {
		t.Errorf("format = %s, want mcpServers", cfg.CustomClients[0].Format)
	}
}

// TestAddCustomClientInvalidFormat verifies that an unknown format is
// rejected.
func TestAddCustomClientInvalidFormat(t *testing.T) {
	cfg := Default()
	err := cfg.AddCustomClient("ed", "/tmp/x.json", "xml")
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

// TestAddCustomClientReplacesExisting verifies that re-adding with the
// same ID replaces the entry rather than duplicating.
func TestAddCustomClientReplacesExisting(t *testing.T) {
	cfg := Default()
	cfg.AddCustomClient("ed", "/tmp/a.json", "mcpServers")
	cfg.AddCustomClient("ed", "/tmp/b.json", "array")
	if len(cfg.CustomClients) != 1 {
		t.Fatalf("expected 1 entry after replace, got %d", len(cfg.CustomClients))
	}
	if cfg.CustomClients[0].Path != "/tmp/b.json" {
		t.Errorf("path = %s, want /tmp/b.json", cfg.CustomClients[0].Path)
	}
	if cfg.CustomClients[0].Format != "array" {
		t.Errorf("format = %s, want array", cfg.CustomClients[0].Format)
	}
}

// TestRemoveCustomClient verifies removal.
func TestRemoveCustomClient(t *testing.T) {
	cfg := Default()
	cfg.AddCustomClient("a", "/tmp/a.json", "mcpServers")
	cfg.AddCustomClient("b", "/tmp/b.json", "array")
	if !cfg.RemoveCustomClient("a") {
		t.Fatal("expected RemoveCustomClient to return true for existing id")
	}
	if len(cfg.CustomClients) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(cfg.CustomClients))
	}
	if cfg.CustomClients[0].ID != "b" {
		t.Errorf("remaining id = %s, want b", cfg.CustomClients[0].ID)
	}
}

// TestRemoveCustomClientNotFound verifies false is returned for a
// missing id.
func TestRemoveCustomClientNotFound(t *testing.T) {
	cfg := Default()
	if cfg.RemoveCustomClient("nope") {
		t.Fatal("expected false for non-existent id")
	}
}

// TestGetCustomClient verifies lookup.
func TestGetCustomClient(t *testing.T) {
	cfg := Default()
	cfg.AddCustomClient("ed", "/tmp/ed.json", "array")
	cc := cfg.GetCustomClient("ed")
	if cc == nil {
		t.Fatal("expected non-nil for existing id")
	}
	if cc.Path != "/tmp/ed.json" {
		t.Errorf("path = %s", cc.Path)
	}
	if cfg.GetCustomClient("missing") != nil {
		t.Error("expected nil for missing id")
	}
}
