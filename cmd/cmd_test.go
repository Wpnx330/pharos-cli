package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/api"
)

// --- init command tests ---

// TestSplitCSV verifies the comma-separated parsing used by init.
func TestSplitCSV(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"tools", []string{"tools"}},
		{"tools, resources, prompts", []string{"tools", "resources", "prompts"}},
		{"  tools  ,  resources  ", []string{"tools", "resources"}},
		{",,", []string{}}, // only empty parts → trimmed to nothing
	}
	for _, tt := range tests {
		got := splitCSV(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("splitCSV(%q) = %v (len %d), want %v (len %d)", tt.input, got, len(got), tt.want, len(tt.want))
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

// TestDefaultCommand verifies runtime-based default commands.
func TestDefaultCommand(t *testing.T) {
	tests := []struct {
		runtime  string
		contains string
	}{
		{"node", "node server.js"},
		{"python", "python -m my_mcp_server"},
		{"docker", "docker run -i my-mcp-server"},
		{"unknown", "node server.js"},
	}
	for _, tt := range tests {
		got := defaultCommand(tt.runtime, "stdio")
		if !strings.Contains(got, tt.contains) {
			t.Errorf("defaultCommand(%q) = %q, want it to contain %q", tt.runtime, got, tt.contains)
		}
	}
}

// TestManifestGeneration verifies that a manifest with all fields set
// serialises to valid JSON with the expected structure.
func TestManifestGeneration(t *testing.T) {
	// Simulate what buildManifestInteractive produces
	m := &manifestForTest{
		Name:         "my-mcp-server",
		Version:      "0.1.0",
		Description:  "A test MCP server",
		Transport:    "stdio",
		Runtime:      "node",
		License:      "MIT",
		Bin:          "node server.js",
		Capabilities: []string{"tools", "resources"},
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// Verify we can round-trip
	var decoded manifestForTest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Name != "my-mcp-server" {
		t.Errorf("name = %s", decoded.Name)
	}
	if decoded.Transport != "stdio" {
		t.Errorf("transport = %s", decoded.Transport)
	}
	if len(decoded.Capabilities) != 2 {
		t.Errorf("capabilities len = %d", len(decoded.Capabilities))
	}
}

// manifestForTest mirrors the manifest structure produced by init.
type manifestForTest struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description"`
	Transport    string   `json:"transport,omitempty"`
	Runtime      string   `json:"runtime,omitempty"`
	License      string   `json:"license"`
	Bin          string   `json:"bin"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// --- audit command tests ---

// TestFormatAuditReportNoVulns verifies the report shows "ok" when clean.
func TestFormatAuditReportNoVulns(t *testing.T) {
	report := &auditReport{
		Total:   1,
		Scanned: 1,
		Entries: []auditEntry{
			{Server: "safe-server", Version: "1.0.0"},
		},
	}
	output := formatAuditReport(report)
	if !strings.Contains(output, "safe-server") {
		t.Error("output should contain server name")
	}
	if !strings.Contains(output, "No vulnerabilities found") {
		t.Error("output should say no vulnerabilities found")
	}
}

// TestFormatAuditReportWithVulns verifies the report shows advisories.
func TestFormatAuditReportWithVulns(t *testing.T) {
	report := &auditReport{
		Total:      1,
		Scanned:    1,
		Vulnerable: 1,
		HasVulns:   true,
		Entries: []auditEntry{
			{
				Server:  "vuln-server",
				Version: "1.0.0",
				Advisories: []api.Advisory{
					{
						ID:       "PHAROS-001",
						Title:    "RCE in parser",
						Severity: "critical",
						FixedIn:  "1.1.0",
					},
				},
			},
		},
	}
	output := formatAuditReport(report)
	if !strings.Contains(output, "vuln-server") {
		t.Error("output should contain vulnerable server name")
	}
	if !strings.Contains(output, "PHAROS-001") {
		t.Error("output should contain advisory ID")
	}
	if !strings.Contains(output, "critical") {
		t.Error("output should contain severity")
	}
	if !strings.Contains(output, "vulnerable") {
		t.Error("output should mention vulnerabilities found")
	}
}

// TestFilterApplicable verifies advisory filtering by version range.
func TestFilterApplicable(t *testing.T) {
	advisories := []api.Advisory{
		{ID: "A1", Affected: "< 2.0.0", Severity: "high"},
		{ID: "A2", Affected: ">= 3.0.0", Severity: "medium"}, // can't parse, included
		{ID: "A3", Affected: "", Severity: "low"},            // no range, included
	}

	// Version 1.5.0 — A1 applies (< 2.0.0), A2 can't parse so included, A3 included
	filtered := filterApplicable(advisories, "1.5.0")
	if len(filtered) != 3 {
		t.Errorf("expected 3 applicable advisories for 1.5.0, got %d", len(filtered))
	}

	// Version 2.5.0 — A1 does NOT apply (>= 2.0.0)
	filtered = filterApplicable(advisories, "2.5.0")
	if len(filtered) != 2 {
		t.Errorf("expected 2 applicable advisories for 2.5.0, got %d", len(filtered))
	}
}

// TestCompareVersions verifies semver comparison.
func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.0.0", 1},
		{"1.2.0", "1.3.0", -1},
		{"1.0.1", "1.0.0", 1},
	}
	for _, tt := range tests {
		got := compareVersions(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("compareVersions(%s, %s) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// --- import command tests ---

// TestImportReadClientConfig verifies that client config JSON is parsed
// correctly into server entries.
func TestImportReadClientConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "mcp.json")

	// Create a Claude Desktop-style config
	configData := `{
		"mcpServers": {
			"server-a": {
				"command": "npx",
				"args": ["-y", "@example/server-a"]
			},
			"server-b": {
				"command": "node",
				"args": ["./server.js"],
				"env": {"API_KEY": "secret"}
			}
		}
	}`
	if err := os.WriteFile(configPath, []byte(configData), 0o644); err != nil {
		t.Fatal(err)
	}

	// Read the config using the same logic the import command uses
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	// Parse as wrapped form (mcpServers)
	var wrapped struct {
		McpServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		t.Fatal(err)
	}

	if len(wrapped.McpServers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(wrapped.McpServers))
	}

	sa, ok := wrapped.McpServers["server-a"]
	if !ok {
		t.Fatal("server-a not found")
	}
	if sa.Command != "npx" {
		t.Errorf("server-a command = %s, want npx", sa.Command)
	}
	if len(sa.Args) != 2 || sa.Args[1] != "@example/server-a" {
		t.Errorf("server-a args = %v", sa.Args)
	}

	sb, ok := wrapped.McpServers["server-b"]
	if !ok {
		t.Fatal("server-b not found")
	}
	if sb.Env["API_KEY"] != "secret" {
		t.Errorf("server-b env API_KEY = %s", sb.Env["API_KEY"])
	}
}

// TestImportReadBareConfig verifies parsing of a bare (non-wrapped) config.
func TestImportReadBareConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "mcp.json")

	configData := `{
		"my-server": {
			"command": "python",
			"args": ["-m", "my_server"]
		}
	}`
	if err := os.WriteFile(configPath, []byte(configData), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	var bare map[string]struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if err := json.Unmarshal(data, &bare); err != nil {
		t.Fatal(err)
	}

	if len(bare) != 1 {
		t.Fatalf("expected 1 server, got %d", len(bare))
	}
	s, ok := bare["my-server"]
	if !ok {
		t.Fatal("my-server not found")
	}
	if s.Command != "python" {
		t.Errorf("command = %s, want python", s.Command)
	}
}
