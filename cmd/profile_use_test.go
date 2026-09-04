package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/canonical"
	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/profiles"
)

// ── profile use test harness ────────────────────────────────────────────
//
// Standard scenario: isolated home with TWO clients (generic + cursor),
// a canonical+lockfile footprint for every referenced server, and a
// `work` profile mapped to the generic client. Every test asserts on
// real files — client configs, canonical, pharos.lock.

type profileUseFixture struct {
	home    string
	generic clientconfig.Client
	cursor  clientconfig.Client
}

// setupProfileUse builds the fixture. mapped selects which clients the
// `work` profile drives.
func setupProfileUse(t *testing.T, mapped []string) profileUseFixture {
	t.Helper()
	home := driftIsolate(t)
	f := profileUseFixture{
		home:    home,
		generic: driftGenericClient(home),
		cursor:  driftBuiltinClient(t, clientconfig.ClientCursor),
	}
	plantProfileState(t, func(st *profiles.State) {
		if err := st.Create("work", "", mapped); err != nil {
			t.Fatal(err)
		}
	})
	return f
}

// plantWorkServer gives a server the installed footprint (canonical +
// lockfile; the client entry is planted separately where the scenario
// needs it).
func plantWorkServer(t *testing.T, name string) {
	t.Helper()
	plantDriftCanonical(t, map[string]canonical.Server{
		name: driftStdioCanonical(name, "node", []string{"server.js"}),
	})
	plantDriftLockfile(t, map[string]lockfile.ServerEntry{
		name: driftLockEntry(),
	})
}

func clientEntry(t *testing.T, c clientconfig.Client, name string) (json.RawMessage, bool) {
	t.Helper()
	servers, err := clientconfig.ReadServersFormat(c.Path, c.Format)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := servers[name]
	return raw, ok
}

func lockClients(t *testing.T, name string) []string {
	t.Helper()
	lf, err := lockfile.Load("pharos.lock")
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := lf.Get(name)
	if !ok {
		return nil
	}
	return entry.Clients
}

// ── UseAddsMissing ──────────────────────────────────────────────────────

func TestProfileUseAddsMissing(t *testing.T) {
	f := setupProfileUse(t, []string{"generic"})
	plantWorkServer(t, "echo-server")
	plantProfileState(t, func(st *profiles.State) {
		_ = st.AddServers("work", "echo-server")
	})

	plan, code, err := runProfileUse("work", profileUseOptions{Yes: true})
	if err != nil || code != 0 {
		t.Fatalf("runProfileUse = (%d, %v), want (0, nil)", code, err)
	}
	if !plan.Applied || plan.Changes != 1 {
		t.Errorf("applied/changes = %v/%d, want true/1", plan.Applied, plan.Changes)
	}
	if !plan.LockfileUpdated {
		t.Error("lockfile must be updated when a managed server is added")
	}

	if _, ok := clientEntry(t, f.generic, "echo-server"); !ok {
		t.Error("echo-server missing from mapped generic client after apply")
	}
	// The written entry must match what install would write (canonical
	// re-derivation through install.BuildClientConfig).
	raw, _ := clientEntry(t, f.generic, "echo-server")
	var entry map[string]any
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatal(err)
	}
	if entry["command"] != "node" {
		t.Errorf("written command = %v, want node (from canonical record)", entry["command"])
	}

	// Lockfile Clients[] records the client that received the config.
	if got := lockClients(t, "echo-server"); len(got) != 1 || got[0] != "generic" {
		t.Errorf("lockfile clients = %v, want [generic]", got)
	}
}

// ── UseRemovesOutside (yes) ─────────────────────────────────────────────

func TestProfileUseRemovesOutside(t *testing.T) {
	f := setupProfileUse(t, []string{"generic"})
	plantWorkServer(t, "in-profile")
	// Both servers are present in the mapped client, as a previous
	// `profile use misc` (or manual install writes) would have left it.
	plantDriftServer(t, f.generic, "in-profile", driftStdioCfg)
	plantDriftServer(t, f.generic, "other-profiled", driftStdioCfg)
	plantProfileState(t, func(st *profiles.State) {
		_ = st.AddServers("work", "in-profile")
		_ = st.Create("misc", "", []string{"generic"})
		_ = st.AddServers("misc", "other-profiled")
	})
	// Put both in the lockfile as written to generic (as installs would).
	plantDriftLockfile(t, map[string]lockfile.ServerEntry{
		"in-profile":     driftLockEntry(),
		"other-profiled": driftLockEntry(),
	})

	_, code, err := runProfileUse("work", profileUseOptions{Yes: true})
	if err != nil || code != 0 {
		t.Fatalf("runProfileUse = (%d, %v)", code, err)
	}
	if _, ok := clientEntry(t, f.generic, "other-profiled"); ok {
		t.Error("other-profiled must be removed from the mapped client (not in target set)")
	}
	if _, ok := clientEntry(t, f.generic, "in-profile"); !ok {
		t.Error("in-profile must stay in the mapped client")
	}
	if got := lockClients(t, "other-profiled"); len(got) != 0 {
		t.Errorf("lockfile clients for removed server = %v, want empty", got)
	}
	// in-profile was already present (unchanged row) — reconcile only
	// touches the lockfile for actual add/remove actions.
	if got := lockClients(t, "in-profile"); len(got) != 0 {
		t.Errorf("lockfile clients for unchanged server = %v, want untouched (empty)", got)
	}
}

// ── UseDryRunWritesNothing ──────────────────────────────────────────────

func TestProfileUseDryRunWritesNothing(t *testing.T) {
	f := setupProfileUse(t, []string{"generic"})
	plantWorkServer(t, "echo-server")
	plantProfileState(t, func(st *profiles.State) {
		_ = st.AddServers("work", "echo-server")
	})
	beforeGeneric := plantSnapshot(t, f.generic.Path)

	plan, code, err := runProfileUse("work", profileUseOptions{DryRun: true})
	if err != nil || code != 0 {
		t.Fatalf("dry-run = (%d, %v), want (0, nil)", code, err)
	}
	if plan.Applied {
		t.Error("dry-run must report applied:false")
	}
	if plan.Changes != 1 {
		t.Errorf("dry-run changes = %d, want 1 (the missing add)", plan.Changes)
	}
	if after := plantSnapshot(t, f.generic.Path); !equalBytes(beforeGeneric, after) {
		t.Error("dry-run rewrote the client config")
	}
	lf, err := lockfile.Load("pharos.lock")
	if err != nil {
		t.Fatal(err)
	}
	if entry, ok := lf.Get("echo-server"); ok && len(entry.Clients) > 0 {
		t.Errorf("dry-run updated lockfile clients: %v", entry.Clients)
	}
}

// plantSnapshot reads a file's bytes (nil when absent).
func plantSnapshot(t *testing.T, path string) []byte {
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

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── UseDeclineNoChange ──────────────────────────────────────────────────

func TestProfileUseDeclineNoChange(t *testing.T) {
	f := setupProfileUse(t, []string{"generic"})
	plantWorkServer(t, "echo-server")
	plantProfileState(t, func(st *profiles.State) {
		_ = st.AddServers("work", "echo-server")
	})
	before := plantSnapshot(t, f.generic.Path)

	withProfileStdin(t, "n\n")
	plan, code, err := runProfileUse("work", profileUseOptions{})
	if err != nil {
		t.Fatalf("declined use error: %v", err)
	}
	if code != 1 {
		t.Errorf("declined use exit code = %d, want 1 (nothing done)", code)
	}
	if plan.Applied || plan.Blocked != "declined" {
		t.Errorf("plan = applied:%v blocked:%q, want false/declined", plan.Applied, plan.Blocked)
	}
	if after := plantSnapshot(t, f.generic.Path); !equalBytes(before, after) {
		t.Error("declined use rewrote the client config")
	}
}

// ── UseUnprofiledWarning (+ strict) ─────────────────────────────────────

func TestProfileUseUnprofiledWarning(t *testing.T) {
	f := setupProfileUse(t, []string{"generic"})
	plantWorkServer(t, "echo-server")
	// A server in the client that belongs to NO profile.
	plantDriftServer(t, f.generic, "stray", driftStdioCfg)
	plantProfileState(t, func(st *profiles.State) {
		_ = st.AddServers("work", "echo-server")
	})

	// Default (no --strict): the plan flags it as unprofiled and offers
	// the base hint. Inspect via dry-run (never prompts).
	plan, code, err := runProfileUse("work", profileUseOptions{DryRun: true})
	if err != nil || code != 0 {
		t.Fatalf("dry-run with unprofiled = (%d, %v)", code, err)
	}
	found := false
	for _, cp := range plan.ClientsPlan {
		for _, r := range cp.Remove {
			if r.Server == "stray" && strings.HasPrefix(r.Reason, "unprofiled") {
				found = true
			}
		}
	}
	if !found {
		t.Error("plan must label the stray server 'unprofiled'")
	}
	if !strings.Contains(plan.Hint, "pharos profile add base stray") {
		t.Errorf("hint = %q, want 'pharos profile add base stray ...'", plan.Hint)
	}
	if _, ok := clientEntry(t, f.generic, "stray"); !ok {
		t.Error("dry-run must not remove the stray server")
	}

	// Apply (yes): the unprofiled server is a removal candidate like any
	// other — it is removed from the mapped client.
	if _, code, err := runProfileUse("work", profileUseOptions{Yes: true}); code != 0 || err != nil {
		t.Fatalf("apply with unprofiled = (%d, %v)", code, err)
	}
	if _, ok := clientEntry(t, f.generic, "stray"); ok {
		t.Error("unprofiled server must be removed on apply")
	}

	// --strict: the unprofiled removal blocks the apply (exit 1, no writes).
	plantDriftServer(t, f.generic, "stray2", driftStdioCfg)
	before := plantSnapshot(t, f.generic.Path)
	plan, code, err = runProfileUse("work", profileUseOptions{Strict: true, Yes: true})
	if err != nil {
		t.Fatalf("strict error: %v", err)
	}
	if code != 1 {
		t.Errorf("strict exit code = %d, want 1", code)
	}
	if plan.Blocked != "strict-unprofiled" || plan.Applied {
		t.Errorf("strict plan = blocked:%q applied:%v, want strict-unprofiled/false", plan.Blocked, plan.Applied)
	}
	if after := plantSnapshot(t, f.generic.Path); !equalBytes(before, after) {
		t.Error("--strict must write nothing")
	}
}

// ── UseNonMappedUntouched ───────────────────────────────────────────────

func TestProfileUseNonMappedUntouched(t *testing.T) {
	f := setupProfileUse(t, []string{"generic"})
	plantWorkServer(t, "echo-server")
	// The cursor client holds a server that is NOT in the target set —
	// a mapped-client reconcile must never touch cursor.
	plantDriftServer(t, f.cursor, "cursor-only", driftStdioCfg)
	plantProfileState(t, func(st *profiles.State) {
		_ = st.AddServers("work", "echo-server")
	})
	before := plantSnapshot(t, f.cursor.Path)

	plan, code, err := runProfileUse("work", profileUseOptions{Yes: true})
	if err != nil || code != 0 {
		t.Fatalf("use = (%d, %v)", code, err)
	}
	if len(plan.ClientsPlan) != 1 {
		t.Fatalf("plan covers %d clients, want 1 (generic only)", len(plan.ClientsPlan))
	}
	if after := plantSnapshot(t, f.cursor.Path); !equalBytes(before, after) {
		t.Error("non-mapped client config was rewritten")
	}
	if _, ok := clientEntry(t, f.cursor, "cursor-only"); !ok {
		t.Error("non-mapped client lost its server")
	}
	if _, ok := clientEntry(t, f.cursor, "echo-server"); ok {
		t.Error("non-mapped client must NOT receive target-set servers")
	}
}

// ── UseUpdatesLockfileClients ───────────────────────────────────────────

func TestProfileUseUpdatesLockfileClients(t *testing.T) {
	f := setupProfileUse(t, []string{"generic", "cursor"})
	plantWorkServer(t, "echo-server")
	plantProfileState(t, func(st *profiles.State) {
		_ = st.AddServers("work", "echo-server")
	})
	// The lockfile says echo-server was previously written to cursor
	// only (a subset install); applying work adds generic.
	plantDriftLockfile(t, map[string]lockfile.ServerEntry{
		"echo-server": driftLockEntry(),
	})
	// Give cursor a stale copy so the reconcile also exercises removal.
	plantDriftServer(t, f.cursor, "stale", driftStdioCfg)

	if got := lockClients(t, "echo-server"); len(got) != 0 {
		t.Fatalf("pre-apply lockfile clients = %v, want empty", got)
	}

	plan, code, err := runProfileUse("work", profileUseOptions{Yes: true})
	if err != nil || code != 0 {
		t.Fatalf("use = (%d, %v)", code, err)
	}

	// Added to both mapped clients → Clients gains both, sorted.
	if got := lockClients(t, "echo-server"); len(got) != 2 || got[0] != "cursor" || got[1] != "generic" {
		t.Errorf("lockfile clients = %v, want [cursor generic]", got)
	}
	// Removed stale from cursor → its record drops cursor.
	if got := lockClients(t, "stale"); len(got) != 0 {
		t.Errorf("lockfile clients for stale = %v, want empty after removal", got)
	}
	if !plan.LockfileUpdated {
		t.Error("plan must report lockfile_updated")
	}
}

// ── UseInSyncNoChanges ──────────────────────────────────────────────────

func TestProfileUseInSyncNoChanges(t *testing.T) {
	f := setupProfileUse(t, []string{"generic"})
	plantWorkServer(t, "echo-server")
	plantProfileState(t, func(st *profiles.State) {
		_ = st.AddServers("work", "echo-server")
	})
	// First apply makes it in sync.
	if _, code, err := runProfileUse("work", profileUseOptions{Yes: true}); code != 0 || err != nil {
		t.Fatalf("first use = (%d, %v)", code, err)
	}

	before := plantSnapshot(t, f.generic.Path)
	plan, code, err := runProfileUse("work", profileUseOptions{})
	if err != nil || code != 0 {
		t.Fatalf("in-sync use = (%d, %v), want (0, nil) — reconcile with nothing to do succeeds", code, err)
	}
	if !plan.Applied || plan.Changes != 0 {
		t.Errorf("in-sync plan = applied:%v changes:%d, want true/0", plan.Applied, plan.Changes)
	}
	if after := plantSnapshot(t, f.generic.Path); !equalBytes(before, after) {
		t.Error("in-sync use rewrote the client config")
	}
}

// ── W1.1 contract: JSON plan, no prompts ────────────────────────────────

func TestProfileUseJSONPlanNoPrompt(t *testing.T) {
	f := setupProfileUse(t, []string{"generic"})
	plantWorkServer(t, "echo-server")
	plantDriftServer(t, f.generic, "stray", driftStdioCfg)
	plantProfileState(t, func(st *profiles.State) {
		_ = st.AddServers("work", "echo-server")
	})

	counter := &recordingStdin{}
	orig := profileStdin
	profileStdin = counter
	t.Cleanup(func() { profileStdin = orig })

	// JSON mode without --yes: the run returns the plan, applied:false,
	// exit 1, and never reads stdin. Driven through runProfileUse (the
	// cobra wrapper os.Exits on code 1; the adopt-test precedent applies).
	t.Setenv("PHAROS_JSON", "1")
	plan, code, err := runProfileUse("work", profileUseOptions{})
	if err != nil {
		t.Fatalf("JSON plan error: %v", err)
	}
	if code != 1 {
		t.Errorf("JSON plan exit code = %d, want 1 (pending changes, not applied)", code)
	}
	if plan.Applied {
		t.Error("JSON plan without --yes must report applied:false")
	}
	if plan.Profile != "work" || len(plan.ClientsPlan) != 1 {
		t.Errorf("plan identity wrong: %+v", plan)
	}
	if plan.Changes == 0 {
		t.Error("plan must carry the pending change count")
	}
	for _, cp := range plan.ClientsPlan {
		if len(cp.Add) != 1 || cp.Add[0].Server != "echo-server" {
			t.Errorf("plan add = %+v, want echo-server", cp.Add)
		}
		if len(cp.Remove) != 1 || cp.Remove[0].Server != "stray" {
			t.Errorf("plan remove = %+v, want stray", cp.Remove)
		}
	}
	if plan.Blocked != "needs-yes" {
		t.Errorf("blocked = %q, want needs-yes", plan.Blocked)
	}
	if counter.reads != 0 {
		t.Errorf("JSON mode read stdin %d time(s); prompts are forbidden in JSON", counter.reads)
	}

	// Nothing was written by the JSON plan run.
	if _, ok := clientEntry(t, f.generic, "echo-server"); ok {
		t.Error("JSON plan run must not apply")
	}

	// PHAROS_ASSUME_YES=1 with JSON applies without reading stdin.
	t.Setenv("PHAROS_ASSUME_YES", "1")
	plan, code, err = runProfileUse("work", profileUseOptions{})
	if err != nil || code != 0 {
		t.Fatalf("ASSUME_YES JSON run = (%d, %v), want (0, nil)", code, err)
	}
	if !plan.Applied {
		t.Error("ASSUME_YES JSON run must report applied:true")
	}
	if _, ok := clientEntry(t, f.generic, "echo-server"); !ok {
		t.Error("ASSUME_YES JSON run must apply the plan")
	}
	if counter.reads != 0 {
		t.Errorf("ASSUME_YES JSON run read stdin %d time(s)", counter.reads)
	}

	// Command-level: the applied JSON run (exit 0, safe for in-process
	// cobra) emits pure stdout JSON. Re-plant a missing server first.
	plantProfileState(t, func(st *profiles.State) {
		_, _ = st.RemoveServers("work", "echo-server")
	})
	stdout, _ := runContract(t, map[string]string{"PHAROS_JSON": "1", "PHAROS_ASSUME_YES": "1"}, "profile", "use", "work", "--yes")
	trimmed := strings.TrimSpace(stdout)
	if !json.Valid([]byte(trimmed)) {
		t.Fatalf("use --yes --json stdout is not valid JSON: %.200q", trimmed)
	}
	var emitted profileUsePlan
	if err := json.Unmarshal([]byte(trimmed), &emitted); err != nil {
		t.Fatal(err)
	}
	if !emitted.Applied || emitted.Changes == 0 {
		t.Errorf("emitted plan = applied:%v changes:%d, want true/>0", emitted.Applied, emitted.Changes)
	}
}

// ── apply failure accounting (M2: honest failed rows) ───────────────────

// captureProfileStdout runs fn with os.Stdout swapped for a pipe and
// returns what it wrote — the runContract capture pattern, for logic-level
// human output (fmt.Printf goes to os.Stdout directly).
func captureProfileStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	ch := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		ch <- b.String()
	}()
	fn()
	os.Stdout = orig
	_ = w.Close()
	out := <-ch
	_ = r.Close()
	return out
}

// sabotageResolvedClient points every resolved client instance matching id
// at a directory path — reading a directory as a config file fails on
// every GOOS, so the apply's MergeServer/RemoveServer error for real.
func sabotageResolvedClient(t *testing.T, plan *profileUsePlan, id clientconfig.ClientID, home string) {
	t.Helper()
	dir := filepath.Join(home, "sabotage-"+string(id))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range plan.resolved {
		if plan.resolved[i].ID == id {
			plan.resolved[i].Path = dir
		}
	}
}

// mustMarshalPlan serializes a plan through the real JSON contract.
func mustMarshalPlan(t *testing.T, plan *profileUsePlan) []byte {
	t.Helper()
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestProfileUseAllFailedAccounting pins the honesty contract: when every
// add/remove fails, failed rows keep their Error, Changes stays counted
// (never zeroed to a fake "already in sync"), applied:false, and the
// human output carries the explicit FAILED block.
func TestProfileUseAllFailedAccounting(t *testing.T) {
	f := setupProfileUse(t, []string{"generic"})
	plantWorkServer(t, "echo-server")
	plantDriftServer(t, f.generic, "stray", driftStdioCfg)
	plantProfileState(t, func(st *profiles.State) {
		_ = st.AddServers("work", "echo-server")
	})

	st := readProfileState(t)
	plan, err := computeProfileUsePlan(st, "work")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Changes != 2 {
		t.Fatalf("planned changes = %d, want 2 (add + remove)", plan.Changes)
	}
	sabotageResolvedClient(t, plan, f.generic.ID, f.home)

	applyProfileUsePlan(plan)

	if plan.Applied {
		t.Error("all-failed apply must report applied:false")
	}
	if plan.Failed != 2 {
		t.Errorf("failed = %d, want 2 (add + remove both failed)", plan.Failed)
	}
	if plan.Changes != 2 {
		t.Errorf("changes = %d, want 2 — failed rows must stay counted (never zeroed)", plan.Changes)
	}
	cp := plan.ClientsPlan[0]
	if len(cp.Add) != 1 || cp.Add[0].Error == "" {
		t.Errorf("failed add rows = %+v, want the echo-server row kept with its Error", cp.Add)
	}
	if len(cp.Remove) != 1 || cp.Remove[0].Error == "" {
		t.Errorf("failed remove rows = %+v, want the stray row kept with its Error", cp.Remove)
	}
	var doc struct {
		Failed int `json:"failed"`
	}
	if err := json.Unmarshal(mustMarshalPlan(t, plan), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Failed != 2 {
		t.Errorf("JSON doc failed = %d, want 2", doc.Failed)
	}

	out := captureProfileStdout(t, func() { emitProfileUseHuman(plan) })
	if !strings.Contains(out, "2 change(s) FAILED") {
		t.Errorf("human output missing the FAILED block: %q", out)
	}
	for _, want := range []string{"echo-server", "stray"} {
		if !strings.Contains(out, want) {
			t.Errorf("FAILED block must name %s: %q", want, out)
		}
	}
	if strings.Contains(out, "Already in sync") {
		t.Error("all-failed human output must never claim 'Already in sync.'")
	}
}

// TestProfileUsePartialFailureAccounting pins partial success: applied
// rows land, failed rows are listed and counted, applied:true, exit 0.
func TestProfileUsePartialFailureAccounting(t *testing.T) {
	f := setupProfileUse(t, []string{"generic", "cursor"})
	plantWorkServer(t, "echo-server")
	plantDriftServer(t, f.cursor, "cursor-only", driftStdioCfg)
	plantProfileState(t, func(st *profiles.State) {
		_ = st.AddServers("work", "echo-server")
	})

	st := readProfileState(t)
	plan, err := computeProfileUsePlan(st, "work")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Changes != 3 {
		t.Fatalf("planned changes = %d, want 3 (2 adds + 1 remove)", plan.Changes)
	}
	// Sabotage only the cursor client: generic applies, cursor fails.
	sabotageResolvedClient(t, plan, f.cursor.ID, f.home)

	applyProfileUsePlan(plan)

	if !plan.Applied {
		t.Error("partial success must report applied:true")
	}
	if plan.Failed != 2 {
		t.Errorf("failed = %d, want 2 (cursor add + remove)", plan.Failed)
	}
	if plan.Changes != 3 {
		t.Errorf("changes = %d, want 3 (1 applied + 2 failed)", plan.Changes)
	}
	if _, ok := clientEntry(t, f.generic, "echo-server"); !ok {
		t.Error("the healthy client's add must have applied")
	}
	if _, ok := clientEntry(t, f.cursor, "cursor-only"); !ok {
		t.Error("the sabotaged client must be untouched")
	}

	out := captureProfileStdout(t, func() { emitProfileUseHuman(plan) })
	if !strings.Contains(out, "Applied 1 of 3 change(s)") {
		t.Errorf("partial success line wrong: %q", out)
	}
	if !strings.Contains(out, "2 change(s) FAILED") || !strings.Contains(out, "cursor-only") {
		t.Errorf("partial success must list the failures: %q", out)
	}
	if strings.Contains(out, "Already in sync") {
		t.Error("partial failure must not read as 'Already in sync.'")
	}
}

// TestProfileUseAllFailedExitOne pins the exit code end-to-end: an apply
// where every row fails exits 1 (Changes keeps failed rows, applied stays
// false). Uses a read-only config dir as the real-world failure; platforms
// that do not enforce directory permissions skip (accounting is covered by
// TestProfileUseAllFailedAccounting on every GOOS).
func TestProfileUseAllFailedExitOne(t *testing.T) {
	f := setupProfileUse(t, []string{"generic"})
	plantWorkServer(t, "echo-server")
	plantDriftServer(t, f.generic, "stray", driftStdioCfg)
	plantProfileState(t, func(st *profiles.State) {
		_ = st.AddServers("work", "echo-server")
	})

	dir := filepath.Dir(f.generic.Path)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	plan, code, err := runProfileUse("work", profileUseOptions{Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Failed == 0 {
		t.Skip("platform does not enforce read-only config dirs; failure accounting covered at apply level")
	}
	if code != 1 {
		t.Errorf("all-failed exit code = %d, want 1", code)
	}
	if plan.Applied || plan.Changes != plan.Failed {
		t.Errorf("all-failed plan = applied:%v changes:%d failed:%d, want false/==failed",
			plan.Applied, plan.Changes, plan.Failed)
	}
}

// ── skipped rows (post `pharos remove` of a profile member) ─────────────

// TestProfileUseSkippedAfterRemove pins the no-silent-perfection note:
// after `pharos remove <member>` the profile still lists it, the client
// and canonical no longer have it → skipped row, changes 0, exit 0, and
// the human output says the target servers are missing from canonical.
func TestProfileUseSkippedAfterRemove(t *testing.T) {
	setupProfileUse(t, []string{"generic"})
	plantWorkServer(t, "echo-server")
	plantProfileState(t, func(st *profiles.State) {
		_ = st.AddServers("work", "echo-server")
	})

	// `pharos remove` clears canonical + lockfile + client entries; the
	// profile membership stays (profiles are orchestration-only).
	if _, combined := runContract(t, nil, "remove", "echo-server"); !strings.Contains(combined, "Removed") {
		t.Fatalf("remove echo-server failed: %q", combined)
	}

	plan, code, err := runProfileUse("work", profileUseOptions{Yes: true})
	if err != nil || code != 0 {
		t.Fatalf("use after remove = (%d, %v), want (0, nil)", code, err)
	}
	if plan.Skipped != 1 {
		t.Errorf("skipped = %d, want 1", plan.Skipped)
	}
	if plan.Changes != 0 {
		t.Errorf("changes = %d, want 0 (the only target server is skipped)", plan.Changes)
	}
	cp := plan.ClientsPlan[0]
	if len(cp.Skipped) != 1 || cp.Skipped[0].Server != "echo-server" {
		t.Errorf("skipped rows = %+v, want echo-server", cp.Skipped)
	}

	// Human output: skip row + the missing-from-canonical note.
	stdout, _ := runContract(t, nil, "profile", "use", "work")
	if !strings.Contains(stdout, "skip echo-server") {
		t.Errorf("human plan missing the skip row: %q", stdout)
	}
	if !strings.Contains(stdout, "missing from canonical") {
		t.Errorf("human plan missing the skipped note: %q", stdout)
	}
}

// ── W1.1 contract gaps: NON_INTERACTIVE, unknown profile, strict+JSON ───

// TestProfileUseNonInteractiveDecline: PHAROS_NON_INTERACTIVE=1 without
// --yes declines with guidance semantics (blocked needs-yes, exit 1) and
// never reads stdin.
func TestProfileUseNonInteractiveDecline(t *testing.T) {
	f := setupProfileUse(t, []string{"generic"})
	plantWorkServer(t, "echo-server")
	plantProfileState(t, func(st *profiles.State) {
		_ = st.AddServers("work", "echo-server")
	})

	counter := &recordingStdin{}
	orig := profileStdin
	profileStdin = counter
	t.Cleanup(func() { profileStdin = orig })

	t.Setenv("PHAROS_NON_INTERACTIVE", "1")
	plan, code, err := runProfileUse("work", profileUseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Errorf("NON_INTERACTIVE decline exit = %d, want 1 (nothing applied)", code)
	}
	if plan.Applied || plan.Blocked != "needs-yes" {
		t.Errorf("plan = applied:%v blocked:%q, want false/needs-yes", plan.Applied, plan.Blocked)
	}
	if counter.reads != 0 {
		t.Errorf("NON_INTERACTIVE read stdin %d time(s); prompts are forbidden", counter.reads)
	}
	if _, ok := clientEntry(t, f.generic, "echo-server"); ok {
		t.Error("NON_INTERACTIVE decline must not apply")
	}
}

// TestProfileUseUnknownProfileExitTwo: `profile use <unknown>` is a
// usage/validation error (exit 2), not a plan failure.
func TestProfileUseUnknownProfileExitTwo(t *testing.T) {
	driftIsolate(t)
	plan, code, err := runProfileUse("ghost", profileUseOptions{})
	if plan != nil || code != 2 || err == nil {
		t.Fatalf("unknown profile = (%v, %d, %v), want (nil, 2, error)", plan, code, err)
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %v, want it to name the missing profile", err)
	}
}

// TestProfileUseStrictJSONBlockedNoPrompt: --strict under JSON blocks with
// the strict-unprofiled doc (exit 1) and never prompts.
func TestProfileUseStrictJSONBlockedNoPrompt(t *testing.T) {
	f := setupProfileUse(t, []string{"generic"})
	plantWorkServer(t, "echo-server")
	plantDriftServer(t, f.generic, "stray", driftStdioCfg)
	plantProfileState(t, func(st *profiles.State) {
		_ = st.AddServers("work", "echo-server")
	})

	counter := &recordingStdin{}
	orig := profileStdin
	profileStdin = counter
	t.Cleanup(func() { profileStdin = orig })

	t.Setenv("PHAROS_JSON", "1")
	plan, code, err := runProfileUse("work", profileUseOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Errorf("strict JSON exit = %d, want 1 (blocked)", code)
	}
	if plan.Blocked != "strict-unprofiled" || plan.Applied {
		t.Errorf("plan = blocked:%q applied:%v, want strict-unprofiled/false", plan.Blocked, plan.Applied)
	}
	if !plan.Strict {
		t.Error("plan must carry strict:true")
	}
	if counter.reads != 0 {
		t.Errorf("strict JSON read stdin %d time(s); prompts are forbidden", counter.reads)
	}
	if _, ok := clientEntry(t, f.generic, "stray"); !ok {
		t.Error("strict blocked run must write nothing")
	}
}

// TestProfileUseStrictDryRunPurePreview pins the L2 semantics: --dry-run
// is always a pure preview (exit 0) — --strict never gates it. The plan
// still shows the unprofiled removal candidates and the base hint.
func TestProfileUseStrictDryRunPurePreview(t *testing.T) {
	f := setupProfileUse(t, []string{"generic"})
	plantWorkServer(t, "echo-server")
	plantDriftServer(t, f.generic, "stray", driftStdioCfg)
	plantProfileState(t, func(st *profiles.State) {
		_ = st.AddServers("work", "echo-server")
	})
	before := plantSnapshot(t, f.generic.Path)

	plan, code, err := runProfileUse("work", profileUseOptions{Strict: true, DryRun: true})
	if err != nil || code != 0 {
		t.Fatalf("strict dry-run = (%d, %v), want (0, nil) — dry-run is a pure preview", code, err)
	}
	if plan.Blocked != "" || plan.Applied || !plan.DryRun || !plan.Strict {
		t.Errorf("strict dry-run plan = blocked:%q applied:%v dry_run:%v strict:%v, want \"\"/false/true/true",
			plan.Blocked, plan.Applied, plan.DryRun, plan.Strict)
	}
	// The preview still carries the unprofiled classification + hint.
	found := false
	for _, cp := range plan.ClientsPlan {
		for _, r := range cp.Remove {
			if r.Server == "stray" && strings.HasPrefix(r.Reason, "unprofiled") {
				found = true
			}
		}
	}
	if !found {
		t.Error("strict dry-run plan must still label the unprofiled removal candidate")
	}
	if !strings.Contains(plan.Hint, "pharos profile add base stray") {
		t.Errorf("hint = %q, want the base hint", plan.Hint)
	}
	if after := plantSnapshot(t, f.generic.Path); !equalBytes(before, after) {
		t.Error("strict dry-run wrote nothing")
	}
}

// ── W1.1 contract: stdout purity (command-level doc) ────────────────────

// TestProfileStdoutPurity pins the W1.1 stdout contract for every profile
// subcommand that completes in-process under PHAROS_JSON=1: stdout is a
// single valid JSON document — no progress lines, no prompt text, and no
// stdin reads. The rows are ordered so each stays at exit 0 (the only
// cobra outcome that can run in-process). `profile run` is not in the
// table: without a daemon it exits 1 via os.Exit, so its JSON doc is
// pinned at logic level in profile_run's tests.
func TestProfileStdoutPurity(t *testing.T) {
	setupProfileUse(t, []string{"generic"})
	plantWorkServer(t, "echo-server")
	plantProfileState(t, func(st *profiles.State) {
		_ = st.AddServers("work", "echo-server")
		_ = st.Create("work2", "", nil)
	})

	// ls; create (fresh name); use --yes (applies the pending add); add
	// (idempotent re-attach); remove; rm --yes (deletes work2).
	rows := [][]string{
		{"profile", "ls"},
		{"profile", "create", "fresh", "--client", "generic"},
		{"profile", "use", "work", "--yes"},
		{"profile", "add", "work", "echo-server"},
		{"profile", "remove", "work", "echo-server"},
		{"profile", "rm", "work2", "--yes"},
	}

	counter := &recordingStdin{}
	orig := profileStdin
	profileStdin = counter
	t.Cleanup(func() { profileStdin = orig })

	for _, args := range rows {
		stdout, _ := runContract(t, map[string]string{"PHAROS_JSON": "1"}, args...)
		trimmed := strings.TrimSpace(stdout)
		if !json.Valid([]byte(trimmed)) {
			t.Errorf("pharos %v stdout under PHAROS_JSON=1 is not a pure JSON document: %.200q", args, trimmed)
		}
		if strings.Contains(trimmed, "Apply?") {
			t.Errorf("pharos %v leaked the interactive prompt into JSON stdout", args)
		}
	}
	if counter.reads != 0 {
		t.Errorf("profile commands read stdin %d time(s) in JSON mode", counter.reads)
	}
}

// ── install --profile ───────────────────────────────────────────────────

// profileRegistryStub serves one kind-1 (remote HTTP) package offline
// and points ~/.pharos/config.json at it, so the full
// `pharos install <name> --profile work` flow runs in-process.
func profileRegistryStub(t *testing.T, name string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// install-event telemetry — accept and ignore.
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "{}")
			return
		}
		if body, ok := map[string]string{
			name: fmt.Sprintf(`{
				"name": %[1]q,
				"dist_tags": {"latest": "1.0.0"},
				"versions": [
					{
						"version": "1.0.0",
						"manifest": {"name": %[1]q, "version": "1.0.0", "transport": "http", "endpoint": "https://example.test/mcp"}
					}
				]
			}`, name),
		}[strings.TrimPrefix(r.URL.Path, "/v1/packages/")]; ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	cfgPath := filepath.Join(pharosHomeDir(t), ".pharos", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf(`{"registry": %q}`, srv.URL)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pharosHomeDir returns the isolated home (driftIsolate planted it via
// env; os.UserHomeDir resolves it).
func pharosHomeDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return home
}

func TestProfileInstallWithProfile(t *testing.T) {
	f := setupProfileUse(t, []string{"cursor"})
	// The cursor client must be detectable: its app dir exists.
	if err := os.MkdirAll(filepath.Dir(f.cursor.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	profileRegistryStub(t, "remote-server")
	plantProfileState(t, func(st *profiles.State) {
		// setupProfileUse created work → [cursor]; nothing else needed.
	})

	stdout, _ := runContract(t, map[string]string{"PHAROS_JSON": "1"}, "install", "remote-server", "--profile", "work")
	trimmed := strings.TrimSpace(stdout)
	if !json.Valid([]byte(trimmed)) {
		t.Fatalf("install --profile JSON stdout is not a pure receipt: %.200q", trimmed)
	}

	// The server is attached to the profile.
	st := readProfileState(t)
	got := st.Profiles["work"].Servers
	if len(got) != 1 || got[0] != "remote-server" {
		t.Errorf("profile servers after install = %v, want [remote-server]", got)
	}

	// Written to the profile's mapped client only.
	if _, ok := clientEntry(t, f.cursor, "remote-server"); !ok {
		t.Error("remote-server missing from the profile's mapped cursor client")
	}
	if _, ok := clientEntry(t, f.generic, "remote-server"); ok {
		t.Error("unmapped generic client must not receive the install")
	}

	// Lockfile records the cursor write.
	if got := lockClients(t, "remote-server"); len(got) != 1 || got[0] != "cursor" {
		t.Errorf("lockfile clients = %v, want [cursor]", got)
	}

	// Conflicts and validation errors (exit 2), driven through the
	// flag-resolver logic (the cobra layer os.Exits on code 2).
	origProfile, origClient, origFrozen := installProfile, installClient, installFrozen
	t.Cleanup(func() { installProfile, installClient, installFrozen = origProfile, origClient, origFrozen })
	installProfile, installClient, installFrozen = "work", "cursor", false
	if _, err := validateInstallProfile(); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Errorf("--client + --profile = %v, want combination error", err)
	}
	installClient, installFrozen = "", true
	if _, err := validateInstallProfile(); err == nil || !strings.Contains(err.Error(), "--frozen") {
		t.Errorf("--frozen + --profile = %v, want frozen error", err)
	}
	installProfile, installFrozen = "ghost", false
	if _, err := validateInstallProfile(); err == nil {
		t.Error("unknown profile must error")
	}
	installProfile, installFrozen = "work", false
	ids, err := validateInstallProfile()
	if err != nil || len(ids) != 1 || ids[0] != "cursor" {
		t.Errorf("valid --profile = (%v, %v), want [cursor]/nil", ids, err)
	}
}

// ── install --profile: dependency attach vs --no-dep-config ─────────────

// profileDepsRegistryStub serves two kind-1 (remote HTTP) packages
// offline — a primary that declares one dependency, plus the dependency —
// and points ~/.pharos/config.json at the stand-in.
func profileDepsRegistryStub(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "{}")
			return
		}
		remote := `"transport": "http", "endpoint": "https://example.test/mcp"`
		if body, ok := map[string]string{
			"with-deps": fmt.Sprintf(`{
				"name": "with-deps",
				"dist_tags": {"latest": "1.0.0"},
				"versions": [
					{
						"version": "1.0.0",
						"manifest": {"name": "with-deps", "version": "1.0.0", %[1]s,
							"dependencies": [{"name": "dep-server", "version": "1.0.0"}]}
					}
				]
			}`, remote),
			"dep-server": fmt.Sprintf(`{
				"name": "dep-server",
				"dist_tags": {"latest": "1.0.0"},
				"versions": [
					{"version": "1.0.0", "manifest": {"name": "dep-server", "version": "1.0.0", %[1]s}}
				]
			}`, remote),
		}[strings.TrimPrefix(r.URL.Path, "/v1/packages/")]; ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	cfgPath := filepath.Join(pharosHomeDir(t), ".pharos", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf(`{"registry": %q}`, srv.URL)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestProfileInstallNoDepConfigSkipsDepAttach pins the asymmetry fix:
// --profile --no-dep-config must skip the dependency's profile attach
// exactly like it skips the already-installed branch — the dep stays out
// of the profile membership.
func TestProfileInstallNoDepConfigSkipsDepAttach(t *testing.T) {
	f := setupProfileUse(t, []string{"cursor"})
	if err := os.MkdirAll(filepath.Dir(f.cursor.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	profileDepsRegistryStub(t)

	stdout, _ := runContract(t, map[string]string{"PHAROS_JSON": "1"},
		"install", "with-deps", "--profile", "work", "--no-dep-config")
	if !json.Valid([]byte(strings.TrimSpace(stdout))) {
		t.Fatalf("install --no-dep-config --profile JSON stdout not valid: %.200q", stdout)
	}

	st := readProfileState(t)
	got := st.Profiles["work"].Servers
	if len(got) != 1 || got[0] != "with-deps" {
		t.Errorf("profile servers after install = %v, want [with-deps] only — --no-dep-config must not attach the dependency", got)
	}

	// The primary itself is still attached and written to the mapped client.
	if _, ok := clientEntry(t, f.cursor, "with-deps"); !ok {
		t.Error("primary server missing from the profile's mapped client")
	}
}

// TestProfileInstallWithProfileAttachesDeps is the positive control: with
// dep configs written (no --no-dep-config), the dependency attaches to the
// profile too — otherwise a later 'profile use' would remove it.
func TestProfileInstallWithProfileAttachesDeps(t *testing.T) {
	f := setupProfileUse(t, []string{"cursor"})
	if err := os.MkdirAll(filepath.Dir(f.cursor.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	profileDepsRegistryStub(t)

	stdout, _ := runContract(t, map[string]string{"PHAROS_JSON": "1"},
		"install", "with-deps", "--profile", "work")
	if !json.Valid([]byte(strings.TrimSpace(stdout))) {
		t.Fatalf("install --profile JSON stdout not valid: %.200q", stdout)
	}

	st := readProfileState(t)
	got := st.Profiles["work"].Servers
	if len(got) != 2 || got[0] != "dep-server" || got[1] != "with-deps" {
		t.Fatalf("profile servers after install = %v, want [dep-server with-deps]", got)
	}
	if _, ok := clientEntry(t, f.cursor, "dep-server"); !ok {
		t.Error("dependency missing from the profile's mapped client")
	}
}
