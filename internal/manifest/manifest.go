// Package manifest defines the pharos.json package manifest structure
// and provides parsing/validation helpers.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Manifest represents the contents of a pharos.json file.
type Manifest struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description"`
	Transport    string   `json:"transport,omitempty"`
	Runtime      string   `json:"runtime,omitempty"`
	License      string   `json:"license"`
	Homepage     string   `json:"homepage,omitempty"`
	Repository   string   `json:"repository,omitempty"`
	Bin          string   `json:"bin,omitempty"`
	Command      string   `json:"command,omitempty"`
	Entrypoint   string   `json:"entrypoint,omitempty"`
	Files        []string `json:"files,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Args         []string          `json:"args,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	ToolsCount   int      `json:"tools_count,omitempty"`
	// Dependencies lists other Pharos packages required by this package.
	// Each entry's Version is a semver constraint resolved at install time.
	Dependencies []Dependency `json:"dependencies,omitempty"`
}

// Dependency declares a dependency on another Pharos package.
type Dependency struct {
	Name    string `json:"name"`
	Version string `json:"version"` // semver constraint like "^1.0.0"
}

// Load reads and parses a pharos.json file from the given directory.
func Load(dir string) (*Manifest, error) {
	path := filepath.Join(dir, "pharos.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read pharos.json: %w", err)
	}
	return Parse(data)
}

// Parse decodes a manifest from raw JSON bytes.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse pharos.json: %w", err)
	}
	return &m, nil
}

// Validate checks that the manifest has the required fields: name and
// version.
func (m *Manifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("manifest missing required field: name")
	}
	if m.Version == "" {
		return fmt.Errorf("manifest missing required field: version")
	}
	return nil
}

// RunCommand returns the command used to launch the MCP server.
// Prefers "command" field, falls back to "bin" for backwards compat.
func (m *Manifest) RunCommand() string {
	if m.Command != "" {
		return m.Command
	}
	return m.Bin
}
