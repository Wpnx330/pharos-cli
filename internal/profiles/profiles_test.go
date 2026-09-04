package profiles

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// isolateState points HOME/USERPROFILE at a fresh temp dir so
// profiles.json resolves inside the test sandbox (windows CI safe).
func isolateState(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func statePath(t *testing.T) string {
	t.Helper()
	p, err := FilePath()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// ── CreateReadsBack ─────────────────────────────────────────────────────

func TestProfilesCreateReadsBack(t *testing.T) {
	isolateState(t)

	st, err := Load()
	if err != nil {
		t.Fatalf("Load fresh state: %v", err)
	}
	if !st.HasProfile(BaseProfile) {
		t.Fatal("fresh state must contain the implicit base profile")
	}

	if err := st.Create("work", "", []string{"cursor", " cursor ", "cursor"}); err != nil {
		t.Fatalf("Create work: %v", err)
	}
	if err := st.AddServers("work", "Context7", "github", "Context7"); err != nil {
		t.Fatalf("AddServers: %v", err)
	}
	if err := st.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Read it back through a fresh Load.
	st2, err := Load()
	if err != nil {
		t.Fatalf("Load saved state: %v", err)
	}
	p, ok := st2.Profiles["work"]
	if !ok {
		t.Fatal("profile work missing after reload")
	}
	if len(p.Clients) != 1 || p.Clients[0] != "cursor" {
		t.Errorf("clients = %v, want [cursor] (normalized: trimmed + deduped)", p.Clients)
	}
	if len(p.Servers) != 2 || p.Servers[0] != "Context7" || p.Servers[1] != "github" {
		t.Errorf("servers = %v, want [Context7 github] (sorted + deduped)", p.Servers)
	}
	if len(p.Inherits) != 0 {
		t.Errorf("inherits = %v, want empty (implicit base)", p.Inherits)
	}
}

// ── InheritChain ────────────────────────────────────────────────────────

func TestProfilesInheritChain(t *testing.T) {
	isolateState(t)
	st, _ := Load()

	// base has common servers; work inherits base implicitly plus an
	// explicit parent; the target set is deduped base-first.
	if err := st.AddServers(BaseProfile, "shared-echo"); err != nil {
		t.Fatal(err)
	}
	if err := st.Create("common", "base", nil); err != nil {
		t.Fatal(err)
	}
	// common created with explicit parent base — the parent is absorbed
	// (base is always implicit, never recorded), so the chain must never
	// duplicate it.
	if err := st.AddServers("common", "shared-echo", "tools"); err != nil {
		t.Fatal(err)
	}
	if err := st.Create("work", "common", []string{"cursor"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddServers("work", "github"); err != nil {
		t.Fatal(err)
	}

	chain, err := st.Chain("work")
	if err != nil {
		t.Fatalf("Chain(work): %v", err)
	}
	wantChain := []string{BaseProfile, "common", "work"}
	if len(chain) != len(wantChain) {
		t.Fatalf("chain = %v, want %v", chain, wantChain)
	}
	for i := range wantChain {
		if chain[i] != wantChain[i] {
			t.Fatalf("chain = %v, want %v (base-first order)", chain, wantChain)
		}
	}

	target, err := st.TargetSet("work")
	if err != nil {
		t.Fatalf("TargetSet(work): %v", err)
	}
	wantTarget := []string{"shared-echo", "tools", "github"}
	if len(target) != len(wantTarget) {
		t.Fatalf("target = %v, want %v", target, wantTarget)
	}
	for i := range wantTarget {
		if target[i] != wantTarget[i] {
			t.Errorf("target = %v, want %v (deduped, base servers first)", target, wantTarget)
		}
	}
}

// ── CycleRejected ───────────────────────────────────────────────────────

func TestProfilesCycleRejected(t *testing.T) {
	isolateState(t)

	// Self-inherit is rejected at create time.
	st, _ := Load()
	if err := st.Create("loop", "loop", nil); err == nil {
		t.Error("Create with self as parent must be rejected")
	} else if !strings.Contains(err.Error(), "cannot inherit itself") {
		t.Errorf("self-inherit error = %q, want 'cannot inherit itself'", err)
	}

	// A hand-edited state carrying a cycle is an error on Load (every
	// command surfaces corrupt state instead of looping forever).
	path := statePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte(`{"version":1,"profiles":{
		"base":{"inherits":[],"servers":[],"clients":[]},
		"a":{"inherits":["b"],"servers":[],"clients":[]},
		"b":{"inherits":["a"],"servers":[],"clients":[]}
	}}`)
	if err := os.WriteFile(path, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Error("Load must reject a cyclic inheritance graph")
	} else if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("cycle error = %q, want it to mention 'cycle'", err)
	}
}

// ── RmKeepsServers ──────────────────────────────────────────────────────

func TestProfilesRmKeepsServers(t *testing.T) {
	isolateState(t)
	st, _ := Load()

	if err := st.Create("work", "", []string{"cursor"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddServers("work", "github"); err != nil {
		t.Fatal(err)
	}
	if err := st.Create("work-child", "work", nil); err != nil {
		t.Fatal(err)
	}

	// Deleting a profile that others inherit is refused (would dangle
	// the child's Inherits reference).
	if err := st.Delete("work"); err == nil {
		t.Error("Delete of an inherited profile must be refused")
	} else if !strings.Contains(err.Error(), "inherited by") {
		t.Errorf("inherited-delete error = %q, want 'inherited by'", err)
	}

	// base is protected.
	if err := st.Delete(BaseProfile); err == nil {
		t.Error("Delete of base must be refused")
	}

	// Deleting a leaf removes only the profile entry; base and its
	// server records stay (profiles never own servers).
	if err := st.Delete("work-child"); err != nil {
		t.Fatalf("Delete leaf: %v", err)
	}
	if st.HasProfile("work-child") {
		t.Error("leaf profile still present after Delete")
	}
	if !st.HasProfile("work") {
		t.Error("unrelated profile must survive Delete")
	}
	p := st.Profiles["work"]
	if len(p.Servers) != 1 || p.Servers[0] != "github" {
		t.Errorf("work servers after sibling delete = %v, want [github] (servers stay)", p.Servers)
	}
	if _, err := os.Stat(statePath(t)); err == nil {
		// State file exists only after Save; Delete alone must not
		// have written anything yet (mutations save explicitly).
		t.Log("state file present pre-save (acceptable)")
	}
}

// ── CorruptStateErrors ──────────────────────────────────────────────────

func TestProfilesCorruptStateErrors(t *testing.T) {
	isolateState(t)
	path := statePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	// Garbage JSON errors with a parse message naming the file.
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Error("Load must fail on corrupt JSON")
	} else if !strings.Contains(err.Error(), "parse profiles state") {
		t.Errorf("corrupt JSON error = %q", err)
	}

	// A schema version from the future is refused, not silently read.
	future := []byte(`{"version":999,"profiles":{}}`)
	if err := os.WriteFile(path, future, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Error("Load must fail on a future schema version")
	} else if !strings.Contains(err.Error(), "newer pharos") {
		t.Errorf("future version error = %q", err)
	}

	// A dangling Inherits parent is invalid state.
	dangling := []byte(`{"version":1,"profiles":{
		"base":{"inherits":[],"servers":[],"clients":[]},
		"orphan":{"inherits":["ghost"],"servers":[],"clients":[]}
	}}`)
	if err := os.WriteFile(path, dangling, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Error("Load must fail on a dangling inherits reference")
	}
}

// ── ValidName ───────────────────────────────────────────────────────────

func TestProfilesValidName(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"work", true},
		{"Work_Context.2", true},
		{"a-b-c", true},
		{"", false},
		{"   ", false},
		{"has space", false},
		{"-lead", false},
		{"slash/ed", false},
		{"back\\slash", false},
		{strings.Repeat("x", 65), false},
		{strings.Repeat("x", 64), true},
	}
	for _, tt := range tests {
		err := ValidName(tt.name)
		if tt.valid && err != nil {
			t.Errorf("ValidName(%q) = %v, want valid", tt.name, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("ValidName(%q) = nil, want invalid", tt.name)
		}
	}
}

// ── state file shape ────────────────────────────────────────────────────

// TestProfilesFileShape pins the on-disk contract: version 1, profiles
// keyed by name, arrays never null (agents read this file directly).
func TestProfilesFileShape(t *testing.T) {
	isolateState(t)
	st, _ := Load()
	if err := st.Create("work", "base", []string{"cursor"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(statePath(t))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("profiles.json is not valid JSON: %v", err)
	}
	if raw["version"].(float64) != SchemaVersion {
		t.Errorf("version = %v, want %d", raw["version"], SchemaVersion)
	}
	profilesRaw, _ := raw["profiles"].(map[string]any)
	if profilesRaw == nil {
		t.Fatal("profiles key missing")
	}
	work, _ := profilesRaw["work"].(map[string]any)
	if work == nil {
		t.Fatal("work profile missing from file")
	}
	for _, key := range []string{"inherits", "servers", "clients"} {
		arr, ok := work[key].([]any)
		if !ok {
			t.Errorf("profiles.work.%s must be an array, got %T", key, work[key])
			continue
		}
		if arr == nil {
			t.Errorf("profiles.work.%s must never be null in the file", key)
		}
	}
}

// ── Save: atomic unique-temp swap ───────────────────────────────────────

// TestProfilesSaveConcurrentValidJSON pins the atomic-swap contract: rapid
// concurrent Saves never tear the file (readers only ever see one complete
// generation), no temp file survives, and the state stays loadable.
// There is deliberately no lock — last writer wins on the whole file.
func TestProfilesSaveConcurrentValidJSON(t *testing.T) {
	isolateState(t)

	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st, err := Load()
			if err != nil {
				errs <- err
				return
			}
			if err := st.AddServers(BaseProfile, fmt.Sprintf("srv-%d", i)); err != nil {
				errs <- err
				return
			}
			if err := st.Save(); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent save: %v", err)
	}

	// The file is always one complete generation: loadable and schema-valid.
	st, err := Load()
	if err != nil {
		t.Fatalf("Load after concurrent saves: %v", err)
	}
	if !st.HasProfile(BaseProfile) {
		t.Error("base profile missing after concurrent saves")
	}

	// No torn temp files may survive in the state dir.
	entries, err := os.ReadDir(filepath.Dir(statePath(t)))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file survived Save: %s", e.Name())
		}
	}
}

// ── Create: --inherit base is absorbed ──────────────────────────────────

// TestProfilesCreateWithBaseParentAbsorbed pins the state-level semantics:
// parent "base" records nothing (base is always implicitly inherited and
// is never stored in Inherits), the create succeeds, and the chain still
// resolves base-first.
func TestProfilesCreateWithBaseParentAbsorbed(t *testing.T) {
	isolateState(t)
	st, _ := Load()

	if err := st.Create("personal", BaseProfile, nil); err != nil {
		t.Fatalf("Create with base parent: %v", err)
	}
	if got := st.Profiles["personal"].Inherits; len(got) != 0 {
		t.Errorf("inherits = %v, want empty — base is implicit and never recorded", got)
	}

	chain, err := st.Chain("personal")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 2 || chain[0] != BaseProfile || chain[1] != "personal" {
		t.Errorf("chain = %v, want [base personal]", chain)
	}

	// An explicit non-base parent still records normally.
	if err := st.Create("child", "personal", nil); err != nil {
		t.Fatal(err)
	}
	if got := st.Profiles["child"].Inherits; len(got) != 1 || got[0] != "personal" {
		t.Errorf("child inherits = %v, want [personal]", got)
	}
}
