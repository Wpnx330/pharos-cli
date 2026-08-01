package clientconfig

import (
	"encoding/json"
	"fmt"
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

// TestMergeServerArrayFormat verifies that MergeServer writes a flat
// JSON array when the client uses the "array" format.
func TestMergeServerArrayFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	c := Client{
		ID:     "my-editor",
		Name:   "my-editor",
		Path:   path,
		Format: FormatArray,
	}
	server := ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}}
	if err := MergeServer(c, "my-server", server); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries []arrayEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("expected flat array, parse error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "my-server" {
		t.Errorf("name = %s", entries[0].Name)
	}
	if entries[0].Command != "npx" {
		t.Errorf("command = %s", entries[0].Command)
	}
}

// TestMergeServerArrayPreservesExisting verifies that adding a second
// server to an array-format config preserves the first.
func TestMergeServerArrayPreservesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	c := Client{ID: "ed", Name: "ed", Path: path, Format: FormatArray}
	if err := MergeServer(c, "a", ServerConfig{Command: "cmd-a"}); err != nil {
		t.Fatal(err)
	}
	c.Existing = true
	if err := MergeServer(c, "b", ServerConfig{Command: "cmd-b"}); err != nil {
		t.Fatal(err)
	}
	servers, err := ReadServersFormat(path, FormatArray)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(servers))
	}
	if _, ok := servers["a"]; !ok {
		t.Error("server a missing")
	}
	if _, ok := servers["b"]; !ok {
		t.Error("server b missing")
	}
}

// TestReadServersArrayFormat verifies reading an array-format config
// returns a name→raw map.
func TestReadServersArrayFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	entries := []arrayEntry{
		{Name: "alpha", Command: "node", Args: []string{"a.js"}},
		{Name: "beta", URL: "https://example.com/sse", Type: "sse"},
	}
	data, _ := json.MarshalIndent(entries, "", "  ")
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	servers, err := ReadServersFormat(path, FormatArray)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 {
		t.Fatalf("expected 2, got %d", len(servers))
	}
	if _, ok := servers["alpha"]; !ok {
		t.Error("alpha missing")
	}
}

// TestDetectCustomClient verifies that a custom client registered in
// config.json is returned by Detect when its config file exists.
func TestDetectCustomClient(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create the custom client's config file so it's "detected".
	customDir := filepath.Join(home, ".my-editor")
	if err := os.MkdirAll(customDir, 0o755); err != nil {
		t.Fatal(err)
	}
	customPath := filepath.Join(customDir, "mcp.json")
	if err := os.WriteFile(customPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Register the custom client in ~/.pharos/config.json
	pharosDir := filepath.Join(home, ".pharos")
	if err := os.MkdirAll(pharosDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgJSON := fmt.Sprintf(`{"registry":"https://x","custom_clients":[{"id":"my-editor","path":%q,"format":"mcpServers"}]}`, customPath)
	if err := os.WriteFile(filepath.Join(pharosDir, "config.json"), []byte(cfgJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	clients := Detect()
	found := false
	for _, c := range clients {
		if c.ID == "my-editor" && c.Custom && c.Existing {
			found = true
		}
	}
	if !found {
		t.Error("Detect did not return the custom client as detected")
	}
}

// TestDetectCustomClientMissingFile verifies that a custom client whose
// config file does NOT exist is not returned by Detect.
func TestDetectCustomClientMissingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pharosDir := filepath.Join(home, ".pharos")
	if err := os.MkdirAll(pharosDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgJSON := `{"registry":"https://x","custom_clients":[{"id":"ed","path":"/nonexistent/path/mcp.json","format":"mcpServers"}]}`
	if err := os.WriteFile(filepath.Join(pharosDir, "config.json"), []byte(cfgJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range Detect() {
		if c.Custom {
			t.Errorf("did not expect custom client in Detect() when file missing, got %+v", c)
		}
	}
}

// TestCandidatePathsExported verifies CandidatePaths returns built-ins
// with Format set.
func TestCandidatePathsExported(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, c := range CandidatePaths() {
		if c.Format != FormatMcpServers {
			t.Errorf("client %s format = %s, want mcpServers", c.Name, c.Format)
		}
	}
}
