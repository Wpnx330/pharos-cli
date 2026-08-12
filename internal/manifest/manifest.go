// Package manifest defines the pharos.json package manifest structure
// and provides parsing/validation helpers.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// Tags are optional text hashtags (max 3) for discoverability.
	// Lowercase, alphanumeric + hyphens, max 20 chars each.
	Tags []string `json:"tags,omitempty"`
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
// version. It also validates optional tag format: max 3 tags, lowercase
// alphanumeric + hyphens, max 20 chars each.
func (m *Manifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("manifest missing required field: name")
	}
	if m.Version == "" {
		return fmt.Errorf("manifest missing required field: version")
	}
	if len(m.Tags) > 3 {
		return fmt.Errorf("manifest may have at most 3 tags, got %d", len(m.Tags))
	}
	for _, tag := range m.Tags {
		if !isValidManifestTag(tag) {
			return fmt.Errorf("invalid tag %q: must be lowercase, alphanumeric + hyphens, max 20 chars", tag)
		}
	}
	return nil
}

// isValidManifestTag returns true if the tag contains only lowercase
// alphanumeric and hyphen characters and is at most 20 characters long.
func isValidManifestTag(tag string) bool {
	if tag == "" || len(tag) > 20 {
		return false
	}
	for _, r := range tag {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '-':
			// allowed
		default:
			return false
		}
	}
	return true
}

// NormalizeTags lowercases and trims all tags, dropping any that are empty
// or contain invalid characters. Caps the result at 3 tags.
func (m *Manifest) NormalizeTags() {
	if len(m.Tags) == 0 {
		return
	}
	seen := make(map[string]bool)
	var result []string
	for _, t := range m.Tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || !isValidManifestTag(t) {
			continue
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		result = append(result, t)
		if len(result) >= 3 {
			break
		}
	}
	m.Tags = result
}

// RunCommand returns the command used to launch the MCP server.
// Prefers "command" field, falls back to "bin" for backwards compat.
func (m *Manifest) RunCommand() string {
	if m.Command != "" {
		return m.Command
	}
	return m.Bin
}
