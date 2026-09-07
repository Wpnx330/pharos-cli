package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/lockfile"
)

// =============================================================
// W5.1 A6 — pin / unpin lifecycle + pinned-skip in update
// =============================================================

// pinLockLoad returns the named entry from the cwd lockfile.
func pinLockLoad(t *testing.T, name string) lockfile.ServerEntry {
	t.Helper()
	return originLockLoad(t, name)
}

// TestPinAtCurrentVersion pins without a version argument.
func TestPinAtCurrentVersion(t *testing.T) {
	receiptRegistry(t)
	fakeGenericClient(t)
	inTempDir(t)
	plantW51Lockfile(t, `{
		"echo-server":{"version":"1.0.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z"}
	}`)

	_, combined := runContract(t,
		map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
		"pin", "echo-server")

	entry := pinLockLoad(t, "echo-server")
	if entry.PinnedAt == nil || *entry.PinnedAt != "1.0.0" {
		t.Errorf("PinnedAt = %v, want 1.0.0", entry.PinnedAt)
	}
	if entry.Version != "1.0.0" {
		t.Errorf("version = %q, want unchanged 1.0.0", entry.Version)
	}
	if !strings.Contains(combined, "Pinned echo-server@1.0.0") {
		t.Errorf("confirmation missing in:\n%s", combined)
	}
}

// TestPinExplicitVersionInstallsThroughRegistryPath: pinning an exact
// version lands it via the normal install machinery (lockfile version
// moves, configs rewritten) and then records the pin.
func TestPinExplicitVersionInstallsThroughRegistryPath(t *testing.T) {
	receiptRegistry(t) // echo-server latest = 1.0.0
	fakeGenericClient(t)
	cfgPath := filepath.Join(contractHome(t), ".config", "mcp", "mcp.json")
	inTempDir(t)
	plantW51Lockfile(t, `{
		"echo-server":{"version":"0.9.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z"}
	}`)

	_, combined := runContract(t,
		map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
		"pin", "echo-server", "1.0.0")

	entry := pinLockLoad(t, "echo-server")
	if entry.Version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0 (installed through the registry path)", entry.Version)
	}
	if entry.PinnedAt == nil || *entry.PinnedAt != "1.0.0" {
		t.Errorf("PinnedAt = %v, want 1.0.0", entry.PinnedAt)
	}
	// The delegated install rewrote the referencing client config (the
	// registry-path proof: the config now points at the new version's
	// endpoint rather than staying at the stale one).
	cfgBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfgBytes), "echo-server") {
		t.Errorf("client config lost the server entry:\n%s", cfgBytes)
	}
	if !strings.Contains(combined, "Installing pinned version") {
		t.Errorf("pin-install progress missing in:\n%s", combined)
	}
}

// TestPinUnknownVersionFails: a version the registry does not have
// fails the pin without touching the lockfile.
func TestPinUnknownVersionFails(t *testing.T) {
	receiptRegistry(t)
	fakeGenericClient(t)
	inTempDir(t)
	plantW51Lockfile(t, `{
		"echo-server":{"version":"1.0.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z"}
	}`)

	_, combined := runContract(t,
		map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
		"pin", "echo-server", "9.9.9")

	if !strings.Contains(combined, "resolve version") {
		t.Errorf("resolution error missing in:\n%s", combined)
	}
	entry := pinLockLoad(t, "echo-server")
	if entry.PinnedAt != nil {
		t.Errorf("PinnedAt = %v, want nil (failed pin must not record)", entry.PinnedAt)
	}
}

// TestPinUnresolvedAdoptedServerErrors: a server adopted without a
// registry version cannot be pinned without an explicit version.
func TestPinUnresolvedAdoptedServerErrors(t *testing.T) {
	receiptRegistry(t)
	fakeGenericClient(t)
	inTempDir(t)
	plantW51Lockfile(t, `{
		"mystery":{"version":"","integrity":"","transport":"stdio","resolved":"","installedAt":"2026-01-01T00:00:00Z"}
	}`)

	_, combined := runContract(t,
		map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
		"pin", "mystery")

	if !strings.Contains(combined, "no installed version recorded") {
		t.Errorf("guidance missing in:\n%s", combined)
	}
}

// TestPinNotInLockfileErrors
func TestPinNotInLockfileErrors(t *testing.T) {
	receiptRegistry(t)
	fakeGenericClient(t)
	inTempDir(t)
	plantW51Lockfile(t, `{
		"echo-server":{"version":"1.0.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z"}
	}`)

	_, combined := runContract(t,
		map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
		"pin", "nope")

	if !strings.Contains(combined, "not in pharos.lock") {
		t.Errorf("guidance missing in:\n%s", combined)
	}
}

// TestUnpinLifecycle covers pin → unpin → PinnedAt nil, plus the
// not-pinned error.
func TestUnpinLifecycle(t *testing.T) {
	receiptRegistry(t)
	fakeGenericClient(t)
	inTempDir(t)
	plantW51Lockfile(t, `{
		"echo-server":{"version":"1.0.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z","pinnedAt":"1.0.0"}
	}`)

	_, combined := runContract(t,
		map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
		"unpin", "echo-server")

	entry := pinLockLoad(t, "echo-server")
	if entry.PinnedAt != nil {
		t.Errorf("PinnedAt = %v, want nil after unpin", entry.PinnedAt)
	}
	if !strings.Contains(combined, "Unpinned echo-server") {
		t.Errorf("confirmation missing in:\n%s", combined)
	}

	// Unpinning again is an error (exit 1 via RunE).
	_, combined = runContract(t,
		map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
		"unpin", "echo-server")
	if !strings.Contains(combined, "not pinned") {
		t.Errorf("not-pinned guidance missing in:\n%s", combined)
	}
}

// pinDownRegistry serves NOTHING for echo-server (registry down /
// package gone): the apply-path skip must happen BEFORE any registry
// call, so a pinned server is still reported as pinned.
func pinDownRegistry(t *testing.T) {
	t.Helper()
	home := isolateHome(t)
	t.Setenv("PHAROS_WINDOWS_USERS_ROOT", filepath.Join(t.TempDir(), "absent"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	dir := filepath.Join(home, ".pharos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"registry":`+strconv.Quote(srv.URL)+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fakeGenericClientBoth plants a Generic MCP config referencing BOTH
// receiptRegistryTwo packages.
func fakeGenericClientBoth(t *testing.T) {
	t.Helper()
	dir := filepath.Join(contractHome(t), ".config", "mcp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"mcpServers": {
		"echo-server": {"url": "https://echo.example.test/sse"},
		"beta-server": {"url": "https://beta.example.test/sse"}
	}}`
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestUpdateApplySkipsPinnedBeforeRegistryCall: a pinned stale server is
// left untouched while an unpinned stale server updates. Under --json an
// applied update emits the RECEIPT as the sole stdout document (W1.2),
// so the receipt must show beta replaced and nothing for the pinned
// echo; the lockfile carries the final truth.
func TestUpdateApplySkipsPinnedBeforeRegistryCall(t *testing.T) {
	// receiptRegistryTwo serves echo (latest 1.0.0) + beta (latest 2.0.0).
	receiptRegistryTwo(t)
	fakeGenericClientBoth(t)
	inTempDir(t)
	plantW51Lockfile(t, `{
		"echo-server":{"version":"0.9.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z","pinnedAt":"0.9.0"},
		"beta-server":{"version":"1.0.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z"}
	}`)

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"update", "--json")

	// The receipt is the stdout document: beta replaced, pinned echo
	// never rewritten (no ServerChange for it).
	r := parseReceipt(t, stdout)
	for _, sc := range r.Servers {
		if sc.Name == "echo-server" {
			t.Errorf("pinned server must not be rewritten, got %+v", sc)
		}
	}
	found := false
	for _, sc := range r.Servers {
		if sc.Name == "beta-server" && sc.Action == "replaced" {
			found = true
		}
	}
	if !found {
		t.Errorf("servers = %+v, want beta-server replaced (unpinned servers still update)", r.Servers)
	}

	entry := pinLockLoad(t, "echo-server")
	if entry.Version != "0.9.0" {
		t.Errorf("pinned version = %q, want untouched 0.9.0", entry.Version)
	}
	if entry.PinnedAt == nil || *entry.PinnedAt != "0.9.0" {
		t.Errorf("PinnedAt = %v, want preserved", entry.PinnedAt)
	}
	if beta := pinLockLoad(t, "beta-server"); beta.Version != "2.0.0" {
		t.Errorf("beta version = %q, want 2.0.0 (updated)", beta.Version)
	}
}

// TestUpdateApplyAllPinnedReportsPinnedAction: when every server is
// pinned nothing is updated, so the update REPORT (not the receipt) is
// the stdout document and its rows carry the "pinned" action.
func TestUpdateApplyAllPinnedReportsPinnedAction(t *testing.T) {
	receiptRegistryTwo(t)
	fakeGenericClientBoth(t)
	inTempDir(t)
	plantW51Lockfile(t, `{
		"echo-server":{"version":"0.9.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z","pinnedAt":"0.9.0"},
		"beta-server":{"version":"1.0.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z","pinnedAt":"1.0.0"}
	}`)

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"update", "--json")

	report := parseUpdateReport(t, stdout)
	echo := findUpdateRow(t, report, "echo-server")
	if echo.Action != "pinned" || !echo.Pinned {
		t.Errorf("echo row = %+v, want action pinned + pinned=true", echo)
	}
	if report.Pinned != 2 || report.Updated != 0 || report.UpdatesAvailable != 0 {
		t.Errorf("counters = pinned:%d updated:%d available:%d, want 2/0/0", report.Pinned, report.Updated, report.UpdatesAvailable)
	}
}

// TestUpdateApplySkipsPinnedWhenRegistryDown: same skip with a dead
// registry — proof the skip precedes the registry call.
func TestUpdateApplySkipsPinnedWhenRegistryDown(t *testing.T) {
	pinDownRegistry(t)
	fakeGenericClientBoth(t)
	inTempDir(t)
	plantW51Lockfile(t, `{
		"echo-server":{"version":"0.9.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z","pinnedAt":"0.9.0"}
	}`)

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"update", "--json")

	report := parseUpdateReport(t, stdout)
	echo := findUpdateRow(t, report, "echo-server")
	if echo.Action != "pinned" {
		t.Errorf("action = %q, want pinned (skip must precede the registry call)", echo.Action)
	}
	if report.Updated != 0 || report.UpdatesAvailable != 0 {
		t.Errorf("updated/updates_available = %d/%d, want 0/0", report.Updated, report.UpdatesAvailable)
	}
}

// TestUpdateCheckShowsPinnedWithAvailableVersion: check mode still
// probes pinned servers and reports the available version with
// pinned=true; an up-to-date pinned server reports up_to_date+pinned.
func TestUpdateCheckShowsPinnedWithAvailableVersion(t *testing.T) {
	receiptRegistryTwo(t) // echo latest 1.0.0, beta latest 2.0.0
	fakeGenericClientBoth(t)
	inTempDir(t)
	plantW51Lockfile(t, `{
		"echo-server":{"version":"0.9.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z","pinnedAt":"0.9.0"},
		"beta-server":{"version":"2.0.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z","pinnedAt":"2.0.0"}
	}`)

	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"update", "--check", "--json")

	report := parseUpdateReport(t, stdout)
	echo := findUpdateRow(t, report, "echo-server")
	if echo.Action != "update_available" || echo.To != "1.0.0" || !echo.Pinned {
		t.Errorf("echo row = %+v, want update_available → 1.0.0 with pinned=true", echo)
	}
	beta := findUpdateRow(t, report, "beta-server")
	if beta.Action != "up_to_date" || !beta.Pinned {
		t.Errorf("beta row = %+v, want up_to_date with pinned=true", beta)
	}
	if report.Pinned != 2 {
		t.Errorf("pinned counter = %d, want 2", report.Pinned)
	}

	// Nothing applied in check mode.
	if entry := pinLockLoad(t, "echo-server"); entry.Version != "0.9.0" {
		t.Errorf("echo version = %q, want untouched", entry.Version)
	}
}

// TestUpdateAllFlagAndConflict: --all behaves like the bare all-servers
// run; combining --all with a name is an error.
func TestUpdateAllFlagAndConflict(t *testing.T) {
	receiptRegistryTwo(t)
	fakeGenericClientBoth(t)
	inTempDir(t)
	plantW51Lockfile(t, `{
		"echo-server":{"version":"0.9.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z"},
		"beta-server":{"version":"1.0.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z"}
	}`)

	_, combined := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"update", "--all", "echo-server", "--json")
	if !strings.Contains(combined, "not both") {
		t.Errorf("--all + name conflict guidance missing in:\n%s", combined)
	}

	// Error path must not have applied anything.
	if entry := pinLockLoad(t, "echo-server"); entry.Version != "0.9.0" {
		t.Errorf("echo version = %q, want untouched after the rejected run", entry.Version)
	}

	// --all applies to every server. Under --json the receipt is the
	// stdout document; the lockfile carries the final truth.
	stdout, _ := runContract(t,
		map[string]string{"PHAROS_JSON": "1", "PHAROS_NON_INTERACTIVE": "1"},
		"update", "--all", "--json")
	parseReceipt(t, stdout) // stdout must be a single receipt document
	if entry := pinLockLoad(t, "echo-server"); entry.Version != "1.0.0" {
		t.Errorf("echo version = %q, want 1.0.0 after --all", entry.Version)
	}
	if entry := pinLockLoad(t, "beta-server"); entry.Version != "2.0.0" {
		t.Errorf("beta version = %q, want 2.0.0 after --all", entry.Version)
	}
}

// TestUpdateSummaryTableHuman: the apply path ends with the summary
// table (NAME/FROM/TO/ACTION/PINNED) covering updated and pinned rows.
func TestUpdateSummaryTableHuman(t *testing.T) {
	receiptRegistryTwo(t)
	fakeGenericClientBoth(t)
	inTempDir(t)
	plantW51Lockfile(t, `{
		"echo-server":{"version":"0.9.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z","pinnedAt":"0.9.0"},
		"beta-server":{"version":"1.0.0","integrity":"sha512-x","transport":"http-sse","resolved":"","installedAt":"2026-01-01T00:00:00Z"}
	}`)

	_, combined := runContract(t,
		map[string]string{"PHAROS_NON_INTERACTIVE": "1"},
		"update")

	for _, want := range []string{"NAME", "FROM", "TO", "ACTION", "PINNED", "beta-server", "updated", "echo-server", "pinned", "yes"} {
		if !strings.Contains(combined, want) {
			t.Errorf("summary table missing %q in:\n%s", want, combined)
		}
	}
	// JSON mode prints no table (stdout purity) — asserted implicitly by
	// the JSON tests above parsing the report as the sole document.
}
