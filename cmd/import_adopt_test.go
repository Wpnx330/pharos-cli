package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/canonical"
	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
)

// ── adopt test harness ──────────────────────────────────────────────────────
//
// Every adopt test runs inside the drift harness (driftIsolate): HOME at a
// fresh temp dir, cwd at a fresh temp dir (pharos.lock lands in cwd), and
// PHAROS_WINDOWS_USERS_ROOT pointed at an absent dir so the WSL2 mirrors of
// the real machine are never detected. All paths go through
// filepath.Join / driftBuiltinClient, so the suite is Windows-safe.

// adoptUnresolvedRegistry returns an API client pointed at a local stub
// registry that 404s everything (fast, deterministic, fully offline).
func adoptUnresolvedRegistry(t *testing.T) *api.Client {
	t.Helper()
	return adoptFakeRegistry(t, nil)
}

// adoptFakeRegistry returns an API client against a stub registry serving
// the given package name → packument JSON bodies (404 otherwise).
func adoptFakeRegistry(t *testing.T, pkgs map[string]string) *api.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/v1/packages/")
		if body, ok := pkgs[name]; ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return api.New(srv.URL, "")
}

// adoptRegistryPkg builds a minimal packument for a stdio package.
func adoptRegistryPkg(name, version, integrity string) string {
	return fmt.Sprintf(`{
		"name": %[1]q,
		"dist_tags": {"latest": %[2]q},
		"versions": [
			{
				"version": %[2]q,
				"manifest": {"name": %[1]q, "version": %[2]q, "transport": "stdio", "integrity": %[3]q}
			}
		]
	}`, name, version, integrity)
}

// adoptRun executes the adopt flow and fails the test on a hard error.
func adoptRun(t *testing.T, opts adoptOptions) (*adoptReport, int) {
	t.Helper()
	report, code, err := runAdoptImport(opts)
	if err != nil {
		t.Fatalf("runAdoptImport failed: %v", err)
	}
	return report, code
}

// adoptLock asserts the server is in the cwd lockfile and returns its entry.
func adoptLock(t *testing.T, name string) lockfile.ServerEntry {
	t.Helper()
	lf, err := lockfile.Load("pharos.lock")
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := lf.Get(name)
	if !ok {
		t.Fatalf("server %q not found in lockfile", name)
	}
	return entry
}

// adoptCanon asserts the server is in the canonical config and returns it.
func adoptCanon(t *testing.T, name string) canonical.Server {
	t.Helper()
	cfg, err := canonical.Load()
	if err != nil {
		t.Fatal(err)
	}
	srv, ok := cfg.Servers[name]
	if !ok {
		t.Fatalf("server %q not found in canonical config", name)
	}
	return srv
}

// adoptCanonicalPath returns the isolated canonical file path.
func adoptCanonicalPath(t *testing.T) string {
	t.Helper()
	path, err := canonical.FilePath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// adoptReadClient reads back a client config's raw server entries.
func adoptReadClient(t *testing.T, c clientconfig.Client) map[string]json.RawMessage {
	t.Helper()
	servers, err := clientconfig.ReadServersFormat(c.Path, c.Format)
	if err != nil {
		t.Fatal(err)
	}
	return servers
}

// adoptSnapshotFile returns the file bytes (nil when absent) so tests can
// prove adopt did not rewrite a client config.
func adoptSnapshotFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	return data
}

// adoptCountLockfile returns the number of servers in the cwd lockfile.
func adoptCountLockfile(t *testing.T) int {
	t.Helper()
	lf, err := lockfile.Load("pharos.lock")
	if err != nil {
		t.Fatal(err)
	}
	return len(lf.Servers)
}

// adoptRow finds a server row in the report.
func adoptRow(t *testing.T, report *adoptReport, name string) adoptServerRow {
	t.Helper()
	for _, row := range report.Servers {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("server %q missing from adopt report", name)
	return adoptServerRow{}
}

// recordingStdin counts reads — used to prove JSON mode never prompts.
type recordingStdin struct{ reads int }

func (r *recordingStdin) Read(p []byte) (int, error) {
	r.reads++
	return 0, io.EOF
}

// ── scenarios ───────────────────────────────────────────────────────────────

// AdoptSingleClient: one client, one registry-resolved server + one
// unresolved one; both adopt as managed, client config untouched.
func TestAdoptSingleClient(t *testing.T) {
	home := driftIsolate(t)
	c := driftGenericClient(home)
	plantDriftServer(t, c, "echo-server", driftStdioCfg)
	plantDriftServer(t, c, "mystery", driftStdioCfg)
	before := adoptSnapshotFile(t, c.Path)

	opts := adoptOptions{
		API: adoptFakeRegistry(t, map[string]string{
			"echo-server": adoptRegistryPkg("echo-server", "1.0.0", "sha512-abc"),
		}),
	}
	report, code := adoptRun(t, opts)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if report.Found != 2 || report.Adopted != 2 {
		t.Errorf("found/adopted = %d/%d, want 2/2", report.Found, report.Adopted)
	}
	if report.UnresolvedRegistry != 1 {
		t.Errorf("unresolved = %d, want 1 (mystery)", report.UnresolvedRegistry)
	}

	entry := adoptLock(t, "echo-server")
	if entry.Version != "1.0.0" || entry.Integrity != "sha512-abc" {
		t.Errorf("echo-server lock entry = %+v, want version 1.0.0 + integrity", entry)
	}
	if entry.Transport != "stdio" {
		t.Errorf("transport = %q, want stdio (registry manifest)", entry.Transport)
	}
	if len(entry.Clients) != 1 || entry.Clients[0] != "generic" {
		t.Errorf("clients = %v, want [generic]", entry.Clients)
	}

	// Unresolved servers still adopt as managed (config-truth wins).
	mystery := adoptLock(t, "mystery")
	if mystery.Version != "" {
		t.Errorf("mystery version = %q, want empty", mystery.Version)
	}
	if len(mystery.Clients) != 1 || mystery.Clients[0] != "generic" {
		t.Errorf("mystery clients = %v, want [generic]", mystery.Clients)
	}

	canon := adoptCanon(t, "echo-server")
	if canon.Package.Version != "1.0.0" || canon.Command != "node" {
		t.Errorf("canonical echo-server = %+v", canon)
	}
	if !canon.Enabled {
		t.Error("adopted canonical server must be enabled")
	}
	adoptCanon(t, "mystery") // must exist

	// Adopt reads; it never rewrites the source config.
	if !bytes.Equal(before, adoptSnapshotFile(t, c.Path)) {
		t.Error("adopt rewrote the source client config")
	}

	row := adoptRow(t, report, "echo-server")
	if row.Status != "adopted" || row.Version != "1.0.0" || row.SourceClient != "generic" {
		t.Errorf("echo-server row = %+v", row)
	}
	if report.Next != "Run 'pharos doctor --diff' to verify clean state." {
		t.Errorf("next hint = %q", report.Next)
	}
}

// AdoptMultiClientDedupe: the same server in two clients with identical
// configs is one adopted server recorded for both clients.
func TestAdoptMultiClientDedupe(t *testing.T) {
	home := driftIsolate(t)
	generic := driftGenericClient(home)
	cursor := driftBuiltinClient(t, clientconfig.ClientCursor)

	cfg := clientconfig.ServerConfig{
		Command: "node",
		Args:    []string{"server.js"},
		Env:     map[string]string{"SHARED": "1"},
		Type:    "stdio",
	}
	plantDriftServer(t, generic, "shared", cfg)
	plantDriftServer(t, cursor, "shared", cfg)
	plantDriftServer(t, generic, "only-generic", cfg)
	plantDriftServer(t, cursor, "only-cursor", cfg)

	report, code := adoptRun(t, adoptOptions{API: adoptUnresolvedRegistry(t)})

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if report.Found != 3 {
		t.Errorf("found = %d, want 3 (deduped)", report.Found)
	}
	if report.ClientsScanned != 2 {
		t.Errorf("clients_scanned = %d, want 2", report.ClientsScanned)
	}
	if report.Conflicts != 0 {
		t.Errorf("conflicts = %d, want 0", report.Conflicts)
	}

	entry := adoptLock(t, "shared")
	if len(entry.Clients) != 2 {
		t.Fatalf("shared clients = %v, want [cursor generic]", entry.Clients)
	}
	if entry.Clients[0] != "cursor" || entry.Clients[1] != "generic" {
		t.Errorf("shared clients = %v, want [cursor generic]", entry.Clients)
	}
	if adoptCountLockfile(t) != 3 {
		t.Errorf("lockfile has %d servers, want 3", adoptCountLockfile(t))
	}

	row := adoptRow(t, report, "shared")
	if len(row.Clients) != 2 {
		t.Errorf("report row clients = %v, want both", row.Clients)
	}
}

// AdoptUnresolvedStillManaged: nothing resolves, everything still adopts.
func TestAdoptUnresolvedStillManaged(t *testing.T) {
	home := driftIsolate(t)
	generic := driftGenericClient(home)
	plantDriftServer(t, generic, "srv-a", driftStdioCfg)
	plantDriftServer(t, generic, "srv-b", driftStdioCfg)

	report, code := adoptRun(t, adoptOptions{API: adoptUnresolvedRegistry(t)})

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if report.UnresolvedRegistry != 2 || report.Adopted != 2 {
		t.Errorf("unresolved/adopted = %d/%d, want 2/2", report.UnresolvedRegistry, report.Adopted)
	}
	entry := adoptLock(t, "srv-a")
	if entry.Version != "" || entry.Integrity != "" {
		t.Errorf("unresolved entry should carry no registry data: %+v", entry)
	}
	if entry.Transport != "stdio" {
		t.Errorf("transport derived from config = %q, want stdio", entry.Transport)
	}
	adoptCanon(t, "srv-b")
}

// AdoptConflictDetected: materially different configs across clients are
// flagged; in non-interactive mode the conflict is skipped (exit 1) while
// the rest adopts, and no client config is rewritten.
func TestAdoptConflictDetected(t *testing.T) {
	t.Setenv("PHAROS_NON_INTERACTIVE", "1")
	home := driftIsolate(t)
	generic := driftGenericClient(home)
	cursor := driftBuiltinClient(t, clientconfig.ClientCursor)

	a := clientconfig.ServerConfig{Command: "node", Args: []string{"a.js"}, Env: map[string]string{"WHO": "generic"}, Type: "stdio"}
	b := clientconfig.ServerConfig{Command: "node", Args: []string{"b.js"}, Env: map[string]string{"WHO": "cursor"}, Type: "stdio"}
	plantDriftServer(t, generic, "shared", a)
	plantDriftServer(t, cursor, "shared", b)
	plantDriftServer(t, generic, "other", a)
	plantDriftServer(t, cursor, "other", a) // identical → no conflict
	genericBefore := adoptSnapshotFile(t, generic.Path)
	cursorBefore := adoptSnapshotFile(t, cursor.Path)

	report, code := adoptRun(t, adoptOptions{API: adoptUnresolvedRegistry(t)})

	if code != 1 {
		t.Errorf("exit code = %d, want 1 (conflict skipped)", code)
	}
	if report.Conflicts != 1 || report.ConflictsSkipped != 1 {
		t.Errorf("conflicts/skipped = %d/%d, want 1/1", report.Conflicts, report.ConflictsSkipped)
	}
	row := adoptRow(t, report, "shared")
	if row.Status != "conflict-skipped" {
		t.Errorf("status = %q, want conflict-skipped", row.Status)
	}
	if len(row.Conflict.Variants) != 2 {
		t.Errorf("conflict variants = %d, want 2", len(row.Conflict.Variants))
	}

	// Non-conflicting servers still adopt.
	if adoptRow(t, report, "other").Status != "adopted" {
		t.Error("other should adopt")
	}
	if adoptCountLockfile(t) != 1 {
		t.Errorf("lockfile has %d servers, want 1 (only 'other')", adoptCountLockfile(t))
	}

	// Adopt never rewrites source configs.
	if !bytes.Equal(genericBefore, adoptSnapshotFile(t, generic.Path)) ||
		!bytes.Equal(cursorBefore, adoptSnapshotFile(t, cursor.Path)) {
		t.Error("adopt rewrote a source client config")
	}
}

// AdoptConflictInteractivePick: the prompt parses picks, use-everywhere
// (with and without an explicit variant), skips, EOF, and re-asks on
// garbage input.
func TestAdoptConflictInteractivePick(t *testing.T) {
	variants := []adoptVariant{
		{
			Raw:     json.RawMessage(`{"command":"node","args":["a.js"],"env":{"WHO":"generic"}}`),
			Config:  clientconfig.ServerConfig{Command: "node", Args: []string{"a.js"}, Env: map[string]string{"WHO": "generic"}},
			Clients: []clientconfig.Client{{ID: clientconfig.ClientGeneric, Name: "Generic MCP"}},
		},
		{
			Raw:     json.RawMessage(`{"command":"node","args":["b.js"],"env":{"WHO":"cursor"}}`),
			Config:  clientconfig.ServerConfig{Command: "node", Args: []string{"b.js"}, Env: map[string]string{"WHO": "cursor"}},
			Clients: []clientconfig.Client{{ID: clientconfig.ClientCursor, Name: "Cursor"}},
		},
	}

	t.Run("pick second variant", func(t *testing.T) {
		var out bytes.Buffer
		pick, everywhere, skip := promptAdoptConflict(&out, bufio.NewReader(strings.NewReader("2\n")), "srv", variants)
		if skip || everywhere || pick != 1 {
			t.Errorf("pick=%d everywhere=%v skip=%v, want 1/false/false", pick, everywhere, skip)
		}
		if !strings.Contains(out.String(), "2 distinct configs") {
			t.Errorf("prompt must show the variant count, got %q", out.String())
		}
		if !strings.Contains(out.String(), "args") {
			t.Errorf("prompt must show the per-field diff, got %q", out.String())
		}
	})

	t.Run("use everywhere defaults to first", func(t *testing.T) {
		pick, everywhere, skip := promptAdoptConflict(&bytes.Buffer{}, bufio.NewReader(strings.NewReader("u\n")), "srv", variants)
		if skip || !everywhere || pick != 0 {
			t.Errorf("pick=%d everywhere=%v skip=%v, want 0/true/false", pick, everywhere, skip)
		}
	})

	t.Run("use everywhere explicit variant", func(t *testing.T) {
		pick, everywhere, skip := promptAdoptConflict(&bytes.Buffer{}, bufio.NewReader(strings.NewReader("u2\n")), "srv", variants)
		if skip || !everywhere || pick != 1 {
			t.Errorf("pick=%d everywhere=%v skip=%v, want 1/true/false", pick, everywhere, skip)
		}
	})

	t.Run("u out of range re-asks", func(t *testing.T) {
		// Above-range, zero, and negative N all re-ask (2 prompts); the
		// following bare pick then resolves normally.
		for _, tt := range []struct {
			in   string
			pick int
		}{
			{"u9\n2\n", 1},
			{"u0\n1\n", 0},
			{"u-1\n1\n", 0},
		} {
			var out bytes.Buffer
			pick, everywhere, skip := promptAdoptConflict(&out, bufio.NewReader(strings.NewReader(tt.in)), "srv", variants)
			if skip || everywhere || pick != tt.pick {
				t.Errorf("input %q: pick=%d everywhere=%v skip=%v, want %d/false/false (u[N] must re-ask, then the bare pick resolves)", tt.in, pick, everywhere, skip, tt.pick)
			}
			if got := strings.Count(out.String(), "Pick 1-"); got != 2 {
				t.Errorf("input %q: prompt asked %d time(s), want 2 — out-of-range u[N] must re-ask instead of silently using variant 1", tt.in, got)
			}
		}
	})

	t.Run("skip word and bare s", func(t *testing.T) {
		for _, in := range []string{"s\n", "skip\n"} {
			_, _, skip := promptAdoptConflict(&bytes.Buffer{}, bufio.NewReader(strings.NewReader(in)), "srv", variants)
			if !skip {
				t.Errorf("input %q: want skip", in)
			}
		}
	})

	t.Run("EOF skips", func(t *testing.T) {
		_, _, skip := promptAdoptConflict(&bytes.Buffer{}, bufio.NewReader(strings.NewReader("")), "srv", variants)
		if !skip {
			t.Error("EOF should skip")
		}
	})

	t.Run("garbage then valid", func(t *testing.T) {
		pick, everywhere, skip := promptAdoptConflict(&bytes.Buffer{}, bufio.NewReader(strings.NewReader("zz\n9\n1\n")), "srv", variants)
		if skip || everywhere || pick != 0 {
			t.Errorf("pick=%d everywhere=%v skip=%v, want 0/false/false", pick, everywhere, skip)
		}
	})
}

// AdoptConflictYesAutoFirst: --yes resolves the conflict deterministically
// with the FIRST detected client's config, no prompt, exit 0.
func TestAdoptConflictYesAutoFirst(t *testing.T) {
	driftIsolate(t)
	desktop := driftBuiltinClient(t, clientconfig.ClientClaudeDesktop)
	cursor := driftBuiltinClient(t, clientconfig.ClientCursor)

	// Claude Desktop is first in Detect() order; its config must win.
	a := clientconfig.ServerConfig{Command: "node", Args: []string{"desktop.js"}, Env: map[string]string{"WHO": "desktop"}, Type: "stdio"}
	b := clientconfig.ServerConfig{Command: "node", Args: []string{"cursor.js"}, Env: map[string]string{"WHO": "cursor"}, Type: "stdio"}
	plantDriftServer(t, desktop, "shared", a)
	plantDriftServer(t, cursor, "shared", b)

	// Replace stdin with a recorder — --yes must never read it.
	rec := &recordingStdin{}
	old := adoptStdin
	adoptStdin = rec
	t.Cleanup(func() { adoptStdin = old })

	report, code := adoptRun(t, adoptOptions{API: adoptUnresolvedRegistry(t), Yes: true})

	if code != 0 {
		t.Errorf("exit code = %d, want 0 (auto-resolved is not skipped)", code)
	}
	if rec.reads != 0 {
		t.Errorf("--yes read stdin %d time(s); must not prompt", rec.reads)
	}
	row := adoptRow(t, report, "shared")
	if row.Status != "conflict-auto-resolved" {
		t.Errorf("status = %q, want conflict-auto-resolved", row.Status)
	}
	if row.SourceClient != "claude-desktop" {
		t.Errorf("source_client = %q, want claude-desktop (first in Detect order)", row.SourceClient)
	}
	canon := adoptCanon(t, "shared")
	if canon.Args == nil || canon.Args[0] != "desktop.js" {
		t.Errorf("canonical args = %v, want the first client's config", canon.Args)
	}
	if report.ConflictsResolved != 1 || report.ConflictsSkipped != 0 {
		t.Errorf("resolved/skipped = %d/%d, want 1/0", report.ConflictsResolved, report.ConflictsSkipped)
	}
}

// AdoptPickWithoutEverywhereShowsHonestDrift: auto-resolving a conflict
// WITHOUT "use everywhere" (--yes picks the first client's config) leaves
// the unpicked client on its own variant — doctor --diff must honestly
// report that as a MODIFIED drift finding, not read it as clean. This
// pins the lockfile Clients-union design: the drift IS the record of
// which variant won.
func TestAdoptPickWithoutEverywhereShowsHonestDrift(t *testing.T) {
	driftIsolate(t)
	desktop := driftBuiltinClient(t, clientconfig.ClientClaudeDesktop)
	cursor := driftBuiltinClient(t, clientconfig.ClientCursor)

	// Claude Desktop is first in Detect() order; --yes picks its config.
	a := clientconfig.ServerConfig{Command: "node", Args: []string{"desktop.js"}, Env: map[string]string{"WHO": "desktop"}, Type: "stdio"}
	b := clientconfig.ServerConfig{Command: "node", Args: []string{"cursor.js"}, Env: map[string]string{"WHO": "cursor"}, Type: "stdio"}
	plantDriftServer(t, desktop, "shared", a)
	plantDriftServer(t, cursor, "shared", b)

	report, code := adoptRun(t, adoptOptions{API: adoptUnresolvedRegistry(t), Yes: true})

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	row := adoptRow(t, report, "shared")
	if row.Status != "conflict-auto-resolved" || row.UseEverywhere {
		t.Fatalf("row = %+v, want conflict-auto-resolved without use_everywhere", row)
	}

	checks, note := runDriftChecks()
	if note != "" {
		t.Fatalf("drift note = %q, want none", note)
	}
	var picked, unpicked *doctorCheck
	for i := range checks {
		switch {
		case strings.Contains(checks[i].Name, "Claude Desktop"):
			picked = &checks[i]
		case strings.Contains(checks[i].Name, "Cursor"):
			unpicked = &checks[i]
		}
	}
	if picked == nil || unpicked == nil {
		t.Fatalf("expected drift checks for both clients, got %+v", checks)
	}

	// The client whose variant won reads clean.
	if picked.Status != "ok" {
		t.Errorf("picked-client drift check = %s (%s) findings=%+v, want ok", picked.Status, picked.Error, picked.Findings)
	}
	// The client keeping the other variant shows the honest MODIFIED drift.
	if unpicked.Status != "fail" {
		t.Errorf("unpicked-client drift check = %s (%s), want fail (intentional drift)", unpicked.Status, unpicked.Error)
	}
	found := false
	for _, f := range unpicked.Findings {
		if f.Server == "shared" && f.Kind == "modified" {
			found = true
		}
	}
	if !found {
		t.Errorf("unpicked-client findings = %+v, want a modified finding for 'shared'", unpicked.Findings)
	}
}

// AdoptHumanNextHint: the human-mode Next hint is status-aware — a
// conflict resolved by picking one variant without "use everywhere"
// (interactive pick or --yes auto-first) warns that unpicked clients will
// show as drift; every other shape keeps the plain clean-state hint. The
// JSON "next" field stays the plain hint in all cases.
func TestAdoptHumanNextHint(t *testing.T) {
	clean := &adoptReport{Servers: []adoptServerRow{
		{Name: "a", Status: adoptStatusAdopted},
	}}

	everywhere := &adoptReport{Servers: []adoptServerRow{
		{Name: "a", Status: adoptStatusResolved, UseEverywhere: true, Conflict: &adoptConflictInfo{Resolution: "use-everywhere"}},
	}}

	picked := &adoptReport{Servers: []adoptServerRow{
		{Name: "a", Status: adoptStatusResolved, Conflict: &adoptConflictInfo{Resolution: "picked"}},
	}}

	auto := &adoptReport{Servers: []adoptServerRow{
		{Name: "a", Status: adoptStatusAutoResolved, Conflict: &adoptConflictInfo{Resolution: "auto-first"}},
	}}

	skipped := &adoptReport{Servers: []adoptServerRow{
		{Name: "a", Status: adoptStatusSkipped, Conflict: &adoptConflictInfo{Resolution: "skipped"}},
	}}

	tests := []struct {
		name   string
		report *adoptReport
		want   string
	}{
		{"clean adopt keeps the clean-state hint", clean, adoptNextHint},
		{"use-everywhere resolution keeps the clean-state hint", everywhere, adoptNextHint},
		{"picked-variant resolution warns about drift", picked, adoptNextHintPicked},
		{"--yes auto-first resolution warns about drift", auto, adoptNextHintPicked},
		{"skipped conflict keeps the clean-state hint", skipped, adoptNextHint},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adoptHumanNextHint(tt.report); got != tt.want {
				t.Errorf("adoptHumanNextHint = %q, want %q", got, tt.want)
			}
		})
	}
}

// AdoptConflictJSONNoPrompt: JSON mode reports both configs and skips the
// conflict without ever prompting; non-conflicting servers still adopt.
func TestAdoptConflictJSONNoPrompt(t *testing.T) {
	t.Setenv("PHAROS_JSON", "1")
	home := driftIsolate(t)
	generic := driftGenericClient(home)
	cursor := driftBuiltinClient(t, clientconfig.ClientCursor)

	a := clientconfig.ServerConfig{Command: "node", Args: []string{"a.js"}, Env: map[string]string{"WHO": "generic"}, Type: "stdio"}
	b := clientconfig.ServerConfig{Command: "node", Args: []string{"b.js"}, Env: map[string]string{"WHO": "cursor"}, Type: "stdio"}
	plantDriftServer(t, generic, "shared", a)
	plantDriftServer(t, cursor, "shared", b)
	plantDriftServer(t, generic, "plain", a)

	rec := &recordingStdin{}
	old := adoptStdin
	adoptStdin = rec
	t.Cleanup(func() { adoptStdin = old })

	report, code := adoptRun(t, adoptOptions{API: adoptUnresolvedRegistry(t)})

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if rec.reads != 0 {
		t.Errorf("JSON mode read stdin %d time(s); must never prompt", rec.reads)
	}
	row := adoptRow(t, report, "shared")
	if row.Status != "conflict-skipped" {
		t.Errorf("status = %q, want conflict-skipped", row.Status)
	}
	if len(row.Conflict.Variants) != 2 {
		t.Fatalf("variants = %d, want 2", len(row.Conflict.Variants))
	}
	for i, v := range row.Conflict.Variants {
		var cfg map[string]any
		if err := json.Unmarshal(v.Config, &cfg); err != nil {
			t.Fatalf("variant %d config not JSON: %v", i, err)
		}
		if len(v.Clients) == 0 {
			t.Errorf("variant %d must name its clients", i)
		}
	}
	// Both configs present: one says a.js, the other b.js.
	joined := string(row.Conflict.Variants[0].Config) + string(row.Conflict.Variants[1].Config)
	if !strings.Contains(joined, "a.js") || !strings.Contains(joined, "b.js") {
		t.Errorf("conflict report must carry both configs, got %s", joined)
	}
	if _, ok := adoptLockMaybe(t, "shared"); ok {
		t.Error("skipped conflict must not be adopted into the lockfile")
	}
	if adoptRow(t, report, "plain").Status != "adopted" {
		t.Error("non-conflicting server must still adopt in JSON mode")
	}
}

// adoptLockMaybe returns the lockfile entry when present.
func adoptLockMaybe(t *testing.T, name string) (lockfile.ServerEntry, bool) {
	t.Helper()
	lf, err := lockfile.Load("pharos.lock")
	if err != nil {
		t.Fatal(err)
	}
	return lf.Get(name)
}

// AdoptDryRunWritesNothing: the full report is produced, but the lockfile,
// canonical config, and client files are untouched.
func TestAdoptDryRunWritesNothing(t *testing.T) {
	home := driftIsolate(t)
	generic := driftGenericClient(home)
	cursor := driftBuiltinClient(t, clientconfig.ClientCursor)

	cfg := clientconfig.ServerConfig{Command: "node", Args: []string{"server.js"}, Env: map[string]string{"K": "v"}, Type: "stdio"}
	plantDriftServer(t, generic, "srv", cfg)
	plantDriftServer(t, cursor, "srv", cfg)
	genericBefore := adoptSnapshotFile(t, generic.Path)
	cursorBefore := adoptSnapshotFile(t, cursor.Path)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(cwd, "pharos.lock")

	report, code := adoptRun(t, adoptOptions{API: adoptUnresolvedRegistry(t), DryRun: true})

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !report.DryRun {
		t.Error("report must be marked dry_run")
	}
	if report.Found != 1 || report.Adopted != 1 {
		t.Errorf("found/adopted = %d/%d, want 1/1", report.Found, report.Adopted)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("dry run must not write pharos.lock")
	}
	if _, err := os.Stat(adoptCanonicalPath(t)); !os.IsNotExist(err) {
		t.Error("dry run must not write ~/.pharos/mcp.json")
	}
	if !bytes.Equal(genericBefore, adoptSnapshotFile(t, generic.Path)) ||
		!bytes.Equal(cursorBefore, adoptSnapshotFile(t, cursor.Path)) {
		t.Error("dry run rewrote a client config")
	}
}

// AdoptFromClientSubset: --from limits the adopt to one client, and the
// alias merge rejects conflicting --client/--from values.
func TestAdoptFromClientSubset(t *testing.T) {
	home := driftIsolate(t)
	generic := driftGenericClient(home)
	cursor := driftBuiltinClient(t, clientconfig.ClientCursor)

	cfg := clientconfig.ServerConfig{Command: "node", Args: []string{"server.js"}, Type: "stdio"}
	plantDriftServer(t, generic, "from-generic", cfg)
	plantDriftServer(t, cursor, "from-cursor", cfg)

	report, code := adoptRun(t, adoptOptions{API: adoptUnresolvedRegistry(t), Client: "cursor"})

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if report.ClientsScanned != 1 {
		t.Errorf("clients_scanned = %d, want 1", report.ClientsScanned)
	}
	if report.Found != 1 {
		t.Errorf("found = %d, want 1", report.Found)
	}
	if _, ok := adoptLockMaybe(t, "from-cursor"); !ok {
		t.Error("cursor's server must adopt")
	}
	if _, ok := adoptLockMaybe(t, "from-generic"); ok {
		t.Error("generic's server must not adopt when --from cursor")
	}

	// Alias semantics: --from mirrors --client.
	got, err := resolveImportFromAlias("", "cursor")
	if err != nil || got != "cursor" {
		t.Errorf("alias merge = %q,%v; want cursor,nil", got, err)
	}
	got, err = resolveImportFromAlias("cursor", "cursor")
	if err != nil || got != "cursor" {
		t.Errorf("equal alias merge = %q,%v; want cursor,nil", got, err)
	}
	if _, err = resolveImportFromAlias("generic", "cursor"); err == nil {
		t.Error("conflicting --client and --from must error")
	}
}

// AdoptUseEverywhereRewritesBoth: "u" adopts the first variant and writes
// it to every client that had the server, leaving doctor --diff clean.
func TestAdoptUseEverywhereRewritesBoth(t *testing.T) {
	home := driftIsolate(t)
	generic := driftGenericClient(home)
	cursor := driftBuiltinClient(t, clientconfig.ClientCursor)

	a := clientconfig.ServerConfig{Command: "node", Args: []string{"a.js"}, Env: map[string]string{"WHO": "generic"}, Type: "stdio"}
	b := clientconfig.ServerConfig{Command: "node", Args: []string{"b.js"}, Env: map[string]string{"WHO": "cursor"}, Type: "stdio"}
	plantDriftServer(t, generic, "shared", a)
	plantDriftServer(t, cursor, "shared", b)

	// Cursor precedes Generic in Detect() order, so variant [1] is
	// cursor's config; "u" adopts it everywhere.
	old := adoptStdin
	adoptStdin = strings.NewReader("u\n")
	t.Cleanup(func() { adoptStdin = old })

	report, code := adoptRun(t, adoptOptions{API: adoptUnresolvedRegistry(t)})

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	row := adoptRow(t, report, "shared")
	if row.Status != "conflict-resolved" || !row.UseEverywhere {
		t.Errorf("row = %+v, want conflict-resolved + use_everywhere", row)
	}
	if row.Conflict.Resolution != "use-everywhere" {
		t.Errorf("resolution = %q, want use-everywhere", row.Conflict.Resolution)
	}
	if row.SourceClient != "cursor" {
		t.Errorf("source_client = %q, want cursor (first detected)", row.SourceClient)
	}

	canon := adoptCanon(t, "shared")
	if canon.Args == nil || canon.Args[0] != "b.js" || canon.Env["WHO"] != "cursor" {
		t.Errorf("canonical = %+v, want the picked (first detected) config", canon)
	}

	// The client that differed is rewritten to the picked config.
	got := adoptReadClient(t, generic)
	raw, ok := got["shared"]
	if !ok {
		t.Fatal("generic config lost 'shared'")
	}
	var entry struct {
		Args []string          `json:"args"`
		Env  map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatal(err)
	}
	if len(entry.Args) != 1 || entry.Args[0] != "b.js" || entry.Env["WHO"] != "cursor" {
		t.Errorf("generic entry after use-everywhere = %+v, want the picked config", entry)
	}

	// The resolution promise: doctor --diff reads clean afterwards.
	checks, note := runDriftChecks()
	if note != "" {
		t.Fatalf("drift note = %q, want none", note)
	}
	for _, check := range checks {
		if check.Status != "ok" {
			t.Errorf("drift check %q = %s (%s) %+v", check.Name, check.Status, check.Error, check.Findings)
		}
	}
}

// AdoptMultiConflictSequentialStdin: two conflicts in one run are fed by
// one buffered stdin reader — each prompt consumes its own line ("1" then
// "2") and both servers resolve independently.
func TestAdoptMultiConflictSequentialStdin(t *testing.T) {
	home := driftIsolate(t)
	generic := driftGenericClient(home)
	cursor := driftBuiltinClient(t, clientconfig.ClientCursor)

	// Cursor precedes Generic in Detect() order, so variant [1] of each
	// conflict is cursor's config.
	plantDriftServer(t, cursor, "conflict-one", clientconfig.ServerConfig{Command: "node", Args: []string{"x.js"}, Type: "stdio"})
	plantDriftServer(t, generic, "conflict-one", clientconfig.ServerConfig{Command: "node", Args: []string{"y.js"}, Type: "stdio"})
	plantDriftServer(t, cursor, "conflict-two", clientconfig.ServerConfig{Command: "node", Args: []string{"p.js"}, Type: "stdio"})
	plantDriftServer(t, generic, "conflict-two", clientconfig.ServerConfig{Command: "node", Args: []string{"q.js"}, Type: "stdio"})

	old := adoptStdin
	adoptStdin = strings.NewReader("1\n2\n")
	t.Cleanup(func() { adoptStdin = old })

	report, code := adoptRun(t, adoptOptions{API: adoptUnresolvedRegistry(t)})

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if report.Conflicts != 2 || report.ConflictsSkipped != 0 || report.ConflictsResolved != 2 {
		t.Errorf("conflicts/resolved/skipped = %d/%d/%d, want 2/2/0", report.Conflicts, report.ConflictsResolved, report.ConflictsSkipped)
	}
	// conflict-one picked variant 1 (x.js); conflict-two picked variant 2 (q.js).
	if canon := adoptCanon(t, "conflict-one"); canon.Args == nil || canon.Args[0] != "x.js" {
		t.Errorf("conflict-one canonical args = %v, want [x.js]", canon.Args)
	}
	if canon := adoptCanon(t, "conflict-two"); canon.Args == nil || canon.Args[0] != "q.js" {
		t.Errorf("conflict-two canonical args = %v, want [q.js]", canon.Args)
	}
	for _, name := range []string{"conflict-one", "conflict-two"} {
		if row := adoptRow(t, report, name); row.Status != "conflict-resolved" {
			t.Errorf("%s status = %q, want conflict-resolved", name, row.Status)
		}
	}
}

// AdoptThenDoctorDiffClean: 2 clients, 1 shared server + 1 unique each →
// adopt → doctor --diff reports zero drift.
func TestAdoptThenDoctorDiffClean(t *testing.T) {
	home := driftIsolate(t)
	generic := driftGenericClient(home)
	cursor := driftBuiltinClient(t, clientconfig.ClientCursor)

	shared := clientconfig.ServerConfig{Command: "node", Args: []string{"server.js"}, Env: map[string]string{"SHARED_ENV": "x"}, Type: "stdio"}
	plantDriftServer(t, generic, "shared", shared)
	plantDriftServer(t, cursor, "shared", shared)
	plantDriftServer(t, generic, "only-generic", clientconfig.ServerConfig{Command: "node", Args: []string{"g.js"}, Type: "stdio"})
	plantDriftServer(t, cursor, "only-cursor", clientconfig.ServerConfig{Command: "node", Args: []string{"c.js"}, Type: "stdio"})

	report, code := adoptRun(t, adoptOptions{API: adoptUnresolvedRegistry(t)})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if report.Found != 3 || report.Conflicts != 0 {
		t.Fatalf("found/conflicts = %d/%d, want 3/0", report.Found, report.Conflicts)
	}

	if entry := adoptLock(t, "shared"); len(entry.Clients) != 2 {
		t.Errorf("shared clients = %v, want both", entry.Clients)
	}

	checks, note := runDriftChecks()
	if note != "" {
		t.Fatalf("drift note = %q, want none", note)
	}
	if len(checks) != 2 {
		t.Fatalf("expected 2 drift checks (generic + cursor), got %d: %+v", len(checks), checks)
	}
	for _, check := range checks {
		if check.Status != "ok" {
			t.Errorf("drift check %q = %s (%s) findings=%+v", check.Name, check.Status, check.Error, check.Findings)
		}
	}
}

// AdoptJSONReportShape: the report marshals with the documented keys and
// per-server row shape.
func TestAdoptJSONReportShape(t *testing.T) {
	t.Setenv("PHAROS_JSON", "1")
	home := driftIsolate(t)
	generic := driftGenericClient(home)
	plantDriftServer(t, generic, "srv", driftStdioCfg)

	report, _ := adoptRun(t, adoptOptions{API: adoptUnresolvedRegistry(t)})

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]any
	if err := json.Unmarshal(data, &shape); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"mode", "dry_run", "lockfile", "clients_scanned", "found", "adopted", "conflicts", "conflicts_resolved", "conflicts_skipped", "unresolved_in_registry", "servers", "next"} {
		if _, ok := shape[key]; !ok {
			t.Errorf("report JSON missing key %q", key)
		}
	}
	if shape["mode"] != "adopt" {
		t.Errorf("mode = %v, want adopt", shape["mode"])
	}
	rows, ok := shape["servers"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("servers = %v, want 1 row", shape["servers"])
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatal("server row is not an object")
	}
	for _, key := range []string{"name", "clients", "status"} {
		if _, ok := row[key]; !ok {
			t.Errorf("server row missing key %q", key)
		}
	}
	if row["status"] != "adopted" {
		t.Errorf("row status = %v, want adopted", row["status"])
	}
}

// AdoptExitCodeConflictSkipped: 0 on a clean adopt, 1 when a conflict is
// skipped.
func TestAdoptExitCodeConflictSkipped(t *testing.T) {
	t.Run("clean adopt exits 0", func(t *testing.T) {
		home := driftIsolate(t)
		generic := driftGenericClient(home)
		plantDriftServer(t, generic, "srv", driftStdioCfg)
		_, code := adoptRun(t, adoptOptions{API: adoptUnresolvedRegistry(t)})
		if code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	})

	t.Run("skipped conflict exits 1", func(t *testing.T) {
		t.Setenv("PHAROS_NON_INTERACTIVE", "1")
		home := driftIsolate(t)
		generic := driftGenericClient(home)
		cursor := driftBuiltinClient(t, clientconfig.ClientCursor)
		a := clientconfig.ServerConfig{Command: "node", Args: []string{"a.js"}, Type: "stdio"}
		b := clientconfig.ServerConfig{Command: "node", Args: []string{"b.js"}, Type: "stdio"}
		plantDriftServer(t, generic, "shared", a)
		plantDriftServer(t, cursor, "shared", b)
		_, code := adoptRun(t, adoptOptions{API: adoptUnresolvedRegistry(t)})
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
	})
}

// AdoptEnvNumericNoFalseConflict: JSON env PORT "8080" vs TOML env
// PORT 8080 (unquoted number) is NOT a conflict — driftLooseEqual
// semantics — and doctor --diff stays clean for both clients afterwards.
func TestAdoptEnvNumericNoFalseConflict(t *testing.T) {
	home := driftIsolate(t)
	generic := driftGenericClient(home)
	codex := driftBuiltinClient(t, clientconfig.ClientCodex)

	plantDriftServer(t, generic, "shared", clientconfig.ServerConfig{
		Command: "node",
		Args:    []string{"server.js"},
		Env:     map[string]string{"PORT": "8080"},
		Type:    "stdio",
	})

	// Hand-written Codex TOML with an unquoted numeric env value — the
	// shape TOML readers surface as float64.
	if err := os.MkdirAll(filepath.Dir(codex.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	tomlCfg := `model = "test"

[mcp_servers.shared]
command = "node"
args = ["server.js"]

[mcp_servers.shared.env]
PORT = 8080
`
	if err := os.WriteFile(codex.Path, []byte(tomlCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	report, code := adoptRun(t, adoptOptions{API: adoptUnresolvedRegistry(t)})

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if report.Conflicts != 0 {
		t.Errorf("conflicts = %d, want 0 (numeric env spelling is not a conflict)", report.Conflicts)
	}
	row := adoptRow(t, report, "shared")
	if row.Status != "adopted" || len(row.Clients) != 2 {
		t.Errorf("row = %+v, want adopted across both clients", row)
	}
	if entry := adoptLock(t, "shared"); len(entry.Clients) != 2 {
		t.Errorf("lockfile clients = %v, want both", entry.Clients)
	}
	if canon := adoptCanon(t, "shared"); canon.Env["PORT"] != "8080" {
		t.Errorf("canonical PORT = %q, want 8080", canon.Env["PORT"])
	}

	// doctor --diff must accept the numeric spelling on both sides.
	checks, note := runDriftChecks()
	if note != "" {
		t.Fatalf("drift note = %q, want none", note)
	}
	for _, check := range checks {
		if check.Status != "ok" {
			t.Errorf("drift check %q = %s (%s) findings=%+v", check.Name, check.Status, check.Error, check.Findings)
		}
	}
}

// ── command-level contract tests ────────────────────────────────────────────
//
// These run the real cobra command (`pharos import --adopt`) through the
// envcontract harness (runContract), so the env contract (PHAROS_JSON,
// PHAROS_ASSUME_YES), flag reset, and stdout capture are exercised exactly
// like the receipt contract tests. adoptPointCLIAtRegistry keeps every
// registry touch on localhost — the suite never contacts the network.

// adoptPointCLIAtRegistry points the CLI's config at a local stand-in
// registry (404s everything, like adoptUnresolvedRegistry) WITHOUT
// re-isolating HOME — it writes ~/.pharos/config.json into the CURRENT
// home, so it MUST be called after HOME is isolated (driftIsolate or
// adoptPlantsConflict). Mirrors pointCLIAtRegistry, minus the HOME swap.
func adoptPointCLIAtRegistry(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	dir := filepath.Join(contractHome(t), ".pharos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"registry":` + strconv.Quote(srv.URL) + `}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// adoptPlantsConflict plants the same server in two built-in clients with
// materially different configs (Cursor precedes Generic in Detect() order,
// so the first client's variant is cursor's).
func adoptPlantsConflict(t *testing.T) {
	t.Helper()
	home := driftIsolate(t)
	generic := driftGenericClient(home)
	cursor := driftBuiltinClient(t, clientconfig.ClientCursor)
	a := clientconfig.ServerConfig{Command: "node", Args: []string{"a.js"}, Env: map[string]string{"WHO": "generic"}, Type: "stdio"}
	b := clientconfig.ServerConfig{Command: "node", Args: []string{"b.js"}, Env: map[string]string{"WHO": "cursor"}, Type: "stdio"}
	plantDriftServer(t, generic, "shared", a)
	plantDriftServer(t, cursor, "shared", b)
}

// AdoptCommandJSONStdoutPurity: `pharos import --adopt` under PHAROS_JSON=1
// emits EXACTLY one JSON document on stdout — a single json.Unmarshal of
// the whole stdout (which fails on any trailing output) must succeed.
// Mirrors the receipt stdout-purity contract.
func TestAdoptCommandJSONStdoutPurity(t *testing.T) {
	home := driftIsolate(t)
	adoptPointCLIAtRegistry(t)
	generic := driftGenericClient(home)
	plantDriftServer(t, generic, "echo-server", driftStdioCfg)

	stdout, _ := runContract(t, map[string]string{"PHAROS_JSON": "1"}, "import", "--adopt")

	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		t.Fatal("stdout is empty — no adopt JSON emitted")
	}
	var report adoptReport
	if err := json.Unmarshal([]byte(trimmed), &report); err != nil {
		t.Fatalf("stdout is not exactly one adopt JSON document: %v\n%s", err, trimmed)
	}
	if report.Mode != "adopt" || report.Found != 1 || report.Adopted != 1 {
		t.Errorf("report = mode %q found %d adopted %d, want adopt 1/1", report.Mode, report.Found, report.Adopted)
	}
	if report.Next != "Run 'pharos doctor --diff' to verify clean state." {
		t.Errorf("next = %q, want the clean-state hint (JSON next is status-independent)", report.Next)
	}
}

// AdoptCommandAssumeYesEnv: PHAROS_ASSUME_YES=1 alone (no --yes flag, set
// through the real env contract) auto-resolves the conflict with the first
// detected client's config and exits 0 — proven here in human mode via the
// printed report plus the on-disk lockfile/canonical state.
func TestAdoptCommandAssumeYesEnv(t *testing.T) {
	adoptPlantsConflict(t)
	adoptPointCLIAtRegistry(t)

	stdout, _ := runContract(t, map[string]string{"PHAROS_ASSUME_YES": "1"}, "import", "--adopt")

	for _, want := range []string{
		"conflict auto-resolved: using cursor's config",
		"1 conflict(s) (1 resolved, 0 skipped)",
		"Adopt complete.",
		"clients keeping a different variant", // honest drift warning in the Next hint
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("human output %q does not contain %q", stdout, want)
		}
	}
	entry := adoptLock(t, "shared")
	if len(entry.Clients) != 2 {
		t.Errorf("lockfile clients = %v, want both (union despite auto-resolve)", entry.Clients)
	}
	if canon := adoptCanon(t, "shared"); canon.Args == nil || canon.Args[0] != "b.js" {
		t.Errorf("canonical args = %v, want the first client's config", canon.Args)
	}
}

// AdoptCommandJSONYes: --json + --yes together — conflicts auto-resolve,
// exit 0, and stdout is one valid JSON document with the resolution in it.
func TestAdoptCommandJSONYes(t *testing.T) {
	adoptPlantsConflict(t)
	adoptPointCLIAtRegistry(t)

	stdout, combined := runContract(t, map[string]string{"PHAROS_JSON": "1"}, "import", "--adopt", "--yes")

	var report adoptReport
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &report); err != nil {
		t.Fatalf("stdout is not exactly one adopt JSON document: %v\n%s", err, stdout)
	}
	if strings.Contains(combined, "Adopt failed") {
		t.Errorf("adopt must not fail: %q", combined)
	}
	if report.Conflicts != 1 || report.ConflictsResolved != 1 || report.ConflictsSkipped != 0 {
		t.Errorf("conflicts/resolved/skipped = %d/%d/%d, want 1/1/0", report.Conflicts, report.ConflictsResolved, report.ConflictsSkipped)
	}
	row := adoptRow(t, &report, "shared")
	if row.Status != "conflict-auto-resolved" || row.Conflict == nil || row.Conflict.Resolution != "auto-first" {
		t.Errorf("row = %+v, want conflict-auto-resolved/auto-first", row)
	}
	if canon := adoptCanon(t, "shared"); canon.Args == nil || canon.Args[0] != "b.js" {
		t.Errorf("canonical args = %v, want the first client's config", canon.Args)
	}
}
