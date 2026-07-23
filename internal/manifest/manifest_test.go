package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseValid verifies parsing a well-formed manifest.
func TestParseValid(t *testing.T) {
	data := []byte(`{
		"name": "my-mcp-server",
		"version": "1.0.0",
		"description": "An MCP server for X",
		"license": "MIT",
		"homepage": "https://github.com/user/repo",
		"repository": "https://github.com/user/repo",
		"bin": "./server.js",
		"files": ["./server.js", "./lib/"]
	}`)
	m, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "my-mcp-server" {
		t.Errorf("name = %s", m.Name)
	}
	if m.Version != "1.0.0" {
		t.Errorf("version = %s", m.Version)
	}
	if m.License != "MIT" {
		t.Errorf("license = %s", m.License)
	}
	if len(m.Files) != 2 {
		t.Errorf("files len = %d", len(m.Files))
	}
}

// TestParseInvalidJSON verifies error on bad JSON.
func TestParseInvalidJSON(t *testing.T) {
	_, err := Parse([]byte(`{bad json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestValidateMissingName verifies validation catches missing name.
func TestValidateMissingName(t *testing.T) {
	m := &Manifest{Version: "1.0.0"}
	if err := m.Validate(); err == nil {
		t.Fatal("expected validation error for missing name")
	}
}

// TestValidateMissingVersion verifies validation catches missing version.
func TestValidateMissingVersion(t *testing.T) {
	m := &Manifest{Name: "test"}
	if err := m.Validate(); err == nil {
		t.Fatal("expected validation error for missing version")
	}
}

// TestValidateOK verifies that a complete manifest validates.
func TestValidateOK(t *testing.T) {
	m := &Manifest{Name: "test", Version: "1.0.0"}
	if err := m.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

// TestLoad verifies loading a manifest from a directory.
func TestLoad(t *testing.T) {
	dir := t.TempDir()
	data := `{"name":"test","version":"2.0.0"}`
	if err := os.WriteFile(filepath.Join(dir, "pharos.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "test" {
		t.Errorf("name = %s", m.Name)
	}
	if m.Version != "2.0.0" {
		t.Errorf("version = %s", m.Version)
	}
}

// TestLoadMissingFile verifies error when pharos.json doesn't exist.
func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
