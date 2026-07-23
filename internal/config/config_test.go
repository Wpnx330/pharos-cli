package config

import (
	"os"
	"path/filepath"
	"testing"
)

// setupHome sets HOME to a temp dir so config is isolated.
func setupHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
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
