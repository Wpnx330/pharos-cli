package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// =============================================================
// W5.1 A6 — `pharos update --check` origin-aware extras
// =============================================================

// checkRegistryWithRepo serves echo-server@1.0.0 with a repo_url, plus a
// stale-able beta-server at latest 2.0.0 whose repo_url sits on an
// UNKNOWN git host (links must be suppressed, never fabricated).
func checkRegistryWithRepo(t *testing.T) {
	t.Helper()
	home := isolateHome(t)
	t.Setenv("PHAROS_WINDOWS_USERS_ROOT", filepath.Join(t.TempDir(), "absent"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/packages/echo-server"):
			// Trailing slash on purpose (C5): normalizeRepoURL must trim it
			// so the derived links are ".../echo" / ".../echo/releases",
			// never ".../echo//releases".
			_, _ = io.WriteString(w, `{
				"name": "echo-server",
				"repo_url": "https://github.com/example/echo/",
				"dist_tags": {"latest": "1.0.0"},
				"versions": [
					{"version": "1.0.0", "status": "active", "created_at": "2026-01-01T00:00:00Z",
					 "manifest": {"name": "echo-server", "version": "1.0.0", "transport": "http-sse",
					              "endpoint": "https://echo.example.test/sse", "capabilities": ["tools"]}}
				]
			}`)
		case strings.HasPrefix(r.URL.Path, "/v1/packages/beta-server"):
			_, _ = io.WriteString(w, `{
				"name": "beta-server",
				"repo_url": "https://git.sr.ht/~example/beta",
				"dist_tags": {"latest": "2.0.0"},
				"versions": [
					{"version": "2.0.0", "status": "active", "created_at": "2026-01-01T00:00:00Z",
					 "manifest": {"name": "beta-server", "version": "2.0.0", "transport": "http-sse",
					              "endpoint": "https://beta.example.test/sse", "capabilities": ["tools"]}},
					{"version": "1.0.0", "status": "active", "created_at": "2026-01-01T00:00:00Z",
					 "manifest": {"name": "beta-server", "version": "1.0.0", "transport": "http-sse",
					              "endpoint": "https://beta.example.test/sse", "capabilities": ["tools"]}}
				]
			}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	dir := filepath.Join(home, ".pharos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"registry":` + strconv.Quote(srv.URL) + `}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// plantW51Lockfile writes a lockfile JSON into cwd and fails on error.
func plantW51Lockfile(t *testing.T, serversJSON string) {
	t.Helper()
	lf := `{"version":1,"servers":` + serversJSON + `}`
	if err := os.WriteFile(filepath.Join(originCwd(t), "pharos.lock"), []byte(lf), 0o644); err != nil {
		t.Fatal(err)
	}
}

// parseUpdateReport requires stdout to be exactly one valid update
// report JSON document.
func parseUpdateReport(t *testing.T, stdout string) updateReport {
	t.Helper()
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		t.Fatal("stdout is empty — no update report JSON emitted")
	}
	var r updateReport
	if err := json.Unmarshal([]byte(trimmed), &r); err != nil {
		t.Fatalf("stdout is not a single update report JSON document: %v\n%s", err, trimmed)
	}
	return r
}

// findUpdateRow returns the report row for a server.
func findUpdateRow(t *testing.T, r updateReport, name string) updateEntry {
	t.Helper()
	for _, e := range r.Servers {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("server %q missing from update report: %+v", name, r.Servers)
	return updateEntry{}
}

// TestUpdateCheckDerivesRepoLinks: --check emits the repo + releases
// changelog for a github-hosted package (legacy nil-origin entry —
// backfill treats it as registry and derives from packument repo_url),
// and emits NEITHER for an unknown git host.
func TestUpdateCheckDerivesRepoLinks(t *testing.T) {
	checkRegistryWithRepo(t)
	fakeGenericClient(t)
	inTempDir(t)
	// Legacy entries: no origin — backfill = registry derivation path.
	plantW51Lockfile(t, `{
		"echo-server":{"version":"0.9.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z"},
		"beta-server":{"version":"1.0.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z"}
	}`)

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"update", "--check", "--json")

	report := parseUpdateReport(t, stdout)
	if !report.DryRun {
		t.Error("--check report must set dry_run=true (check is a no-apply mode)")
	}

	echo := findUpdateRow(t, report, "echo-server")
	if echo.Action != "update_available" {
		t.Errorf("echo action = %q, want update_available", echo.Action)
	}
	if echo.Repo != "https://github.com/example/echo" {
		t.Errorf("echo repo = %q, want the github URL", echo.Repo)
	}
	if echo.Changelog != "https://github.com/example/echo/releases" {
		t.Errorf("echo changelog = %q, want the releases URL", echo.Changelog)
	}
	if echo.Origin != nil {
		t.Errorf("legacy entry origin = %+v, want omitted (nil)", echo.Origin)
	}

	// Unknown host (sourcehut): no fabricated links.
	beta := findUpdateRow(t, report, "beta-server")
	if beta.Repo != "" || beta.Changelog != "" {
		t.Errorf("unknown-host repo/changelog = %q/%q, want both empty (never fabricate)", beta.Repo, beta.Changelog)
	}
}

// TestUpdateCheckOriginEmittedWhenStored: a stored origin rides along on
// the report rows; adopted Ref on a known host wins over packument data.
func TestUpdateCheckOriginEmittedWhenStored(t *testing.T) {
	checkRegistryWithRepo(t)
	fakeGenericClient(t)
	inTempDir(t)
	plantW51Lockfile(t, `{
		"echo-server":{"version":"0.9.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z",
		"origin":{"kind":"adopted","ref":"https://gitlab.com/example/echo","installed_via":"pharos import --adopt","adopted_from":"cursor"}}
	}`)

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"update", "--check", "--json")

	report := parseUpdateReport(t, stdout)
	echo := findUpdateRow(t, report, "echo-server")
	if echo.Origin == nil {
		t.Fatal("stored origin missing from report row")
	}
	if echo.Origin.Kind != "adopted" || echo.Origin.AdoptedFrom != "cursor" {
		t.Errorf("origin = %+v, want adopted/cursor", echo.Origin)
	}
	// Adopted Ref is a known gitlab host → gitlab releases shape.
	if echo.Repo != "https://gitlab.com/example/echo" {
		t.Errorf("repo = %q, want the gitlab URL", echo.Repo)
	}
	if echo.Changelog != "https://gitlab.com/example/echo/-/releases" {
		t.Errorf("changelog = %q, want the gitlab -/releases shape", echo.Changelog)
	}
}

// TestUpdateCheckDoesNotApply: --check must not modify the lockfile.
func TestUpdateCheckDoesNotApply(t *testing.T) {
	checkRegistryWithRepo(t)
	fakeGenericClient(t)
	dir := inTempDir(t)
	plantW51Lockfile(t, `{
		"echo-server":{"version":"0.9.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z"}
	}`)
	before, err := os.ReadFile(filepath.Join(dir, "pharos.lock"))
	if err != nil {
		t.Fatal(err)
	}

	runContract(t,
		map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
		"update", "--check")

	after, err := os.ReadFile(filepath.Join(dir, "pharos.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("--check modified the lockfile")
	}
	entry := originLockLoad(t, "echo-server")
	if entry.Version != "0.9.0" {
		t.Errorf("version after --check = %q, want 0.9.0 (unchanged)", entry.Version)
	}
}

// TestUpdateCheckHumanExtras: human mode prints the repo/changelog line
// under the preview row and suppresses it entirely for unknown hosts.
func TestUpdateCheckHumanExtras(t *testing.T) {
	checkRegistryWithRepo(t)
	fakeGenericClient(t)
	inTempDir(t)
	plantW51Lockfile(t, `{
		"echo-server":{"version":"0.9.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z"},
		"beta-server":{"version":"1.0.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z"}
	}`)

	_, combined := runContract(t,
		map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
		"update", "--check")

	if !strings.Contains(combined, "repo: https://github.com/example/echo") {
		t.Errorf("github repo line missing in:\n%s", combined)
	}
	if !strings.Contains(combined, "changelog: https://github.com/example/echo/releases") {
		t.Errorf("github changelog line missing in:\n%s", combined)
	}
	if strings.Contains(combined, "git.sr.ht") {
		t.Errorf("unknown host must not appear as a derived link:\n%s", combined)
	}
	if !strings.Contains(combined, "(check)") {
		t.Errorf("check-mode summary missing in:\n%s", combined)
	}
}

// TestUpdateCheckAndDryRunBothFlags: both flags together select the same
// no-apply mode with extras (documented alias behavior).
func TestUpdateCheckAndDryRunBothFlags(t *testing.T) {
	checkRegistryWithRepo(t)
	fakeGenericClient(t)
	inTempDir(t)
	plantW51Lockfile(t, `{
		"echo-server":{"version":"0.9.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z"}
	}`)

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"update", "--check", "--dry-run", "--json")

	report := parseUpdateReport(t, stdout)
	if !report.DryRun {
		t.Error("dry_run must be true when both --check and --dry-run are passed")
	}
	echo := findUpdateRow(t, report, "echo-server")
	if echo.Repo != "https://github.com/example/echo" {
		t.Errorf("repo = %q, want extras present under the combined mode", echo.Repo)
	}
	// No apply happened.
	entry := originLockLoad(t, "echo-server")
	if entry.Version != "0.9.0" {
		t.Errorf("version = %q, want unchanged", entry.Version)
	}
}

// ── derivation helper unit tests ────────────────────────────────────────

func TestNormalizeRepoURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://github.com/x/y", "https://github.com/x/y"},
		{"git+https://github.com/x/y.git", "https://github.com/x/y"},
		{"  https://github.com/x/y.git  ", "https://github.com/x/y"},
		{"git@github.com:x/y.git", "git@github.com:x/y"}, // scp-style left alone
		// C5: trailing slashes never survive (no ".../y//releases" joins).
		{"https://github.com/x/y/", "https://github.com/x/y"},
		{"https://github.com/x/y//", "https://github.com/x/y"},
		{"https://github.com/x/y.git/", "https://github.com/x/y"},
	}
	for _, tc := range cases {
		if got := normalizeRepoURL(tc.in); got != tc.want {
			t.Errorf("normalizeRepoURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGitHostReleasesBase(t *testing.T) {
	if _, _, ok := gitHostReleasesBase("echo-server@1.0.0"); ok {
		t.Error("a registry Ref is not a URL — must not derive")
	}
	if _, _, ok := gitHostReleasesBase("https://git.sr.ht/~x/y"); ok {
		t.Error("unknown host must not derive")
	}
	if _, _, ok := gitHostReleasesBase("ftp://github.com/x/y"); ok {
		t.Error("non-http(s) scheme must not derive")
	}
	if _, _, ok := gitHostReleasesBase(""); ok {
		t.Error("empty ref must not derive")
	}
	if repo, releases, ok := gitHostReleasesBase("git+https://github.com/x/y.git"); !ok || repo != "https://github.com/x/y" || releases != "releases" {
		t.Errorf("github derivation = %q/%q/%v", repo, releases, ok)
	}
	if _, releases, ok := gitHostReleasesBase("https://gitlab.com/x/y"); !ok || releases != "-/releases" {
		t.Errorf("gitlab releases path = %q (ok=%v), want -/releases", releases, ok)
	}
	// C5: a trailing-slash repo_url normalizes before the join.
	repo, releases, ok := gitHostReleasesBase("https://github.com/x/y/")
	if !ok || repo != "https://github.com/x/y" {
		t.Errorf("trailing-slash base = %q/%q/%v, want https://github.com/x/y/releases-path", repo, releases, ok)
	}
	if got := deriveChangelogURL(repo, releases); got != "https://github.com/x/y/releases" {
		t.Errorf("trailing-slash changelog join = %q, want https://github.com/x/y/releases (never //releases)", got)
	}
}

// TestUpdateReportJSONAbsentNot — JSON purity discipline: absent
// origin/repo/changelog (entry level) are OMITTED (never null), the
// report's pinned COUNTER is always present like its sibling counters,
// and servers stays a non-null array.
func TestUpdateReportJSONAbsentNot(t *testing.T) {
	report := updateReport{Servers: []updateEntry{{Name: "a", Action: "up_to_date"}}}
	data := mustMarshalIndent(report)
	s := string(data)
	if strings.Contains(s, "null") {
		t.Errorf("report serialized null for absent fields:\n%s", s)
	}
	for _, key := range []string{`"origin"`, `"repo"`, `"changelog"`, `"pinned"`} {
		if strings.Contains(s, `"pinned": true`) || strings.Contains(s, `"pinned":true`) {
			continue
		}
		if key != `"pinned"` && strings.Contains(s, key) {
			t.Errorf("absent %s must be omitted, not emitted:\n%s", key, s)
		}
	}
	if !strings.Contains(s, `"servers": [`) && !strings.Contains(s, `"servers":`) {
		t.Errorf("servers array missing:\n%s", s)
	}
	if !strings.Contains(s, `"pinned": 0`) {
		t.Errorf("pinned counter must always be present (sibling counters are):\n%s", s)
	}
}
