package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/canonical"
	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/profiles"
)

// ── profile command test harness ────────────────────────────────────────
//
// Reuses the drift harness (driftIsolate: isolated HOME, temp cwd for
// pharos.lock, WSL2 mirrors disabled) and the contract harness
// (runContract drives the real cobra tree with captured stdout/stderr).
// profileStdin is swapped for prompt-driven cases and restored.

// plantProfileState writes a profiles.json directly through the state
// API so command tests start from a known map.
func plantProfileState(t *testing.T, mutate func(st *profiles.State)) {
	t.Helper()
	st, err := profiles.Load()
	if err != nil {
		t.Fatal(err)
	}
	mutate(st)
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
}

// readProfileState loads profiles.json for assertions.
func readProfileState(t *testing.T) *profiles.State {
	t.Helper()
	st, err := profiles.Load()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// withProfileStdin swaps the confirm-prompt reader; restore is automatic.
func withProfileStdin(t *testing.T, content string) {
	t.Helper()
	orig := profileStdin
	profileStdin = strings.NewReader(content)
	t.Cleanup(func() { profileStdin = orig })
}

// plantManagedServer gives a server the full installed footprint:
// client entry + canonical record + lockfile row — exactly what
// `pharos install` leaves behind.
func plantManagedServer(t *testing.T, c clientconfig.Client, name string) {
	t.Helper()
	plantDriftServer(t, c, name, driftStdioCfg)
	canon := map[string]canonical.Server{
		name: driftStdioCanonical(name, "node", []string{"server.js"}),
	}
	plantDriftCanonical(t, canon)
	plantDriftLockfile(t, map[string]lockfile.ServerEntry{
		name: driftLockEntry(),
	})
}

// ── Mapping: CreateWithClients ──────────────────────────────────────────

func TestProfileCreateWithClients(t *testing.T) {
	home := driftIsolate(t)

	stdout, combined := runContract(t, nil, "profile", "create", "work", "--client", "cursor,generic")
	if !strings.Contains(combined, "Created profile") {
		t.Fatalf("create output missing confirmation: %q", combined)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Fatal("empty stdout for human create")
	}

	st := readProfileState(t)
	p, ok := st.Profiles["work"]
	if !ok {
		t.Fatal("profile work not created")
	}
	if len(p.Clients) != 2 || p.Clients[0] != "cursor" || p.Clients[1] != "generic" {
		t.Errorf("clients = %v, want [cursor generic]", p.Clients)
	}
	if len(p.Inherits) != 0 {
		t.Errorf("inherits = %v, want empty (implicit base)", p.Inherits)
	}

	// --inherit records the explicit parent and requires it to exist.
	_, combinedInherit := runContract(t, nil, "profile", "create", "deep", "--inherit", "work")
	if !strings.Contains(combinedInherit, "Created profile") {
		t.Fatalf("inherit create failed: %q", combinedInherit)
	}
	st = readProfileState(t)
	if got := st.Profiles["deep"].Inherits; len(got) != 1 || got[0] != "work" {
		t.Errorf("deep inherits = %v, want [work]", got)
	}

	// Unknown client ID is a validation error (exit 2) before any state
	// change — driven through the logic helper (os.Exit safety).
	_, code, err := createProfile("bad", "", "nosuchclient")
	if code != 2 || err == nil {
		t.Errorf("createProfile(nosuchclient) = (%d, %v), want (2, error)", code, err)
	}
	st = readProfileState(t)
	if st.HasProfile("bad") {
		t.Error("failed create must not persist a profile")
	}

	// Duplicate name errors (exit 2).
	_, code, err = createProfile("work", "", "")
	if code != 2 || err == nil {
		t.Errorf("duplicate create = (%d, %v), want (2, error)", code, err)
	}

	_ = home
}

// ── Mapping: LsJSONShape ────────────────────────────────────────────────

func TestProfileLsJSONShape(t *testing.T) {
	home := driftIsolate(t)

	plantProfileState(t, func(st *profiles.State) {
		if err := st.AddServers(profiles.BaseProfile, "shared"); err != nil {
			t.Fatal(err)
		}
		if err := st.Create("work", "", []string{"cursor"}); err != nil {
			t.Fatal(err)
		}
		if err := st.AddServers("work", "github"); err != nil {
			t.Fatal(err)
		}
	})

	stdout, _ := runContract(t, map[string]string{"PHAROS_JSON": "1"}, "profile", "ls")
	trimmed := strings.TrimSpace(stdout)
	if !json.Valid([]byte(trimmed)) {
		t.Fatalf("profile ls --json stdout is not valid JSON: %.200q", trimmed)
	}
	var report profileLsReport
	if err := json.Unmarshal([]byte(trimmed), &report); err != nil {
		t.Fatalf("decode ls report: %v", err)
	}
	if report.Version != profiles.SchemaVersion {
		t.Errorf("version = %d, want %d", report.Version, profiles.SchemaVersion)
	}
	if report.State == "" {
		t.Error("state path missing from ls report")
	}
	work, ok := report.Profiles["work"]
	if !ok {
		t.Fatal("work missing from ls JSON")
	}
	if len(work.Clients) != 1 || work.Clients[0] != "cursor" {
		t.Errorf("work.clients = %v, want [cursor]", work.Clients)
	}
	if len(work.Servers) != 1 || work.Servers[0] != "github" {
		t.Errorf("work.servers = %v, want [github]", work.Servers)
	}
	// target_set is the resolved union: base servers first.
	want := []string{"shared", "github"}
	if len(work.TargetSet) != len(want) {
		t.Fatalf("work.target_set = %v, want %v", work.TargetSet, want)
	}
	for i := range want {
		if work.TargetSet[i] != want[i] {
			t.Errorf("work.target_set = %v, want %v", work.TargetSet, want)
		}
	}
	base, ok := report.Profiles[profiles.BaseProfile]
	if !ok {
		t.Fatal("base missing from ls JSON (implicit profile must be visible)")
	}
	if len(base.TargetSet) != 1 || base.TargetSet[0] != "shared" {
		t.Errorf("base.target_set = %v, want [shared]", base.TargetSet)
	}

	// The --json flag and PHAROS_JSON=1 agree (W1.1 equivalence).
	flagOut, _ := runContract(t, nil, "profile", "ls", "--json")
	if strings.TrimSpace(flagOut) != trimmed {
		t.Errorf("--json and PHAROS_JSON=1 disagree:\nflag: %q\nenv:  %q", flagOut, trimmed)
	}
	_ = home
}

// ── State mutation: add requires installed; remove detaches ────────────

func TestProfileAddRequiresInstalled(t *testing.T) {
	home := driftIsolate(t)
	c := driftGenericClient(home)
	plantManagedServer(t, c, "echo-server")
	plantProfileState(t, func(st *profiles.State) {
		if err := st.Create("work", "", []string{"generic"}); err != nil {
			t.Fatal(err)
		}
	})

	added, all, code, err := addProfileServers("work", []string{"echo-server"})
	if code != 0 || err != nil {
		t.Fatalf("addProfileServers = (%d, %v), want (0, nil)", code, err)
	}
	if len(added) != 1 || added[0] != "echo-server" {
		t.Errorf("added = %v, want [echo-server]", added)
	}
	if len(all) != 1 || all[0] != "echo-server" {
		t.Errorf("profile servers = %v, want [echo-server]", all)
	}

	// A server with no canonical config is rejected with exit 2 and
	// nothing is attached.
	_, _, code, err = addProfileServers("work", []string{"ghost"})
	if code != 2 || err == nil {
		t.Errorf("add ghost = (%d, %v), want (2, error)", code, err)
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Errorf("ghost error = %v, want 'not installed'", err)
	}
	st := readProfileState(t)
	if len(st.Profiles["work"].Servers) != 1 {
		t.Errorf("servers after failed add = %v, want unchanged [echo-server]", st.Profiles["work"].Servers)
	}

	// Re-adding is idempotent.
	_, all, code, err = addProfileServers("work", []string{"echo-server"})
	if code != 0 || err != nil || len(all) != 1 {
		t.Errorf("idempotent add = (%d, %v, %v), want (0, nil, 1 server)", code, err, all)
	}

	// remove detaches but the install footprint stays.
	removed, _, code, err := removeProfileServers("work", []string{"echo-server", "not-there"})
	if code != 0 || err != nil {
		t.Fatalf("removeProfileServers = (%d, %v)", code, err)
	}
	if len(removed) != 1 || removed[0] != "echo-server" {
		t.Errorf("removed = %v, want [echo-server] (not-there skipped, no error)", removed)
	}
	if _, err := canonical.GetServer("echo-server"); err != nil {
		t.Errorf("canonical entry must survive profile remove: %v", err)
	}
	lf, err := lockfile.Load("pharos.lock")
	if err != nil {
		t.Fatal(err)
	}
	if !lf.Has("echo-server") {
		t.Error("lockfile entry must survive profile remove (servers stay installed)")
	}
}

// ── rm: confirm + decline ───────────────────────────────────────────────

func TestProfileRmConfirmAndDecline(t *testing.T) {
	home := driftIsolate(t)
	plantProfileState(t, func(st *profiles.State) {
		if err := st.Create("work", "", nil); err != nil {
			t.Fatal(err)
		}
	})

	// Declined prompt keeps the profile and reports nothing-done (exit 1).
	withProfileStdin(t, "n\n")
	code, err := deleteProfile("work", false)
	if code != 1 || err != nil {
		t.Fatalf("declined rm = (%d, %v), want (1, nil)", code, err)
	}
	if !readProfileState(t).HasProfile("work") {
		t.Error("declined rm deleted the profile")
	}

	// "y" confirms.
	withProfileStdin(t, "y\n")
	code, err = deleteProfile("work", false)
	if code != 0 || err != nil {
		t.Fatalf("confirmed rm = (%d, %v), want (0, nil)", code, err)
	}
	if readProfileState(t).HasProfile("work") {
		t.Error("confirmed rm kept the profile")
	}

	// --yes skips the prompt entirely (stdin never read).
	plantProfileState(t, func(st *profiles.State) {
		_ = st.Create("work2", "", nil)
	})
	withProfileStdin(t, "") // EOF would fail any accidental read of a required answer
	code, err = deleteProfile("work2", true)
	if code != 0 || err != nil {
		t.Fatalf("--yes rm = (%d, %v), want (0, nil)", code, err)
	}
	if readProfileState(t).HasProfile("work2") {
		t.Error("--yes rm kept the profile")
	}

	// PHAROS_ASSUME_YES=1 is equivalent to --yes (W1.1 contract).
	t.Setenv("PHAROS_ASSUME_YES", "1")
	plantProfileState(t, func(st *profiles.State) {
		_ = st.Create("work3", "", nil)
	})
	code, err = deleteProfile("work3", false)
	if code != 0 || err != nil {
		t.Fatalf("ASSUME_YES rm = (%d, %v), want (0, nil)", code, err)
	}
	if readProfileState(t).HasProfile("work3") {
		t.Error("ASSUME_YES rm kept the profile")
	}

	// base is protected (exit 2).
	withProfileStdin(t, "y\n")
	code, err = deleteProfile(profiles.BaseProfile, false)
	if code != 2 || err == nil {
		t.Errorf("rm base = (%d, %v), want (2, error)", code, err)
	}
	_ = home
}

// ── rm: JSON decline emits a document, never a prompt ───────────────────

// TestProfileRmJSONDeclineEmitsDoc pins the W1.1 rm contract: JSON mode
// never prompts and never leaks prompt text — an unconfirmed rm emits the
// minimal {deleted: false, reason} document, exits 1, and keeps the
// profile; a confirmed one deletes and the wrapper's success doc carries
// the profile name.
func TestProfileRmJSONDeclineEmitsDoc(t *testing.T) {
	home := driftIsolate(t)
	plantProfileState(t, func(st *profiles.State) {
		if err := st.Create("work", "", nil); err != nil {
			t.Fatal(err)
		}
	})

	counter := &recordingStdin{}
	orig := profileStdin
	profileStdin = counter
	t.Cleanup(func() { profileStdin = orig })

	// JSON mode without --yes: decline (exit 1), one pure JSON doc on
	// stdout, zero stdin reads (the cobra wrapper os.Exits on code 1, so
	// the doc emission is driven at logic level — the suite's adopted
	// pattern for exit-1 paths).
	t.Setenv("PHAROS_JSON", "1")
	var code int
	var err error
	out := captureProfileStdout(t, func() {
		code, err = deleteProfile("work", false)
	})
	if err != nil || code != 1 {
		t.Fatalf("JSON rm without --yes = (%d, %v), want (1, nil)", code, err)
	}
	trimmed := strings.TrimSpace(out)
	if !json.Valid([]byte(trimmed)) {
		t.Fatalf("JSON decline stdout is not a single JSON document: %q", out)
	}
	if strings.Contains(trimmed, "[y/N]") || strings.Contains(trimmed, "Aborted") {
		t.Errorf("prompt/abort text leaked into JSON stdout: %q", out)
	}
	var doc profileRmDeclined
	if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Deleted {
		t.Error("decline doc must carry deleted:false")
	}
	if !strings.Contains(doc.Reason, "--yes") {
		t.Errorf("decline reason %q must name the fixing flag", doc.Reason)
	}
	if counter.reads != 0 {
		t.Errorf("JSON rm read stdin %d time(s); prompts are forbidden in JSON", counter.reads)
	}
	if !readProfileState(t).HasProfile("work") {
		t.Error("declined JSON rm deleted the profile")
	}

	// Confirmed JSON rm (exit 0 — safe for in-process cobra): the wrapper
	// emits the success receipt with the deleted profile's name.
	plantProfileState(t, func(st *profiles.State) {
		_ = st.Create("work2", "", nil)
	})
	stdout, _ := runContract(t, map[string]string{"PHAROS_JSON": "1"}, "profile", "rm", "work2", "--yes")
	var success struct {
		Deleted     string `json:"deleted"`
		ServersKept bool   `json:"servers_kept"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &success); err != nil {
		t.Fatalf("rm --yes --json stdout is not a single JSON doc: %v (%.200q)", err, stdout)
	}
	if success.Deleted != "work2" || !success.ServersKept {
		t.Errorf("rm --yes --json doc = %+v, want deleted:work2 servers_kept:true", success)
	}
	_ = home
}

// ── create: --inherit base is absorbed (base stays implicit) ────────────

// TestProfileCreateInheritBaseAbsorbed pins the L5 semantics: creating
// with parent "base" records nothing (base is always implicit), succeeds
// silently, and no display ever shows "base (implicit) + base".
func TestProfileCreateInheritBaseAbsorbed(t *testing.T) {
	home := driftIsolate(t)

	created, code, err := createProfile("personal", "base", "")
	if code != 0 || err != nil {
		t.Fatalf("create --inherit base = (%d, %v), want (0, nil)", code, err)
	}
	if len(created.Inherits) != 0 {
		t.Errorf("inherits = %v, want empty — base is implicit and never recorded", created.Inherits)
	}

	// A second create through the CLI also lands cleanly in state.
	if _, combined := runContract(t, nil, "profile", "create", "other", "--inherit", "base"); !strings.Contains(combined, "Created profile") {
		t.Fatalf("create --inherit base via CLI failed: %q", combined)
	}
	st := readProfileState(t)
	for _, name := range []string{"personal", "other"} {
		if got := st.Profiles[name].Inherits; len(got) != 0 {
			t.Errorf("%s inherits = %v, want empty", name, got)
		}
	}

	// ls (human) shows the single implicit label — never a doubled base.
	_, lsOut := runContract(t, nil, "profile", "ls")
	if strings.Contains(lsOut, "base (implicit) + base") {
		t.Errorf("ls doubles the implicit base: %q", lsOut)
	}
	_ = home
}

// ── run: daemon not running + unknown profile ───────────────────────────

func TestProfileRunNotRunning(t *testing.T) {
	home := driftIsolate(t)

	// Never spawn processes from tests.
	orig := profileRunAutoStart
	profileRunAutoStart = func() bool { return false }
	t.Cleanup(func() { profileRunAutoStart = orig })

	// Unknown profile is a validation error (exit 2).
	_, _, _, code, err := runProfileRun("ghost")
	if code != 2 || err == nil {
		t.Errorf("runProfileRun(ghost) = (%d, %v), want (2, error)", code, err)
	}

	// Daemon not running (fresh HOME has no daemon state) → exit 1 with
	// guidance, and the resolved target set still comes back.
	plantProfileState(t, func(st *profiles.State) {
		if err := st.Create("work", "", nil); err != nil {
			t.Fatal(err)
		}
		if err := st.AddServers("work", "github"); err != nil {
			t.Fatal(err)
		}
	})
	loaded, stopped, target, code, err := runProfileRun("work")
	if code != 1 || err == nil {
		t.Fatalf("runProfileRun(no daemon) = (%d, %v), want (1, error)", code, err)
	}
	if !strings.Contains(err.Error(), "daemon is not running") {
		t.Errorf("error = %v, want daemon-not-running guidance", err)
	}
	if len(target) != 1 || target[0] != "github" {
		t.Errorf("target = %v, want [github]", target)
	}
	if loaded != nil || stopped != nil {
		t.Errorf("loaded/stopped must be nil when nothing ran: %v/%v", loaded, stopped)
	}
	_ = home
}
