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
	Homepage     string   `json:"homepage"`
	Repository   string   `json:"repository"`
	Bin          string   `json:"bin"`
	Files        []string `json:"files"`
	Capabilities []string `json:"capabilities,omitempty"`
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
