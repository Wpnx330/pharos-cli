package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/install"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
)

// =============================================================
// W5.1 A6 — origin metadata on lockfile entries
// =============================================================
//
// pharos records HOW each server entered management: registry
// installs at `pharos install` time, adopted servers at
// `pharos import --adopt` time. The update path preserves the
// recorded origin (bumping the registry Ref's version part) and
// legacy nil-Origin entries keep behaving as registry installs
// without pharos fabricating metadata. All offline via the
// receipt/contract harness.

// originLockLoad loads the cwd lockfile and returns the named entry.
func originLockLoad(t *testing.T, name string) lockfile.ServerEntry {
	t.Helper()
	lf, err := lockfile.Load(filepath.Join(originCwd(t), "pharos.lock"))
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := lf.Get(name)
	if !ok {
		t.Fatalf("server %q not found in lockfile", name)
	}
	return entry
}

func originCwd(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return cwd
}

// TestInstallRecordsRegistryOrigin drives a real offline install and
// asserts the lockfile entry carries registry provenance.
func TestInstallRecordsRegistryOrigin(t *testing.T) {
	receiptRegistry(t)
	fakeGenericClientOther(t)
	inTempDir(t)

	runContract(t,
		map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
		"install", "echo-server", "--client", "generic")

	entry := originLockLoad(t, "echo-server")
	if entry.Origin == nil {
		t.Fatal("installed entry has no Origin")
	}
	if entry.Origin.Kind != lockfile.OriginKindRegistry {
		t.Errorf("kind = %q, want %q", entry.Origin.Kind, lockfile.OriginKindRegistry)
	}
	if entry.Origin.Ref != "echo-server@1.0.0" {
		t.Errorf("ref = %q, want echo-server@1.0.0", entry.Origin.Ref)
	}
	if entry.Origin.InstalledVia != "pharos install" {
		t.Errorf("installed_via = %q, want %q", entry.Origin.InstalledVia, "pharos install")
	}
	if entry.Origin.AdoptedFrom != "" {
		t.Errorf("adopted_from = %q, want empty on registry origin", entry.Origin.AdoptedFrom)
	}
}

// TestInstallRecordsDependencyOrigin: dependencies are registry installs
// too — each dep entry records its own provenance.
func TestInstallRecordsDependencyOrigin(t *testing.T) {
	receiptRegistryWithDep(t)
	fakeClaudeDesktopConfig(t)
	inTempDir(t)

	runContract(t,
		map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
		"install", "echo-server", "--client", "claude-desktop")

	entry := originLockLoad(t, "dep-server")
	if entry.Origin == nil {
		t.Fatal("dependency entry has no Origin")
	}
	if entry.Origin.Kind != lockfile.OriginKindRegistry || entry.Origin.Ref != "dep-server@1.0.0" || entry.Origin.InstalledVia != "pharos install" {
		t.Errorf("dependency origin = %+v", entry.Origin)
	}
}

// TestUpdatePreservesOriginAndClients: an adopted, client-recorded entry
// updated through the real apply path keeps its adopted Origin verbatim
// and its Clients record (previously the update path wiped Clients).
func TestUpdatePreservesOriginAndClients(t *testing.T) {
	receiptRegistry(t)
	fakeGenericClient(t)
	dir := inTempDir(t)
	lfJSON := `{"version":1,"servers":{"echo-server":{
		"version":"0.9.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z",
		"clients":["generic"],
		"origin":{"kind":"adopted","ref":"https://github.com/example/echo","installed_via":"pharos import --adopt","adopted_from":"generic"}
	}}}`
	if err := os.WriteFile(filepath.Join(dir, "pharos.lock"), []byte(lfJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"update", "--json")

	entry := originLockLoad(t, "echo-server")
	if entry.Version != "1.0.0" {
		t.Fatalf("version = %q, want 1.0.0 (update applied)", entry.Version)
	}
	if entry.Origin == nil {
		t.Fatal("update dropped the adopted Origin")
	}
	if entry.Origin.Kind != lockfile.OriginKindAdopted ||
		entry.Origin.Ref != "https://github.com/example/echo" ||
		entry.Origin.AdoptedFrom != "generic" ||
		entry.Origin.InstalledVia != "pharos import --adopt" {
		t.Errorf("origin after update = %+v, want adopted origin verbatim", entry.Origin)
	}
	if len(entry.Clients) != 1 || entry.Clients[0] != "generic" {
		t.Errorf("clients after update = %v, want [generic] (preserved)", entry.Clients)
	}
}

// TestUpdateBumpsRegistryOriginRef: a registry-origin entry's Ref version
// part tracks the newly installed version after an update.
func TestUpdateBumpsRegistryOriginRef(t *testing.T) {
	receiptRegistry(t)
	fakeGenericClient(t)
	dir := inTempDir(t)
	lfJSON := `{"version":1,"servers":{"echo-server":{
		"version":"0.9.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z",
		"origin":{"kind":"registry","ref":"echo-server@0.9.0","installed_via":"pharos install"}
	}}}`
	if err := os.WriteFile(filepath.Join(dir, "pharos.lock"), []byte(lfJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"update", "--json")

	entry := originLockLoad(t, "echo-server")
	if entry.Origin == nil {
		t.Fatal("update dropped the registry Origin")
	}
	if entry.Origin.Ref != "echo-server@1.0.0" {
		t.Errorf("ref after update = %q, want echo-server@1.0.0", entry.Origin.Ref)
	}
	if entry.Origin.Kind != lockfile.OriginKindRegistry || entry.Origin.InstalledVia != "pharos install" {
		t.Errorf("origin after update = %+v", entry.Origin)
	}
}

// TestUpdateLegacyNilOriginStaysAbsent pins the backfill rule: legacy
// entries update through the registry path (behavior), but pharos never
// writes a fabricated origin — the field stays absent after the update.
func TestUpdateLegacyNilOriginStaysAbsent(t *testing.T) {
	receiptRegistry(t)
	fakeGenericClient(t)
	dir := inTempDir(t)
	// Pre-W5.1 entry: no origin, no clients.
	if err := os.WriteFile(filepath.Join(dir, "pharos.lock"), []byte(`{"version":1,"servers":{"echo-server":{"version":"0.9.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"update", "--json")

	entry := originLockLoad(t, "echo-server")
	if entry.Version != "1.0.0" {
		t.Fatalf("version = %q, want 1.0.0 (legacy entry updates normally)", entry.Version)
	}
	if entry.Origin != nil {
		t.Errorf("origin = %+v, want nil (backfill is behavior-only, never written)", entry.Origin)
	}
}

// TestAdoptRecordsOrigin: adopted servers record adopted provenance with
// the source client and the registry repository URL when resolution
// found one; unresolved servers still record adopted origin (Ref empty).
func TestAdoptRecordsOrigin(t *testing.T) {
	home := driftIsolate(t)
	c := driftGenericClient(home)
	plantDriftServer(t, c, "echo-server", driftStdioCfg)
	plantDriftServer(t, c, "mystery", driftStdioCfg)

	opts := adoptOptions{
		API: adoptFakeRegistry(t, map[string]string{
			// repo_url at the packument top level (PackageDetail.RepoURL).
			"echo-server": `{
				"name": "echo-server",
				"repo_url": "https://github.com/example/echo",
				"dist_tags": {"latest": "1.0.0"},
				"versions": [
					{"version": "1.0.0", "manifest": {"name": "echo-server", "version": "1.0.0", "transport": "stdio", "integrity": "sha512-abc"}}
				]
			}`,
		}),
	}
	report, code := adoptRun(t, opts)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	_ = report

	entry := adoptLock(t, "echo-server")
	if entry.Origin == nil {
		t.Fatal("adopted entry has no Origin")
	}
	if entry.Origin.Kind != lockfile.OriginKindAdopted {
		t.Errorf("kind = %q, want adopted", entry.Origin.Kind)
	}
	if entry.Origin.AdoptedFrom != "generic" {
		t.Errorf("adopted_from = %q, want generic", entry.Origin.AdoptedFrom)
	}
	if entry.Origin.Ref != "https://github.com/example/echo" {
		t.Errorf("ref = %q, want the registry repo URL", entry.Origin.Ref)
	}
	if entry.Origin.InstalledVia != "pharos import --adopt" {
		t.Errorf("installed_via = %q", entry.Origin.InstalledVia)
	}

	// Unresolved in registry: adopted origin still recorded, Ref empty.
	mystery := adoptLock(t, "mystery")
	if mystery.Origin == nil || mystery.Origin.Kind != lockfile.OriginKindAdopted || mystery.Origin.AdoptedFrom != "generic" {
		t.Errorf("unresolved adopted origin = %+v, want adopted/generic", mystery.Origin)
	}
	if mystery.Origin.Ref != "" {
		t.Errorf("unresolved ref = %q, want empty", mystery.Origin.Ref)
	}
}

// TestAdoptPreservesExistingOriginAndPin: adopting an already-managed
// server merges client coverage but must not rewrite its provenance or
// clear its pin.
func TestAdoptPreservesExistingOriginAndPin(t *testing.T) {
	home := driftIsolate(t)
	c := driftGenericClient(home)
	plantDriftServer(t, c, "echo-server", driftStdioCfg)

	pinned := "1.0.0"
	lf := lockfile.New()
	lf.Set("echo-server", lockfile.ServerEntry{
		Version:     "1.0.0",
		InstalledAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Origin:      &lockfile.OriginInfo{Kind: lockfile.OriginKindRegistry, Ref: "echo-server@1.0.0", InstalledVia: "pharos install"},
		PinnedAt:    &pinned,
	})
	if err := lf.Save("pharos.lock"); err != nil {
		t.Fatal(err)
	}

	opts := adoptOptions{
		API: adoptFakeRegistry(t, map[string]string{
			"echo-server": adoptRegistryPkg("echo-server", "1.0.0", "sha512-abc"),
		}),
	}
	if _, code := adoptRun(t, opts); code != 0 {
		t.Fatalf("adopt exit code = %d, want 0", code)
	}

	entry := adoptLock(t, "echo-server")
	if entry.Origin == nil || entry.Origin.Kind != lockfile.OriginKindRegistry || entry.Origin.Ref != "echo-server@1.0.0" {
		t.Errorf("origin after re-adopt = %+v, want the existing registry origin preserved", entry.Origin)
	}
	if entry.PinnedAt == nil || *entry.PinnedAt != "1.0.0" {
		t.Errorf("PinnedAt after re-adopt = %v, want 1.0.0 preserved", entry.PinnedAt)
	}
}

// TestUpdateLockfileRefreshesPinnedOnReinstall: an explicit reinstall of
// a pinned server to a NEW version moves the pin to the installed
// version (pin intent follows the explicit install); the new install's
// origin replaces the old provenance.
func TestUpdateLockfileRefreshesPinnedOnReinstall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	lockPath := filepath.Join(t.TempDir(), "pharos.lock")

	pinned := "0.9.0"
	seed := lockfile.New()
	seed.Set("srv", lockfile.ServerEntry{
		Version:     "0.9.0",
		PinnedAt:    &pinned,
		InstalledAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err := seed.Save(lockPath); err != nil {
		t.Fatal(err)
	}

	origin := &lockfile.OriginInfo{Kind: lockfile.OriginKindRegistry, Ref: "srv@1.0.0", InstalledVia: "pharos install"}
	err := install.UpdateLockfile(lockPath, &install.InstallResult{Name: "srv", Version: "1.0.0", Transport: "stdio"}, "", nil, origin)
	if err != nil {
		t.Fatal(err)
	}
	lf, err := lockfile.Load(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := lf.Get("srv")
	if entry.PinnedAt == nil || *entry.PinnedAt != "1.0.0" {
		t.Errorf("PinnedAt after reinstall = %v, want refreshed to 1.0.0", entry.PinnedAt)
 	}
 	if entry.Origin == nil || entry.Origin.Ref != "srv@1.0.0" {
 		t.Errorf("origin after reinstall = %+v, want the new install origin", entry.Origin)
 	}
 }
