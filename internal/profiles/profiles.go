// Package profiles manages ~/.pharos/profiles.json — the state file for
// Pharos context profiles (SPEC A1). A profile is a named context that
// groups installed servers and maps them to MCP clients. Profiles are an
// orchestration layer over installs: pharos.lock and the canonical
// ~/.pharos/mcp.json remain the only sources of truth for what is
// installed; profiles only record which servers belong to a context and
// which clients that context drives (`pharos profile use` reconciles
// exactly those clients).
//
// Concurrency: Save is atomic per write (unique temp file + rename, so
// readers only ever see one complete generation of the file) but there
// is deliberately no cross-process lock — concurrent pharos processes
// are last-writer-wins on the whole file.
package profiles

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// SchemaVersion is the profiles.json schema version.
const SchemaVersion = 1

// BaseProfile is the implicit profile every profile inherits (SPEC A1:
// "a base profile every profile inherits, so common servers aren't
// duplicated"). It always exists and can never be created or deleted.
const BaseProfile = "base"

// maxNameLen bounds profile names.
const maxNameLen = 64

// Profile is one named context.
type Profile struct {
	// Inherits holds the explicit parent name (0 or 1 entries —
	// single-parent chains by design). base is always implicitly
	// inherited and is never recorded here.
	Inherits []string `json:"inherits"`
	// Servers are the profile's own server names, kept sorted and deduped.
	Servers []string `json:"servers"`
	// Clients are the MCP client IDs this profile drives. `profile use`
	// reconciles exactly these clients to the profile's target set.
	Clients []string `json:"clients"`
}

// State is the top-level profiles.json structure.
type State struct {
	Version  int                `json:"version"`
	Profiles map[string]Profile `json:"profiles"`
}

// nameRe constrains profile names to shell-friendly, path-safe tokens.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// ValidName reports whether name is acceptable for a profile: non-empty,
// <=64 chars, matching [A-Za-z0-9][A-Za-z0-9_.-]*.
func ValidName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("profile name is required")
	}
	if len(name) > maxNameLen {
		return fmt.Errorf("profile name %q is longer than %d characters", name, maxNameLen)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid profile name %q (use letters, digits, '.', '_' or '-')", name)
	}
	return nil
}

// FilePath returns the absolute path to ~/.pharos/profiles.json.
func FilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, ".pharos", "profiles.json"), nil
}

// Load reads profiles.json. A missing file yields a fresh empty state
// with the implicit base profile ensured. Corrupt JSON, a future schema
// version, or structurally invalid state (inheritance cycle, dangling
// parent, more than one parent) is an error so every command surfaces
// corrupt state instead of guessing.
func Load() (*State, error) {
	path, err := FilePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			st := &State{Version: SchemaVersion, Profiles: make(map[string]Profile)}
			st.EnsureBase()
			return st, nil
		}
		return nil, fmt.Errorf("read profiles state: %w", err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse profiles state %s: %w", path, err)
	}
	if st.Profiles == nil {
		st.Profiles = make(map[string]Profile)
	}
	if st.Version == 0 {
		st.Version = SchemaVersion
	}
	if st.Version > SchemaVersion {
		return nil, fmt.Errorf("profiles state %s was written by a newer pharos (schema %d > %d)", path, st.Version, SchemaVersion)
	}
	st.EnsureBase()
	if err := st.Validate(); err != nil {
		return nil, fmt.Errorf("invalid profiles state %s: %w", path, err)
	}
	return &st, nil
}

// serializeSave returns an unlock func that serializes Save calls for the
// same resolved path within this process. Keyed per path so distinct test
// sandboxes (different HOMEs) never block each other.
var (
	saveMu    sync.Mutex
	saveLocks = map[string]*sync.Mutex{}
)

func serializeSave(path string) func() {
	saveMu.Lock()
	m, ok := saveLocks[path]
	if !ok {
		m = &sync.Mutex{}
		saveLocks[path] = m
	}
	saveMu.Unlock()
	m.Lock()
	return m.Unlock
}

// Save writes profiles.json, creating ~/.pharos as needed.
//
// Concurrency: saves for the same path are serialized in-process (the
// temp+rename swap below is not safely concurrent on Windows, where
// rename-over-open-file is exclusive). Cross-process writers remain
// last-writer-wins on the whole file — there is deliberately no lock file.
func (s *State) Save() error {
	path, err := FilePath()
	if err != nil {
		return err
	}
	unlock := serializeSave(path)
	defer unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create pharos dir: %w", err)
	}
	if s.Profiles == nil {
		s.Profiles = make(map[string]Profile)
	}
	s.Version = SchemaVersion
	s.EnsureBase()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal profiles state: %w", err)
	}
	data = append(data, '\n')
	// Atomic swap: a unique temp file next to the target, then rename.
	// A per-writer temp name is required — the earlier fixed
	// ".pharos-tmp" name could collide between concurrent pharos
	// processes and tear both writes. There is no lock: last writer wins.
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp profiles state: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename has moved it
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("perm temp profiles state: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp profiles state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write temp profiles state: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("swap profiles state: %w", err)
	}
	return nil
}

// EnsureBase guarantees the implicit base profile exists. base is never
// stored with an Inherits entry: every profile inherits it implicitly.
func (s *State) EnsureBase() {
	if s.Profiles == nil {
		s.Profiles = make(map[string]Profile)
	}
	if _, ok := s.Profiles[BaseProfile]; !ok {
		s.Profiles[BaseProfile] = Profile{
			Inherits: []string{},
			Servers:  []string{},
			Clients:  []string{},
		}
	}
}

// HasProfile reports whether the named profile exists.
func (s *State) HasProfile(name string) bool {
	_, ok := s.Profiles[name]
	return ok
}

// Create adds a new profile. parent may be "" (base-only implicit
// inheritance); a non-empty parent must exist and must not start a cycle
// through name. parent "base" is absorbed — base is always implicitly
// inherited, so it is never recorded in Inherits (the field's contract)
// and the create succeeds silently. clients are normalized (trimmed,
// deduped, sorted).
func (s *State) Create(name, parent string, clients []string) error {
	if err := ValidName(name); err != nil {
		return err
	}
	if _, exists := s.Profiles[name]; exists {
		return fmt.Errorf("profile %q already exists", name)
	}
	if parent != "" {
		if parent == name {
			return fmt.Errorf("profile %q cannot inherit itself", name)
		}
		if _, ok := s.Profiles[parent]; !ok {
			return fmt.Errorf("parent profile %q does not exist", parent)
		}
		if _, err := s.Chain(parent); err != nil {
			return fmt.Errorf("parent chain invalid: %w", err)
		}
	}
	p := Profile{
		Inherits: []string{},
		Servers:  []string{},
		Clients:  normalizeNames(clients),
	}
	if parent != "" && parent != BaseProfile {
		p.Inherits = []string{parent}
	}
	if s.Profiles == nil {
		s.Profiles = make(map[string]Profile)
	}
	s.Profiles[name] = p
	return nil
}

// Delete removes a profile. base is protected. A profile that other
// profiles inherit is also protected — deleting it would dangle the
// child's Inherits reference; the child must be re-parented or removed
// first. The profile's servers stay installed (profiles never own files).
func (s *State) Delete(name string) error {
	if name == BaseProfile {
		return fmt.Errorf("the %q profile cannot be deleted", BaseProfile)
	}
	if _, ok := s.Profiles[name]; !ok {
		return fmt.Errorf("profile %q does not exist", name)
	}
	var children []string
	for other, p := range s.Profiles {
		for _, parent := range p.Inherits {
			if parent == name {
				children = append(children, other)
			}
		}
	}
	if len(children) > 0 {
		sort.Strings(children)
		return fmt.Errorf("profile %q is inherited by %s — re-parent or delete them first",
			name, strings.Join(children, ", "))
	}
	delete(s.Profiles, name)
	return nil
}

// AddServers attaches server names to a profile (idempotent, sorted).
// Servers already in the profile are silently kept once.
func (s *State) AddServers(name string, servers ...string) error {
	p, ok := s.Profiles[name]
	if !ok {
		return fmt.Errorf("profile %q does not exist", name)
	}
	seen := make(map[string]bool, len(p.Servers))
	for _, srv := range p.Servers {
		seen[srv] = true
	}
	for _, srv := range servers {
		srv = strings.TrimSpace(srv)
		if srv == "" || seen[srv] {
			continue
		}
		seen[srv] = true
		p.Servers = append(p.Servers, srv)
	}
	sort.Strings(p.Servers)
	s.Profiles[name] = p
	return nil
}

// RemoveServers detaches server names from a profile. Returns the names
// actually removed (sorted); names not present are skipped without error.
func (s *State) RemoveServers(name string, servers ...string) ([]string, error) {
	p, ok := s.Profiles[name]
	if !ok {
		return nil, fmt.Errorf("profile %q does not exist", name)
	}
	drop := make(map[string]bool, len(servers))
	for _, srv := range servers {
		drop[strings.TrimSpace(srv)] = true
	}
	kept := make([]string, 0, len(p.Servers))
	var removed []string
	for _, srv := range p.Servers {
		if drop[srv] {
			removed = append(removed, srv)
			continue
		}
		kept = append(kept, srv)
	}
	sort.Strings(kept)
	p.Servers = kept
	if p.Servers == nil {
		p.Servers = []string{}
	}
	s.Profiles[name] = p
	sort.Strings(removed)
	return removed, nil
}

// Chain returns the inheritance visit order for name, base-first:
// [base, ..., grandparent, parent, name]. Cycles and dangling parents
// are errors. base itself yields [base].
func (s *State) Chain(name string) ([]string, error) {
	if _, ok := s.Profiles[name]; !ok {
		return nil, fmt.Errorf("profile %q does not exist", name)
	}
	var reversed []string
	seen := make(map[string]bool)
	cur := name
	for {
		if seen[cur] {
			return nil, fmt.Errorf("inheritance cycle detected at profile %q", cur)
		}
		seen[cur] = true
		reversed = append(reversed, cur)
		p, ok := s.Profiles[cur]
		if !ok {
			// Unreachable in practice (entry points and every step check
			// existence first) — kept as a defensive guard with a message
			// that names the one missing profile.
			return nil, fmt.Errorf("profile %q not found during inheritance walk", cur)
		}
		switch len(p.Inherits) {
		case 0:
			// Implicit base-only chain ends here — append base unless cur
			// IS base (base never records a parent).
			if cur != BaseProfile {
				reversed = append(reversed, BaseProfile)
			}
			// Reverse into base-first order.
			out := make([]string, 0, len(reversed))
			for i := len(reversed) - 1; i >= 0; i-- {
				out = append(out, reversed[i])
			}
			return out, nil
		case 1:
			parent := p.Inherits[0]
			if _, ok := s.Profiles[parent]; !ok {
				return nil, fmt.Errorf("profile %q inherits missing profile %q", cur, parent)
			}
			cur = parent
		default:
			return nil, fmt.Errorf("profile %q has %d parents; only one is allowed", cur, len(p.Inherits))
		}
	}
}

// TargetSet returns the deduped server set `pharos profile use`
// reconciles a profile's clients to: the union of every profile's
// servers along the base-first chain, deduped preserving first
// occurrence (base servers win positionally).
func (s *State) TargetSet(name string) ([]string, error) {
	chain, err := s.Chain(name)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var out []string
	for _, profile := range chain {
		for _, srv := range s.Profiles[profile].Servers {
			if seen[srv] {
				continue
			}
			seen[srv] = true
			out = append(out, srv)
		}
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// Validate checks every profile: at most one parent, parents exist, and
// no inheritance cycles (each chain walk must terminate at base).
func (s *State) Validate() error {
	names := make([]string, 0, len(s.Profiles))
	for name := range s.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := s.Profiles[name]
		switch len(p.Inherits) {
		case 0:
			// ok — implicit base-only inheritance
		case 1:
			parent := p.Inherits[0]
			if _, ok := s.Profiles[parent]; !ok {
				return fmt.Errorf("profile %q inherits missing profile %q", name, parent)
			}
		default:
			return fmt.Errorf("profile %q has %d parents; only one is allowed", name, len(p.Inherits))
		}
		// Every chain must terminate; Chain errors on cycles and dangling
		// parents. Chain(base) is [base] unless base itself is corrupt.
		if _, err := s.Chain(name); err != nil {
			return err
		}
	}
	return nil
}

// ProfiledBy maps every server name attached to any profile to the
// sorted list of profiles holding it — the classifier behind the
// "unprofiled" label in `pharos profile use`.
func (s *State) ProfiledBy() map[string][]string {
	out := make(map[string][]string)
	for name, p := range s.Profiles {
		for _, srv := range p.Servers {
			out[srv] = append(out[srv], name)
		}
	}
	for srv := range out {
		sort.Strings(out[srv])
	}
	return out
}

// normalizeNames trims, drops empties, dedups, and sorts a name list.
func normalizeNames(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out
}
