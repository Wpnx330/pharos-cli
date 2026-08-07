package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/manifest"
	"github.com/Wpnx330/pharos-cli/internal/resolver"
)

// TestNormalizeLockTransport verifies the transport normalization
// covers the common input variants and defaults to stdio.
func TestNormalizeLockTransport(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty defaults to stdio", "", "stdio"},
		{"whitespace defaults to stdio", "   ", "stdio"},
		{"stdio passthrough", "stdio", "stdio"},
		{"http lowercased", "HTTP", "http"},
		{"sse becomes http-sse", "sse", "http-sse"},
		{"https becomes http-sse", "https", "http-sse"},
		{"http-sse passthrough", "http-sse", "http-sse"},
		{"unknown preserved lowercased", "WebSocket", "websocket"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange — nothing beyond tt.

			// Act
			got := normalizeLockTransport(tt.in)

			// Assert
			if got != tt.want {
				t.Errorf("normalizeLockTransport(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestBuildLockfileFromResolverOutput verifies that a resolver.Result
// is converted into a lockfile with the correct versions, transports,
// and resolved URLs.
func TestBuildLockfileFromResolverOutput(t *testing.T) {
	// Arrange
	const registry = "https://getpharos.dev"
	m := &manifest.Manifest{
		Name:    "my-app",
		Version: "1.0.0",
		Dependencies: []manifest.Dependency{
			{Name: "test-shout-server", Version: "^0.1.0"},
			{Name: "test-echo-server", Version: "^0.2.0"},
		},
	}
	result := &resolver.Result{
		Flat: map[string]string{
			"test-shout-server": "0.1.0",
			"test-echo-server":  "0.2.2",
			"shared-util":       "1.4.0",
		},
	}

	// Act
	lf := buildLockfile(result, m, registry)

	// Assert — every flat entry is present with the right version.
	cases := []struct{ name, version string }{
		{"test-shout-server", "0.1.0"},
		{"test-echo-server", "0.2.2"},
		{"shared-util", "1.4.0"},
	}
	for _, c := range cases {
		entry, ok := lf.Get(c.name)
		if !ok {
			t.Errorf("expected lockfile to contain %q", c.name)
			continue
		}
		if entry.Version != c.version {
			t.Errorf("%q version = %q, want %q", c.name, entry.Version, c.version)
		}
		if entry.Resolved != registry {
			t.Errorf("%q resolved = %q, want %q", c.name, entry.Resolved, registry)
		}
		if entry.Transport != "stdio" {
			t.Errorf("%q transport = %q, want %q", c.name, entry.Transport, "stdio")
		}
	}
	if len(lf.Servers) != 3 {
		t.Errorf("expected 3 server entries, got %d", len(lf.Servers))
	}
	if lf.Version != lockfile.LockVersion {
		t.Errorf("lockfile version = %d, want %d", lf.Version, lockfile.LockVersion)
	}
}

// TestBuildLockfileEmptyDeps verifies that a resolver.Result with an
// empty flat map produces an empty (but valid) lockfile body.
func TestBuildLockfileEmptyDeps(t *testing.T) {
	// Arrange
	m := &manifest.Manifest{Name: "solo", Version: "0.1.0"}
	result := &resolver.Result{Flat: map[string]string{}}

	// Act
	lf := buildLockfile(result, m, "https://getpharos.dev")

	// Assert
	if len(lf.Servers) != 0 {
		t.Errorf("expected 0 servers for empty flat map, got %d", len(lf.Servers))
	}
	if lf.Version != lockfile.LockVersion {
		t.Errorf("version = %d, want %d", lf.Version, lockfile.LockVersion)
	}
}

// TestWriteLockToTempDir verifies that writeLock persists a lockfile to
// the path returned by DefaultPath() and that the contents round-trip
// through Load.
func TestWriteLockToTempDir(t *testing.T) {
	// Arrange — chdir into a temp dir so DefaultPath() resolves there.
	dir := t.TempDir()
	origWd, _ := os.Getwd()
	t.Chdir(dir)
	defer t.Chdir(origWd)

	lf := lockfile.New()
	lf.Set("alpha", lockfile.ServerEntry{
		Version:   "1.0.0",
		Transport: "stdio",
		Resolved:  "https://getpharos.dev",
	})
	lf.Set("beta", lockfile.ServerEntry{
		Version:   "2.3.4",
		Transport: "http-sse",
		Resolved:  "https://getpharos.dev",
	})

	// Act
	if err := writeLock(lf); err != nil {
		t.Fatalf("writeLock failed: %v", err)
	}

	// Assert — file exists on disk.
	path := filepath.Join(dir, "pharos.lock")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lockfile not created: %v", err)
	}

	// Assert — contents round-trip.
	loaded, err := lockfile.Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(loaded.Servers) != 2 {
		t.Fatalf("expected 2 servers after load, got %d", len(loaded.Servers))
	}
	if e, ok := loaded.Get("alpha"); !ok || e.Version != "1.0.0" {
		t.Errorf("alpha missing or wrong version: %+v", e)
	}
	if e, ok := loaded.Get("beta"); !ok || e.Transport != "http-sse" {
		t.Errorf("beta missing or wrong transport: %+v", e)
	}
}

// TestWriteLockOverwritesExisting verifies that an existing lockfile is
// replaced (not merged) when writeLock is called again.
func TestWriteLockOverwritesExisting(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	origWd, _ := os.Getwd()
	t.Chdir(dir)
	defer t.Chdir(origWd)

	// Pre-create a lockfile with an entry that should NOT survive.
	existing := lockfile.New()
	existing.Set("stale-pkg", lockfile.ServerEntry{Version: "0.0.1"})
	if err := existing.Save(filepath.Join(dir, "pharos.lock")); err != nil {
		t.Fatalf("seed lockfile: %v", err)
	}

	// Act — write a fresh lockfile with different contents.
	fresh := lockfile.New()
	fresh.Set("fresh-pkg", lockfile.ServerEntry{
		Version:   "9.9.9",
		Transport: "stdio",
	})
	if err := writeLock(fresh); err != nil {
		t.Fatalf("writeLock failed: %v", err)
	}

	// Assert — stale entry is gone, fresh entry is present.
	loaded, err := lockfile.Load(filepath.Join(dir, "pharos.lock"))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Has("stale-pkg") {
		t.Error("stale-pkg should have been overwritten")
	}
	if !loaded.Has("fresh-pkg") {
		t.Error("fresh-pkg should be present after overwrite")
	}
	if e, _ := loaded.Get("fresh-pkg"); e.Version != "9.9.9" {
		t.Errorf("fresh-pkg version = %q, want 9.9.9", e.Version)
	}
}

// TestWriteLockEmptyDependencies verifies that writeLock succeeds and
// produces a valid (empty) lockfile when the lockfile has no servers.
func TestWriteLockEmptyDependencies(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	origWd, _ := os.Getwd()
	t.Chdir(dir)
	defer t.Chdir(origWd)

	lf := lockfile.New() // no servers

	// Act
	if err := writeLock(lf); err != nil {
		t.Fatalf("writeLock failed: %v", err)
	}

	// Assert — file exists and loads as a valid empty lockfile.
	loaded, err := lockfile.Load(filepath.Join(dir, "pharos.lock"))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Version != lockfile.LockVersion {
		t.Errorf("version = %d, want %d", loaded.Version, lockfile.LockVersion)
	}
	if len(loaded.Servers) != 0 {
		t.Errorf("expected 0 servers, got %d", len(loaded.Servers))
	}
}

// TestLockfileJSONShape verifies the on-disk JSON has the expected
// top-level keys (version + servers) and per-entry fields, so the
// lockfile remains machine-readable by other tooling.
func TestLockfileJSONShape(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	origWd, _ := os.Getwd()
	t.Chdir(dir)
	defer t.Chdir(origWd)

	lf := lockfile.New()
	lf.Set("test-shout-server", lockfile.ServerEntry{
		Version:   "0.1.0",
		Transport: "stdio",
		Resolved:  "https://getpharos.dev",
	})

	// Act
	if err := writeLock(lf); err != nil {
		t.Fatalf("writeLock failed: %v", err)
	}

	// Assert — parse raw JSON to check the shape is as expected.
	raw, err := os.ReadFile(filepath.Join(dir, "pharos.lock"))
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}
	var doc struct {
		Version int `json:"version"`
		Servers map[string]struct {
			Version   string `json:"version"`
			Transport string `json:"transport"`
			Resolved  string `json:"resolved"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, raw)
	}
	if doc.Version != 1 {
		t.Errorf("json version = %d, want 1", doc.Version)
	}
	entry, ok := doc.Servers["test-shout-server"]
	if !ok {
		t.Fatal("test-shout-server missing from json")
	}
	if entry.Version != "0.1.0" {
		t.Errorf("entry version = %q, want 0.1.0", entry.Version)
	}
	if entry.Transport != "stdio" {
		t.Errorf("entry transport = %q, want stdio", entry.Transport)
	}
	if entry.Resolved != "https://getpharos.dev" {
		t.Errorf("entry resolved = %q, want https://getpharos.dev", entry.Resolved)
	}
}
