package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
)

func TestQAOpenCodeConfigPreservation(t *testing.T) {
	home, _ := os.UserHomeDir()
	configPath := filepath.Join(home, ".config", "opencode", "opencode.json")

	// Write a config with unknown top-level keys
	configDir := filepath.Dir(configPath)
	os.MkdirAll(configDir, 0755)

	before := `{
  "model": "claude-sonnet-4-20250514",
  "theme": "dark",
  "tab_size": 2,
  "mcpServers": {
    "existing-server": {
      "command": "node",
      "args": ["server.js"]
    }
  }
}`
	os.WriteFile(configPath, []byte(before), 0644)

	// Find the OpenCode client
	clients := clientconfig.Detect()
	var oc clientconfig.Client
	for _, c := range clients {
		if c.ID == clientconfig.ClientOpenCode {
			oc = c
			break
		}
	}
	if oc.ID == "" {
		t.Skip("OpenCode not detected")
	}

	// Merge a new server
	err := clientconfig.MergeServer(oc, "pharos-test-server", clientconfig.ServerConfig{
		Command: "python3",
		Args:    []string{"-m", "src.server"},
		Env:     map[string]string{"DEBUG": "true"},
	})
	if err != nil {
		t.Fatalf("MergeServer error: %v", err)
	}

	// Read after
	after, _ := os.ReadFile(configPath)
	afterStr := string(after)

	// Verify unknown keys survived
	checks := []string{`"model"`, `"theme"`, `"tab_size"`, `"existing-server"`, `"pharos-test-server"`}
	for _, s := range checks {
		if !strings.Contains(afterStr, s) {
			t.Errorf("Expected %s in config but not found", s)
		}
	}

	t.Logf("Config after merge:\n%s", afterStr)

	// Cleanup: restore original
	os.WriteFile(configPath, []byte(before), 0644)
}
