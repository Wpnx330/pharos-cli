package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
)

// =============================================================
// W5.1 external adversarial review — fixes
// =============================================================
//
// R1 (REQUIRED): re-adopt desyncs PinnedAt from Version — the hadPrev
// branch of adopt now preserves the installed truth (Version, Integrity,
// Transport, InstalledAt) instead of overwriting it with dist-tags-latest
// metadata (or wiping it when the registry is unreachable). C4: dep
// installs count as installs — a dep resolution that bumps a pinned
// server moves the pin (contract, documented in llm.txt). C5: repo_url
// trailing-slash normalization. C6: scp-style derivation + pin JSON
// stdout purity.

// TestReadoptPreservesInstalledVersionAndPin (R1): install 1.0.0 → pin →
// re-adopt while the registry's dist-tags latest = 2.0.0 → the installed
// truth survives: Version stays 1.0.0, PinnedAt stays 1.0.0, and the
// recorded Integrity/Transport are unchanged (registry metadata refresh
// must not fabricate a version bump the disk never saw).
func TestReadoptPreservesInstalledVersionAndPin(t *testing.T) {
	home := driftIsolate(t)
	c := driftGenericClient(home)
	plantDriftServer(t, c, "echo-server", driftStdioCfg)

	pinned := "1.0.0"
	lf := lockfile.New()
	lf.Set("echo-server", lockfile.ServerEntry{
		Version:     "1.0.0",
		Integrity:   "sha512-installed",
		Transport:   "stdio",
		InstalledAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Origin:      &lockfile.OriginInfo{Kind: lockfile.OriginKindRegistry, Ref: "echo-server@1.0.0", InstalledVia: "pharos install"},
		PinnedAt:    &pinned,
	})
	if err := lf.Save("pharos.lock"); err != nil {
		t.Fatal(err)
	}

	// Registry latest is 2.0.0 with different integrity AND a different
	// transport — every field the old re-adopt path would have clobbered.
	opts := adoptOptions{
		API: adoptFakeRegistry(t, map[string]string{
			"echo-server": `{
				"name": "echo-server",
				"dist_tags": {"latest": "2.0.0"},
				"versions": [
					{"version": "2.0.0", "manifest": {"name": "echo-server", "version": "2.0.0", "transport": "http-sse", "endpoint": "https://echo2.example.test/sse", "integrity": "sha512-new"}},
					{"version": "1.0.0", "manifest": {"name": "echo-server", "version": "1.0.0", "transport": "http-sse", "endpoint": "https://echo.example.test/sse", "integrity": "sha512-old"}}
				]
			}`,
		}),
	}
	report, code := adoptRun(t, opts)
	if code != 0 {
		t.Fatalf("adopt exit code = %d, want 0", code)
	}

	entry := adoptLock(t, "echo-server")
	if entry.Version != "1.0.0" {
		t.Errorf("version after re-adopt = %q, want 1.0.0 (installed truth, not dist-tags latest 2.0.0)", entry.Version)
	}
	if entry.Integrity != "sha512-installed" {
		t.Errorf("integrity after re-adopt = %q, want sha512-installed (unchanged)", entry.Integrity)
	}
	if entry.Transport != "stdio" {
		t.Errorf("transport after re-adopt = %q, want stdio (the recorded install transport)", entry.Transport)
	}
	if entry.PinnedAt == nil || *entry.PinnedAt != "1.0.0" {
		t.Errorf("PinnedAt after re-adopt = %v, want 1.0.0 (pin stays consistent with the installed version)", entry.PinnedAt)
	}
	if entry.Origin == nil || entry.Origin.Kind != lockfile.OriginKindRegistry || entry.Origin.Ref != "echo-server@1.0.0" {
		t.Errorf("origin after re-adopt = %+v, want the registry origin preserved", entry.Origin)
	}

	// The canonical record mirrors the preserved values so lockfile and
	// canonical never disagree after a re-adopt.
	srv := adoptCanon(t, "echo-server")
	if srv.Package.Version != "1.0.0" || srv.Package.Integrity != "sha512-installed" {
		t.Errorf("canonical package = %+v, want version/integrity mirroring the preserved entry", srv.Package)
	}

	// The report row shows the preserved version, not the registry latest.
	for _, row := range report.Servers {
		if row.Name == "echo-server" && row.Version != "1.0.0" {
			t.Errorf("report row version = %q, want 1.0.0", row.Version)
		}
	}
}

// TestReadoptDeadRegistryPreservesEntry (R1, related dead-registry wipe):
// re-adopting while the registry is unreachable must leave the recorded
// Version/Integrity/Transport untouched (previously they were wiped to
// empty strings) and the adopt still succeeds; a fresh (unmanaged) server
// in the same run still records adopted provenance.
func TestReadoptDeadRegistryPreservesEntry(t *testing.T) {
	home := driftIsolate(t)
	c := driftGenericClient(home)
	plantDriftServer(t, c, "echo-server", driftStdioCfg)
	plantDriftServer(t, c, "mystery", driftStdioCfg)

	lf := lockfile.New()
	lf.Set("echo-server", lockfile.ServerEntry{
		Version:     "1.0.0",
		Integrity:   "sha512-keep",
		Transport:   "stdio",
		InstalledAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Origin:      &lockfile.OriginInfo{Kind: lockfile.OriginKindAdopted, InstalledVia: "pharos import --adopt", AdoptedFrom: "generic"},
	})
	if err := lf.Save("pharos.lock"); err != nil {
		t.Fatal(err)
	}

	opts := adoptOptions{API: adoptUnresolvedRegistry(t)}
	if _, code := adoptRun(t, opts); code != 0 {
		t.Fatalf("adopt exit code = %d, want 0 (a dead registry must not fail the adopt)", code)
	}

	entry := adoptLock(t, "echo-server")
	if entry.Version != "1.0.0" || entry.Integrity != "sha512-keep" || entry.Transport != "stdio" {
		t.Errorf("entry after dead-registry re-adopt = version %q integrity %q transport %q, want all unchanged",
			entry.Version, entry.Integrity, entry.Transport)
	}
	if entry.Origin == nil || entry.Origin.Kind != lockfile.OriginKindAdopted || entry.Origin.AdoptedFrom != "generic" {
		t.Errorf("origin after dead-registry re-adopt = %+v, want the adopted origin preserved", entry.Origin)
	}

	// A not-yet-managed server still adopts and records adopted origin
	// (Ref empty — nothing was resolved).
	mystery := adoptLock(t, "mystery")
	if mystery.Origin == nil || mystery.Origin.Kind != lockfile.OriginKindAdopted || mystery.Origin.AdoptedFrom != "generic" || mystery.Origin.Ref != "" {
		t.Errorf("fresh adopted origin under dead registry = %+v, want adopted/generic with empty ref", mystery.Origin)
	}
}

// TestDepInstallMovesPinnedDependency (C4): installing a package whose
// dependency graph installs a different version of a pinned dependency
// moves that dependency's pin to the newly installed version
// (most-recent-install-wins) — documented contract, not an accident.
func TestDepInstallMovesPinnedDependency(t *testing.T) {
	receiptRegistryWithDep(t) // echo-server@1.0.0 depends on dep-server ^1.0.0 (latest 1.0.0)
	fakeGenericClient(t)
	inTempDir(t)
	plantW51Lockfile(t, `{
		"dep-server":{"version":"0.9.0","integrity":"sha512-x","transport":"stdio","resolved":"","installedAt":"2026-01-01T00:00:00Z","pinnedAt":"0.9.0"}
	}`)

	runContract(t,
		map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
		"install", "echo-server", "--client", "generic")

	entry := pinLockLoad(t, "dep-server")
	if entry.Version != "1.0.0" {
		t.Fatalf("dep version = %q, want 1.0.0 (the dependency graph installed it)", entry.Version)
	}
	if entry.PinnedAt == nil || *entry.PinnedAt != "1.0.0" {
		t.Errorf("dep PinnedAt = %v, want moved to 1.0.0 (dependency installs count as installs)", entry.PinnedAt)
	}
	if echo := pinLockLoad(t, "echo-server"); echo.Version != "1.0.0" {
		t.Errorf("primary version = %q, want 1.0.0", echo.Version)
	}
}

// TestPinJSONStdoutSingleDocument (C6): under PHAROS_JSON=1,
// `pin <name> <version>` delegates to install, so stdout parses as
// exactly one JSON document — the install receipt — and pin's own
// confirmations never touch stdout (they go to stderr).
func TestPinJSONStdoutSingleDocument(t *testing.T) {
	receiptRegistry(t)
	fakeGenericClient(t)
	inTempDir(t)
	plantW51Lockfile(t, `{
		"echo-server":{"version":"0.9.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z"}
	}`)

	stdout, combined := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"pin", "echo-server", "1.0.0")

	r := parseReceipt(t, stdout)
	if r.Command != "install" {
		t.Errorf("receipt command = %q, want install (pin delegates the version install)", r.Command)
	}
	if strings.Contains(stdout, "Pinned") {
		t.Errorf("pin confirmations leaked to stdout:\n%s", stdout)
	}
	if !strings.Contains(combined, "Pinned echo-server@1.0.0") {
		t.Errorf("pin confirmation missing from combined output (must go to stderr):\n%s", combined)
	}

	entry := pinLockLoad(t, "echo-server")
	if entry.Version != "1.0.0" || entry.PinnedAt == nil || *entry.PinnedAt != "1.0.0" {
		t.Errorf("entry after pin = version %q pinned %v, want 1.0.0/1.0.0", entry.Version, entry.PinnedAt)
	}
}

// TestDeriveRepoLinksScpStyleYieldsNothing (C6): an scp-style
// git@host:path Ref/RepoURL yields NO repo/changelog links — scp-style
// hosts are never rewritten into browse URLs (that would be fabrication).
func TestDeriveRepoLinksScpStyleYieldsNothing(t *testing.T) {
	entry := lockfile.ServerEntry{
		Origin: &lockfile.OriginInfo{
			Kind:         lockfile.OriginKindAdopted,
			Ref:          "git@github.com:x/y.git",
			InstalledVia: "pharos import --adopt",
			AdoptedFrom:  "generic",
		},
	}
	pkg := &api.PackageDetail{RepoURL: "git@github.com:x/y.git"}
	repo, changelog := deriveRepoLinks(entry, pkg)
	if repo != "" || changelog != "" {
		t.Errorf("scp-style refs derived repo=%q changelog=%q, want no links (never rewrite scp-style)", repo, changelog)
	}
}
