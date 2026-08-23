package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
)

func TestValidateConfigFormats(t *testing.T) {
	tests := []struct {
		name      string
		format    string
		content   string
		wantError string
	}{
		{name: "mcpServers JSON", format: clientconfig.FormatMcpServers, content: `{"mcpServers":{}}`},
		{name: "array JSON", format: clientconfig.FormatArray, content: `[]`},
		{name: "OpenCode JSON", format: clientconfig.FormatOpenCode, content: `{"mcp":{}}`},
		{name: "Zed JSON", format: clientconfig.FormatZed, content: `{"context_servers":{}}`},
		{name: "invalid mcpServers JSON", format: clientconfig.FormatMcpServers, content: `{`, wantError: "invalid JSON"},
		{name: "invalid array JSON", format: clientconfig.FormatArray, content: `[`, wantError: "invalid JSON"},
		{name: "invalid OpenCode JSON", format: clientconfig.FormatOpenCode, content: `{`, wantError: "invalid JSON"},
		{name: "invalid Zed JSON", format: clientconfig.FormatZed, content: `{`, wantError: "invalid JSON"},
		{name: "TOML", format: clientconfig.FormatTOML, content: "[mcp_servers.demo]\ncommand = \"echo\"\n"},
		{name: "invalid TOML", format: clientconfig.FormatTOML, content: "[mcp_servers.demo]\ncommand =\n", wantError: "invalid TOML"},
		{name: "Hermes YAML", format: clientconfig.FormatHermes, content: "mcp_servers:\n  demo:\n    command: echo\n"},
		{name: "Aider YAML", format: clientconfig.FormatAider, content: "mcp-servers:\n  - name: demo\n"},
		{name: "invalid Hermes YAML", format: clientconfig.FormatHermes, content: "mcp_servers: [\n", wantError: "invalid YAML"},
		{name: "invalid Aider YAML", format: clientconfig.FormatAider, content: "mcp-servers: [\n", wantError: "invalid YAML"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			detail, err := validateConfig(clientconfig.Client{Path: path, Format: test.format})
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("validateConfig() error = %v", err)
				}
				if detail != "valid" {
					t.Fatalf("validateConfig() detail = %q, want valid", detail)
				}
				return
			}

			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("validateConfig() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestRunConfigCheckIncludesFormatInJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[mcp_servers]\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	check := runConfigCheck(clientconfig.Client{
		Name:   "Codex CLI",
		Path:   path,
		Format: clientconfig.FormatTOML,
	})
	if check.Status != "ok" {
		t.Fatalf("runConfigCheck() status = %q, error = %q", check.Status, check.Error)
	}
	if check.Format != clientconfig.FormatTOML {
		t.Fatalf("runConfigCheck() format = %q, want %q", check.Format, clientconfig.FormatTOML)
	}

	payload, err := json.Marshal(check)
	if err != nil {
		t.Fatalf("marshal doctor check: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal doctor check: %v", err)
	}
	if decoded["format"] != clientconfig.FormatTOML {
		t.Fatalf("JSON format = %v, want %q", decoded["format"], clientconfig.FormatTOML)
	}
}
