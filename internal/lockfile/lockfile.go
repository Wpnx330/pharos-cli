// Package lockfile reads and writes the pharos.lock file that records
// the exact version, integrity hash, transport, and resolved URL of
// every installed MCP server.
package lockfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// LockVersion is the schema version of the lockfile format.
const LockVersion = 1

// Lockfile is the top-level lockfile structure.
type Lockfile struct {
	Version int                    `json:"version"`
	Servers map[string]ServerEntry `json:"servers"`
}

// ServerEntry records the resolved metadata for one installed server.
type ServerEntry struct {
	Version     string    `json:"version"`
	Integrity   string    `json:"integrity"`
	Transport   string    `json:"transport"`
	Resolved    string    `json:"resolved"`
	InstalledAt time.Time `json:"installedAt"`
}

// New creates an empty lockfile with the current schema version.
func New() *Lockfile {
	return &Lockfile{
		Version: LockVersion,
		Servers: make(map[string]ServerEntry),
	}
}

// DefaultPath returns the lockfile location: ./pharos.lock in the current
// working directory if writable, otherwise ~/.pharos/pharos.lock.
func DefaultPath() (string, error) {
	cwd, err := os.Getwd()
	if err == nil {
		candidate := filepath.Join(cwd, "pharos.lock")
		// If a lockfile already exists in cwd, or cwd is writable, prefer it.
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		if writable(cwd) {
			return candidate, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pharos", "pharos.lock"), nil
}

func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".pharos-write-test-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

// Load reads the lockfile from the given path. If the file does not
// exist a new empty lockfile is returned without error.
func Load(path string) (*Lockfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return nil, fmt.Errorf("read lockfile: %w", err)
	}
	var lf Lockfile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("parse lockfile: %w", err)
	}
	if lf.Servers == nil {
		lf.Servers = make(map[string]ServerEntry)
	}
	return &lf, nil
}

// Save writes the lockfile to the given path with 0o644 permissions,
// creating parent directories as needed.
func (lf *Lockfile) Save(path string) error {
	if lf.Servers == nil {
		lf.Servers = make(map[string]ServerEntry)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create lockfile dir: %w", err)
	}
	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal lockfile: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write lockfile: %w", err)
	}
	return nil
}

// Set inserts or replaces a server entry.
func (lf *Lockfile) Set(name string, entry ServerEntry) {
	if lf.Servers == nil {
		lf.Servers = make(map[string]ServerEntry)
	}
	lf.Servers[name] = entry
}

// Get retrieves a server entry, returning ok=false if absent.
func (lf *Lockfile) Get(name string) (ServerEntry, bool) {
	e, ok := lf.Servers[name]
	return e, ok
}

// Has reports whether a server is recorded in the lockfile.
func (lf *Lockfile) Has(name string) bool {
	_, ok := lf.Servers[name]
	return ok
}

// Remove deletes a server entry, returning ok=false if it was absent.
func (lf *Lockfile) Remove(name string) bool {
	if _, ok := lf.Servers[name]; !ok {
		return false
	}
	delete(lf.Servers, name)
	return true
}

// RemoveServer is an alias for Remove that also returns nothing (for
// compatibility with commands that use a method-style call).
func (lf *Lockfile) RemoveServer(name string) bool {
	return lf.Remove(name)
}

// Server is a flat representation of a lockfile entry, used by commands
// that iterate over servers with metadata like managed status and source.
type Server struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Integrity string `json:"integrity,omitempty"`
	Transport string `json:"transport,omitempty"`
	Resolved  string `json:"resolved,omitempty"`
	Managed   bool   `json:"managed"`
	Source    string `json:"source,omitempty"`
}

// AddServer adds or replaces a Server in the lockfile.
func (lf *Lockfile) AddServer(s Server) {
	if lf.Servers == nil {
		lf.Servers = make(map[string]ServerEntry)
	}
	lf.Servers[s.Name] = ServerEntry{
		Version:     s.Version,
		Integrity:   s.Integrity,
		Transport:   s.Transport,
		Resolved:    s.Resolved,
		InstalledAt: time.Now().UTC(),
	}
}

// SortedServers returns all servers sorted by name for deterministic
// display. The returned slice includes the lockfile entry data plus
// Managed=true (since lockfile servers are managed by Pharos).
func (lf *Lockfile) SortedServers() []Server {
	names := make([]string, 0, len(lf.Servers))
	for name := range lf.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]Server, 0, len(names))
	for _, name := range names {
		e := lf.Servers[name]
		result = append(result, Server{
			Name:      name,
			Version:   e.Version,
			Integrity: e.Integrity,
			Transport: e.Transport,
			Resolved:  e.Resolved,
			Managed:   true,
			Source:    "lockfile",
		})
	}
	return result
}

// Exists reports whether a lockfile exists at the given directory or path.
// If a directory is given, it checks for pharos.lock inside it.
func Exists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		_, err = os.Stat(filepath.Join(path, "pharos.lock"))
		return err == nil
	}
	return true
}
