package lockfile

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewIsEmpty(t *testing.T) {
	lf := New()
	if lf.Version != LockVersion {
		t.Errorf("version = %d, want %d", lf.Version, LockVersion)
	}
	if len(lf.Servers) != 0 {
		t.Errorf("expected empty servers, got %d", len(lf.Servers))
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pharos.lock")

	lf := New()
	lf.Set("@scope/server", ServerEntry{
		Version:     "1.2.3",
		Integrity:   "sha512-abcdef",
		Transport:   "stdio",
		Resolved:    "https://example.com/tarball",
		InstalledAt: time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC),
	})
	if err := lf.Save(path); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != LockVersion {
		t.Errorf("loaded version = %d", loaded.Version)
	}
	entry, ok := loaded.Get("@scope/server")
	if !ok {
		t.Fatal("server not found after load")
	}
	if entry.Version != "1.2.3" {
		t.Errorf("version = %s", entry.Version)
	}
	if entry.Integrity != "sha512-abcdef" {
		t.Errorf("integrity = %s", entry.Integrity)
	}
	if entry.Transport != "stdio" {
		t.Errorf("transport = %s", entry.Transport)
	}
}

func TestLoadMissingReturnsEmpty(t *testing.T) {
	lf, err := Load(filepath.Join(t.TempDir(), "nonexistent.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if len(lf.Servers) != 0 {
		t.Errorf("expected empty lockfile")
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.lock")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSetGet(t *testing.T) {
	lf := New()
	lf.Set("foo", ServerEntry{Version: "1.0.0"})
	entry, ok := lf.Get("foo")
	if !ok {
		t.Fatal("expected to find foo")
	}
	if entry.Version != "1.0.0" {
		t.Errorf("version = %s", entry.Version)
	}
}

func TestHas(t *testing.T) {
	lf := New()
	if lf.Has("foo") {
		t.Error("should not have foo")
	}
	lf.Set("foo", ServerEntry{})
	if !lf.Has("foo") {
		t.Error("should have foo after Set")
	}
}

func TestRemove(t *testing.T) {
	lf := New()
	lf.Set("foo", ServerEntry{})
	if !lf.Remove("foo") {
		t.Error("Remove should return true for existing")
	}
	if lf.Has("foo") {
		t.Error("foo should be gone")
	}
	if lf.Remove("foo") {
		t.Error("Remove should return false for missing")
	}
}

func TestSaveCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deep", "pharos.lock")
	lf := New()
	if err := lf.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lockfile not created: %v", err)
	}
}

func TestDefaultPathPrefersCwd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	path, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(dir, "pharos.lock")
	if path != expected {
		t.Errorf("path = %s, want %s", path, expected)
	}
}
