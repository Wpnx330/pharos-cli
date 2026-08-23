package cmd

import (
	"fmt"
	"os"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
)

// writeTemp creates a temp file with the given content and returns its path.
// The file is automatically cleaned up when the test finishes.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "doctor-test-*.cfg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		valid   string
		invalid string
	}{
		{
			name:    "FormatMcpServers (JSON)",
			format:  clientconfig.FormatMcpServers,
			valid:   `{"mcpServers": {"foo": {"command": "bar"}}}`,
			invalid: `{not valid json`,
		},
		{
			name:    "FormatArray (JSON)",
			format:  clientconfig.FormatArray,
			valid:   `[{"name": "foo"}]`,
			invalid: `[not valid`,
		},
		{
			name:    "FormatOpenCode (JSON)",
			format:  clientconfig.FormatOpenCode,
			valid:   `{"mcp": {"foo": {}}}`,
			invalid: `{broken`,
		},
		{
			name:    "FormatZed (JSON)",
			format:  clientconfig.FormatZed,
			valid:   `{"context_servers": {"foo": {}}}`,
			invalid: `{broken`,
		},
		{
			name:    "FormatTOML",
			format:  clientconfig.FormatTOML,
			valid:   "[mcp_servers.foo]\ncommand = \"bar\"",
			invalid: "[mcp_servers.foo\ncommand = broken",
		},
		{
			name:    "FormatHermes (YAML)",
			format:  clientconfig.FormatHermes,
			valid:   "mcp_servers:\n  foo:\n    command: bar",
			invalid: "mcp_servers: [invalid: yaml: here",
		},
		{
			name:    "FormatAider (YAML)",
			format:  clientconfig.FormatAider,
			valid:   "mcp-servers:\n  - name: foo",
			invalid: "mcp-servers: [: broken: yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+"_valid", func(t *testing.T) {
			f := writeTemp(t, tt.valid)
			c := clientconfig.Client{Path: f, Format: tt.format}
			detail, err := validateConfig(c)
			if err != nil {
				t.Errorf("expected no error for valid %s, got: %v", tt.format, err)
			}
			if detail == "" {
				t.Error("expected non-empty detail string")
			}
		})
		t.Run(tt.name+"_invalid", func(t *testing.T) {
			f := writeTemp(t, tt.invalid)
			c := clientconfig.Client{Path: f, Format: tt.format}
			_, err := validateConfig(c)
			if err == nil {
				t.Errorf("expected error for invalid %s, got nil", tt.format)
			}
		})
	}
}

func TestValidateConfig_MissingFile(t *testing.T) {
	c := clientconfig.Client{Path: "/nonexistent/path/config.json", Format: clientconfig.FormatMcpServers}
	_, err := validateConfig(c)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestRunCheck(t *testing.T) {
	// Success case
	c := runCheck("test-ok", func() (string, error) {
		return "all good", nil
	})
	if c.Status != "ok" || c.Detail != "all good" {
		t.Errorf("expected ok status with detail, got %+v", c)
	}

	// Failure case
	c = runCheck("test-fail", func() (string, error) {
		return "", fmt.Errorf("something broke")
	})
	if c.Status != "fail" || c.Error != "something broke" {
		t.Errorf("expected fail status with error, got %+v", c)
	}
}
