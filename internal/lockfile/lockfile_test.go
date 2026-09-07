package lockfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// ── W5.1 A6: additive Origin + PinnedAt (no schema version bump) ────────────

// legacyEntryJSON is a pre-W5.1 lockfile: no origin, no pinnedAt, no
// clients. Legacy lockfiles must load unchanged forever.
const legacyEntryJSON = `{"version":1,"servers":{
	"echo-server":{"version":"1.0.0","integrity":"sha512-x","transport":"stdio","resolved":"https://reg.test/tgz","installedAt":"2026-01-01T00:00:00Z"}
}}`

// TestLoadLegacyEntryFieldsAbsent proves the additive W5.1 fields stay
// absent (nil, not empty-non-nil) when loading a legacy lockfile, so
// legacy behavior is observable and JSON round-trips stay byte-stable
// for untouched entries.
func TestLoadLegacyEntryFieldsAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pharos.lock")
	if err := os.WriteFile(path, []byte(legacyEntryJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	lf, err := Load(path)
	if err != nil {
		t.Fatalf("legacy lockfile must load unchanged: %v", err)
	}
	if lf.Version != 1 {
		t.Errorf("version = %d, want 1 (no schema bump)", lf.Version)
	}
	entry, ok := lf.Get("echo-server")
	if !ok {
		t.Fatal("legacy entry not found")
	}
	if entry.Origin != nil {
		t.Errorf("legacy entry Origin = %+v, want nil", entry.Origin)
	}
	if entry.PinnedAt != nil {
		t.Errorf("legacy entry PinnedAt = %q, want nil", *entry.PinnedAt)
	}
}

// TestLegacyRoundTripAddsNoKeys proves Load→Save of a legacy lockfile
// does not inject the new keys: absence of origin/pinnedAt must survive
// a pharos-driven rewrite, keeping diffs on legacy files empty.
func TestLegacyRoundTripAddsNoKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pharos.lock")
	if err := os.WriteFile(path, []byte(legacyEntryJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	lf, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := lf.Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"\"origin\"", "\"pinnedAt\"", "\"clients\""} {
		if strings.Contains(string(data), key) {
			t.Errorf("legacy round-trip injected %s:\n%s", key, data)
		}
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := reloaded.Get("echo-server")
	if !ok {
		t.Fatal("legacy entry lost after round-trip")
	}
	if entry.Version != "1.0.0" || entry.Integrity != "sha512-x" || entry.Resolved != "https://reg.test/tgz" {
		t.Errorf("legacy entry fields changed after round-trip: %+v", entry)
	}
	if entry.Origin != nil || entry.PinnedAt != nil {
		t.Errorf("round-trip fabricated additive fields: %+v", entry)
	}
}

// TestOriginPinnedRoundTrip proves a fully-populated W5.1 entry
// round-trips: Origin (all four fields) and PinnedAt survive Save→Load
// byte-discipline intact, and AdoptedFrom is omitted when empty.
func TestOriginPinnedRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pharos.lock")
	pinned := "2.3.4"
	lf := New()
	lf.Set("reg-server", ServerEntry{
		Version:     "2.3.4",
		Origin:      &OriginInfo{Kind: OriginKindRegistry, Ref: "reg-server@2.3.4", InstalledVia: "pharos install"},
		PinnedAt:    &pinned,
		InstalledAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	lf.Set("adopted-server", ServerEntry{
		Version: "1.0.0",
		Origin: &OriginInfo{
			Kind:         OriginKindAdopted,
			Ref:          "https://github.com/example/adopted",
			InstalledVia: "pharos import --adopt",
			AdoptedFrom:  "cursor",
		},
		InstalledAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err := lf.Save(path); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := loaded.Get("reg-server")
	if reg.Origin == nil || reg.Origin.Kind != OriginKindRegistry || reg.Origin.Ref != "reg-server@2.3.4" || reg.Origin.InstalledVia != "pharos install" || reg.Origin.AdoptedFrom != "" {
		t.Errorf("registry origin = %+v", reg.Origin)
	}
	if reg.PinnedAt == nil || *reg.PinnedAt != "2.3.4" {
		t.Errorf("PinnedAt = %v, want 2.3.4", reg.PinnedAt)
	}

	adopted, _ := loaded.Get("adopted-server")
	if adopted.Origin == nil || adopted.Origin.Kind != OriginKindAdopted || adopted.Origin.AdoptedFrom != "cursor" {
		t.Errorf("adopted origin = %+v", adopted.Origin)
	}
	if adopted.PinnedAt != nil {
		t.Errorf("PinnedAt = %v, want nil", adopted.PinnedAt)
	}
}

// TestOriginJSONShape pins the wire format: origin is a nested object,
// adopted_from is omitted when empty, pinnedAt is a plain string, and
// legacy entries carry neither key.
func TestOriginJSONShape(t *testing.T) {
	pinned := "1.0.0"
	lf := New()
	lf.Set("a", ServerEntry{Version: "1.0.0", Origin: &OriginInfo{Kind: OriginKindRegistry, Ref: "a@1.0.0", InstalledVia: "pharos install"}})
	lf.Set("b", ServerEntry{Version: "1.0.0", PinnedAt: &pinned})
	data, err := json.Marshal(lf)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `"origin":{"kind":"registry","ref":"a@1.0.0","installed_via":"pharos install"}`) {
		t.Errorf("origin object shape unexpected:\n%s", s)
	}
	if strings.Contains(s, `"adopted_from"`) {
		t.Errorf("adopted_from must be omitted when empty:\n%s", s)
	}
	if !strings.Contains(s, `"pinnedAt":"1.0.0"`) {
		t.Errorf("pinnedAt shape unexpected:\n%s", s)
	}
	if strings.Contains(s, `"origin":null`) || strings.Contains(s, `"pinnedAt":null`) {
		t.Errorf("absent fields must be omitted, never null:\n%s", s)
	}
}
