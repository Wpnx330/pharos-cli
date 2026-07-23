package clientconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// helper: create a fake app directory so Detect() picks it up, return
// the Client with the config path set.
func setupClient(t *testing.T, id ClientID) Client {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if id == ClientClaudeDesktop {
		dir := filepath.Join(home, ".config", "Claude")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if id == ClientCursor {
		dir := filepath.Join(home, ".cursor")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if id == ClientGeneric {
		dir := filepath.Join(home, ".config", "mcp")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range candidatePaths() {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("client %s not found in candidates", id)
	return Client{}
}

func TestDetectFindsCursor(t *testing.T) {
	setupClient(t, ClientCursor)
	clients := Detect()
	found := false
	for _, c := range clients {
		if c.ID == ClientCursor {
			found = true
		}
	}
	if !found {
		t.Error("Detect did not find Cursor")
	}
}

func TestDetectFindsGeneric(t *testing.T) {
	setupClient(t, ClientGeneric)
	clients := Detect()
	found := false
	for _, c := range clients {
		if c.ID == ClientGeneric {
			found = true
		}
	}
	if !found {
		t.Error("Detect did not find Generic")
	}
}

func TestDetectByID(t *testing.T) {
	setupClient(t, ClientCursor)
	c := DetectByID(ClientCursor)
	if c == nil {
		t.Fatal("DetectByID returned nil")
	}
	if c.ID != ClientCursor {
		t.Errorf("id = %s", c.ID)
	}
}

func TestDetectByIDNotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := DetectByID(ClientCursor)
	if c != nil {
		t.Error("expected nil when no clients detected")
	}
}

func TestMergeServerCreatesNewConfig(t *testing.T) {
	c := setupClient(t, ClientGeneric)
	server := ServerConfig{
		Command: "npx",
		Args:    []string{"-y", "@scope/pkg"},
		Env:     map[string]string{"FOO": "bar"},
	}
	if err := MergeServer(c, "my-server", server); err != nil {
		t.Fatal(err)
	}
	servers, err := ReadServers(c.Path)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := servers["my-server"]
	if !ok {
		t.Fatal("server not written")
	}
	var entry map[string]any
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatal(err)
	}
	if entry["command"] != "npx" {
		t.Errorf("command = %v", entry["command"])
	}
}

func TestMergeServerPreservesExisting(t *testing.T) {
	c := setupClient(t, ClientGeneric)
	// Pre-write an existing server.
	existing := configFile{
		McpServers: map[string]json.RawMessage{},
	}
	existing.McpServers["old-server"], _ = json.Marshal(map[string]any{
		"command": "node",
		"args":    []string{"old.js"},
	})
	data, _ := json.MarshalIndent(existing, "", "  ")
	data = append(data, '\n')
	if err := os.WriteFile(c.Path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	c.Existing = true

	server := ServerConfig{Command: "npx", Args: []string{"-y", "@scope/new"}}
	if err := MergeServer(c, "new-server", server); err != nil {
		t.Fatal(err)
	}

	servers, err := ReadServers(c.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := servers["old-server"]; !ok {
		t.Error("old-server was clobbered")
	}
	if _, ok := servers["new-server"]; !ok {
		t.Error("new-server not written")
	}
}

func TestMergeServerReplacesSameName(t *testing.T) {
	c := setupClient(t, ClientGeneric)
	existing := configFile{
		McpServers: map[string]json.RawMessage{},
	}
	existing.McpServers["my-server"], _ = json.Marshal(map[string]any{
		"command": "old-cmd",
	})
	data, _ := json.MarshalIndent(existing, "", "  ")
	data = append(data, '\n')
	os.WriteFile(c.Path, data, 0o644)
	c.Existing = true

	server := ServerConfig{Command: "new-cmd", Args: []string{"run"}}
	if err := MergeServer(c, "my-server", server); err != nil {
		t.Fatal(err)
	}
	servers, _ := ReadServers(c.Path)
	var entry map[string]any
	json.Unmarshal(servers["my-server"], &entry)
	if entry["command"] != "new-cmd" {
		t.Errorf("command = %v, want new-cmd", entry["command"])
	}
	if len(servers) != 1 {
		t.Errorf("expected 1 server, got %d", len(servers))
	}
}

func TestMergeServerHTTPCursor(t *testing.T) {
	c := setupClient(t, ClientCursor)
	server := ServerConfig{URL: "https://example.com/sse", Type: "sse"}
	if err := MergeServer(c, "remote-server", server); err != nil {
		t.Fatal(err)
	}
	servers, _ := ReadServers(c.Path)
	var entry map[string]any
	json.Unmarshal(servers["remote-server"], &entry)
	if entry["url"] != "https://example.com/sse" {
		t.Errorf("url = %v", entry["url"])
	}
	if entry["type"] != "sse" {
		t.Errorf("type = %v", entry["type"])
	}
}

func TestReadServersMissingFile(t *testing.T) {
	servers, err := ReadServers(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 0 {
		t.Errorf("expected empty, got %d", len(servers))
	}
}
