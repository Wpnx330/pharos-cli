package clientconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateWindowsUsers points windowsUserDirs() at an empty temp tree so
// Detect / Write never walk the live /mnt/c/Users profiles.
func isolateWindowsUsers(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "win-users")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PHAROS_WINDOWS_USERS_ROOT", root)
	return root
}

// helper: create a fake app directory so Detect() picks it up, return
// the Client with the config path set.
func setupClient(t *testing.T, id ClientID) Client {
	t.Helper()
	isolateWindowsUsers(t)
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
	isolateWindowsUsers(t)
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
	isolateWindowsUsers(t)
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
	isolateWindowsUsers(t)
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
// with Format set to a known format.
func TestCandidatePathsExported(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, c := range CandidatePaths() {
		switch c.Format {
		case FormatMcpServers, FormatArray, FormatOpenCode, FormatHermes:
			// valid format
		default:
			t.Errorf("client %s format = %s, unknown format", c.Name, c.Format)
		}
	}
}

func writeJSONFixture(t *testing.T, path string, v any) []byte {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return data
}

func readJSONObject(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, data)
	}
	return root
}

func mcpServerNames(t *testing.T, root map[string]json.RawMessage) map[string]bool {
	t.Helper()
	return namedServers(t, root, "mcpServers")
}

func mcpNames(t *testing.T, root map[string]json.RawMessage) map[string]bool {
	t.Helper()
	return namedServers(t, root, "mcp")
}

func namedServers(t *testing.T, root map[string]json.RawMessage, key string) map[string]bool {
	t.Helper()
	raw, ok := root[key]
	if !ok {
		t.Fatalf("%s key missing", key)
	}
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(raw, &servers); err != nil {
		t.Fatalf("parse %s: %v", key, err)
	}
	out := make(map[string]bool, len(servers))
	for name := range servers {
		out[name] = true
	}
	return out
}

func assertNoMcpServersKey(t *testing.T, root map[string]json.RawMessage) {
	t.Helper()
	if _, ok := root["mcpServers"]; ok {
		t.Fatal("mcpServers must not be written into OpenCode config")
	}
}

func TestRemoveServerPreservesClaudeDesktopKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	orig := writeJSONFixture(t, path, map[string]any{
		"preferences": map[string]any{
			"quickEntryShortcut":            "off",
			"coworkScheduledTasksEnabled":   false,
			"coworkWebSearchEnabled":        true,
			"allowAllBrowserActions":        false,
		},
		"coworkUserFilesPath": `C:\Users\chris\.claude\cowork\user-files`,
		"mcpServers": map[string]any{
			"keep-me": map[string]any{
				"command": "npx",
				"args":    []string{"-y", "@scope/keep-me"},
			},
			"drop-me": map[string]any{
				"command": "npx",
				"args":    []string{"-y", "@scope/drop-me"},
			},
		},
	})

	c := Client{
		ID:       ClientClaudeDesktop,
		Name:     "Claude Desktop",
		Path:     path,
		Format:   FormatMcpServers,
		Existing: true,
	}
	if err := RemoveServer(c, "drop-me"); err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(after)*2 < len(orig) {
		t.Fatalf("patched file shrank too much: wrote %d bytes, original %d", len(after), len(orig))
	}

	root := readJSONObject(t, path)
	if _, ok := root["preferences"]; !ok {
		t.Fatal("preferences was dropped")
	}
	if _, ok := root["coworkUserFilesPath"]; !ok {
		t.Fatal("coworkUserFilesPath was dropped")
	}
	names := mcpServerNames(t, root)
	if !names["keep-me"] {
		t.Error("remaining server keep-me is missing")
	}
	if names["drop-me"] {
		t.Error("removed server drop-me is still present")
	}
}

func TestRemoveServerPreservesOpenCodeKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	orig := writeJSONFixture(t, path, map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"model":   "anthropic/claude-sonnet-4-5",
		"provider": map[string]any{
			"anthropic": map[string]any{"options": map[string]any{}},
		},
		"theme": "tron",
		"mcp": map[string]any{
			"keep-me": map[string]any{
				"type":    "local",
				"command": []string{"npx", "-y", "keep"},
				"enabled": true,
			},
			"drop-me": map[string]any{
				"type":    "local",
				"command": []string{"npx", "-y", "drop"},
				"enabled": true,
			},
		},
	})

	c := Client{
		ID:       ClientOpenCode,
		Name:     "OpenCode",
		Path:     path,
		Format:   FormatOpenCode,
		Existing: true,
	}
	if err := RemoveServer(c, "drop-me"); err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(after)*2 < len(orig) {
		t.Fatalf("patched file shrank too much: wrote %d bytes, original %d", len(after), len(orig))
	}

	root := readJSONObject(t, path)
	for _, key := range []string{"$schema", "model", "provider", "theme"} {
		if _, ok := root[key]; !ok {
			t.Errorf("%s was dropped", key)
		}
	}
	assertNoMcpServersKey(t, root)
	names := mcpNames(t, root)
	if !names["keep-me"] {
		t.Error("remaining server keep-me is missing")
	}
	if names["drop-me"] {
		t.Error("removed server drop-me is still present")
	}
}

func TestRemoveServerKeepsOtherKeysWhenLastServerRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	writeJSONFixture(t, path, map[string]any{
		"preferences":         map[string]any{"quickEntryShortcut": "off"},
		"coworkUserFilesPath": "/tmp/cowork",
		"unknownTopLevel":     "must-survive",
		"mcpServers": map[string]any{
			"only-one": map[string]any{"command": "npx", "args": []string{"-y", "gone"}},
		},
	})

	c := Client{Path: path, Format: FormatMcpServers, Existing: true}
	if err := RemoveServer(c, "only-one"); err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}

	root := readJSONObject(t, path)
	for _, key := range []string{"preferences", "coworkUserFilesPath", "unknownTopLevel", "mcpServers"} {
		if _, ok := root[key]; !ok {
			t.Errorf("%s was dropped after removing last server", key)
		}
	}
	names := mcpServerNames(t, root)
	if len(names) != 0 {
		t.Errorf("mcpServers should be empty, got %v", names)
	}
}

func TestMergeServerPreservesTopLevelKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	writeJSONFixture(t, path, map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"model":   "anthropic/claude-sonnet-4-5",
		"provider": map[string]any{
			"anthropic": map[string]any{"options": map[string]any{}},
		},
		"theme": "tron",
	})
	c := Client{
		ID:       ClientOpenCode,
		Name:     "OpenCode",
		Path:     path,
		Format:   FormatOpenCode,
		Existing: true,
	}
	if err := MergeServer(c, "new-server", ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}}); err != nil {
		t.Fatal(err)
	}
	root := readJSONObject(t, path)
	for _, key := range []string{"$schema", "model", "provider", "theme"} {
		if _, ok := root[key]; !ok {
			t.Errorf("MergeServer dropped %s", key)
		}
	}
	assertNoMcpServersKey(t, root)
	names := mcpNames(t, root)
	if !names["new-server"] {
		t.Errorf("servers = %v", names)
	}
}

func TestSafeWriteConfigRejectsTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.json")
	// Substantial original so the size-ratio guard applies (orig > 200).
	padding := make([]byte, 10*1024)
	for i := range padding {
		padding[i] = 'x'
	}
	orig := writeJSONFixture(t, path, map[string]any{
		"preferences": map[string]any{"note": string(padding)},
		"mcpServers": map[string]any{
			"keep-me": map[string]any{"command": "npx"},
		},
	})
	if len(orig) < 10*1024 {
		t.Fatalf("fixture too small to exercise guard: %d", len(orig))
	}

	stub := []byte("{\"mcpServers\":{}}\n")
	if len(stub) > 30 {
		t.Fatalf("stub should be tiny, got %d bytes", len(stub))
	}
	err := SafeWriteConfig(path, stub, FormatMcpServers)
	if err == nil {
		t.Fatal("expected truncation guard to reject 23-byte stub over 10KB file")
	}
	if !strings.Contains(err.Error(), "possible truncation") {
		t.Errorf("error = %q, want possible truncation", err.Error())
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(orig) {
		t.Fatal("original file was modified after rejected write")
	}
}

func TestMergeServerOpenCodeUsesMcpNotMcpServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	writeJSONFixture(t, path, map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"model":   "anthropic/claude-sonnet-4-5",
		"provider": map[string]any{
			"anthropic": map[string]any{"options": map[string]any{}},
		},
		"theme": "dark",
	})
	c := Client{
		ID:       ClientOpenCode,
		Name:     "OpenCode",
		Path:     path,
		Format:   FormatOpenCode,
		Existing: true,
	}
	if err := MergeServer(c, "my-server", ServerConfig{Command: "python3", Args: []string{"server.py"}}); err != nil {
		t.Fatal(err)
	}
	root := readJSONObject(t, path)
	for _, key := range []string{"$schema", "model", "provider", "theme", "mcp"} {
		if _, ok := root[key]; !ok {
			t.Errorf("expected key %s to survive merge", key)
		}
	}
	assertNoMcpServersKey(t, root)
}

func TestRemoveServerOpenCodeLastServerLeavesEmptyMcp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	writeJSONFixture(t, path, map[string]any{
		"$schema":  "https://opencode.ai/config.json",
		"model":    "anthropic/claude-sonnet-4-5",
		"provider": map[string]any{"anthropic": map[string]any{}},
		"theme":    "dark",
		"mcp": map[string]any{
			"only-one": map[string]any{
				"type":    "local",
				"command": []string{"python3", "server.py"},
				"enabled": true,
			},
		},
	})
	c := Client{
		ID:       ClientOpenCode,
		Name:     "OpenCode",
		Path:     path,
		Format:   FormatOpenCode,
		Existing: true,
	}
	if err := RemoveServer(c, "only-one"); err != nil {
		t.Fatal(err)
	}
	root := readJSONObject(t, path)
	for _, key := range []string{"$schema", "model", "provider", "theme", "mcp"} {
		if _, ok := root[key]; !ok {
			t.Errorf("%s was dropped after removing last OpenCode server", key)
		}
	}
	assertNoMcpServersKey(t, root)
	names := mcpNames(t, root)
	if len(names) != 0 {
		t.Errorf("mcp should be empty, got %v", names)
	}
}

func TestRemoveServerMigratesLegacyOpenCodeMcpServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	writeJSONFixture(t, path, map[string]any{
		"model": "anthropic/claude-sonnet-4-5",
		"mcpServers": map[string]any{
			"keep-me": map[string]any{
				"type":    "stdio",
				"command": "npx",
				"args":    []string{"-y", "keep"},
			},
			"drop-me": map[string]any{
				"type":    "stdio",
				"command": "npx",
				"args":    []string{"-y", "drop"},
			},
		},
	})
	c := Client{
		ID:       ClientOpenCode,
		Name:     "OpenCode",
		Path:     path,
		Format:   FormatOpenCode,
		Existing: true,
	}
	if err := RemoveServer(c, "drop-me"); err != nil {
		t.Fatal(err)
	}
	root := readJSONObject(t, path)
	if _, ok := root["model"]; !ok {
		t.Fatal("model was dropped during legacy migration")
	}
	assertNoMcpServersKey(t, root)
	names := mcpNames(t, root)
	if !names["keep-me"] {
		t.Error("legacy leftover keep-me was not migrated under mcp")
	}
	if names["drop-me"] {
		t.Error("removed server drop-me is still present after migration")
	}
}

func TestMergeServerOpenCodeRemoteEntryShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	c := Client{
		ID:     ClientOpenCode,
		Name:   "OpenCode",
		Path:   path,
		Format: FormatOpenCode,
	}
	if err := MergeServer(c, "remote-one", ServerConfig{URL: "https://example.com/mcp"}); err != nil {
		t.Fatal(err)
	}
	root := readJSONObject(t, path)
	assertNoMcpServersKey(t, root)
	raw, ok := root["mcp"]
	if !ok {
		t.Fatal("mcp key missing")
	}
	var servers map[string]map[string]any
	if err := json.Unmarshal(raw, &servers); err != nil {
		t.Fatal(err)
	}
	entry := servers["remote-one"]
	if entry["type"] != "remote" {
		t.Errorf("type = %v, want remote", entry["type"])
	}
	if entry["url"] != "https://example.com/mcp" {
		t.Errorf("url = %v", entry["url"])
	}
	if entry["enabled"] != true {
		t.Errorf("enabled = %v, want true", entry["enabled"])
	}
	if _, has := entry["command"]; has {
		t.Error("remote entry must not include command")
	}
}

func TestMergeServerOpenCodeLocalEntryShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	c := Client{
		ID:     ClientOpenCode,
		Name:   "OpenCode",
		Path:   path,
		Format: FormatOpenCode,
	}
	if err := MergeServer(c, "local-one", ServerConfig{
		Command: "python3",
		Args:    []string{"server.py", "--port", "9"},
		Env:     map[string]string{"FOO": "bar"},
	}); err != nil {
		t.Fatal(err)
	}
	root := readJSONObject(t, path)
	assertNoMcpServersKey(t, root)
	raw, ok := root["mcp"]
	if !ok {
		t.Fatal("mcp key missing")
	}
	var servers map[string]map[string]any
	if err := json.Unmarshal(raw, &servers); err != nil {
		t.Fatal(err)
	}
	entry := servers["local-one"]
	if entry["type"] != "local" {
		t.Errorf("type = %v, want local", entry["type"])
	}
	if entry["enabled"] != true {
		t.Errorf("enabled = %v, want true", entry["enabled"])
	}
	cmd, ok := entry["command"].([]any)
	if !ok {
		t.Fatalf("command = %T %v, want array", entry["command"], entry["command"])
	}
	got := make([]string, 0, len(cmd))
	for _, a := range cmd {
		got = append(got, fmt.Sprint(a))
	}
	want := []string{"python3", "server.py", "--port", "9"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("command = %v, want %v", got, want)
	}
	if _, has := entry["args"]; has {
		t.Error("OpenCode local entry must not use Claude-style args")
	}
	env, ok := entry["env"].([]any)
	if !ok {
		t.Fatalf("env = %T %v, want array of KEY=VALUE", entry["env"], entry["env"])
	}
	if len(env) != 1 || fmt.Sprint(env[0]) != "FOO=bar" {
		t.Errorf("env = %v, want [FOO=bar]", env)
	}
}

func TestMergeServerClaudeStillUsesMcpServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	writeJSONFixture(t, path, map[string]any{
		"preferences": map[string]any{"quickEntryShortcut": "off"},
		"mcpServers": map[string]any{
			"keep-me": map[string]any{"command": "npx"},
		},
	})
	c := Client{
		ID:       ClientClaudeDesktop,
		Name:     "Claude Desktop",
		Path:     path,
		Format:   FormatMcpServers,
		Existing: true,
	}
	if err := MergeServer(c, "new-server", ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}}); err != nil {
		t.Fatal(err)
	}
	root := readJSONObject(t, path)
	if _, ok := root["mcp"]; ok {
		t.Fatal("Claude Desktop must not gain an OpenCode mcp key")
	}
	if _, ok := root["preferences"]; !ok {
		t.Fatal("preferences was dropped")
	}
	names := mcpServerNames(t, root)
	if !names["keep-me"] || !names["new-server"] {
		t.Errorf("servers = %v", names)
	}
}

func claudeServerEntry(t *testing.T, path, name string) map[string]any {
	t.Helper()
	root := readJSONObject(t, path)
	raw, ok := root["mcpServers"]
	if !ok {
		t.Fatal("mcpServers key missing")
	}
	var servers map[string]map[string]any
	if err := json.Unmarshal(raw, &servers); err != nil {
		t.Fatal(err)
	}
	entry, ok := servers[name]
	if !ok {
		t.Fatalf("server %q missing, have %v", name, servers)
	}
	return entry
}

func TestMergeServerClaudeDesktopRemoteIsSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	orig := writeJSONFixture(t, path, map[string]any{
		"preferences": map[string]any{
			"quickEntryShortcut":     "off",
			"coworkWebSearchEnabled": true,
		},
		"coworkUserFilesPath": `C:\Users\chris\.claude\cowork\user-files`,
		"mcpServers": map[string]any{
			"keep-me": map[string]any{"command": "npx", "args": []string{"-y", "keep"}},
		},
	})
	c := Client{
		ID:       ClientClaudeDesktop,
		Name:     "Claude Desktop",
		Path:     path,
		Format:   FormatMcpServers,
		Existing: true,
	}
	err := MergeServer(c, "com.invokera/world-time", ServerConfig{
		URL:  "https://invokera.com/r/world-time",
		Type: "http",
	})
	if err == nil {
		t.Fatal("expected Desktop remote merge to skip, got nil")
	}
	if !IsSkip(err) {
		t.Fatalf("err = %v, want SkipError", err)
	}
	if !strings.Contains(err.Error(), "Connectors") {
		t.Errorf("skip reason = %q, want Connectors", err.Error())
	}

	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(orig) {
		t.Fatal("Desktop config was modified on a remote skip")
	}
	if strings.Contains(string(after), "mcp-remote") {
		t.Fatal("Desktop JSON must not contain mcp-remote")
	}
	if strings.Contains(string(after), "com.invokera/world-time") {
		t.Fatal("Desktop JSON must not gain a remote server key")
	}
	root := readJSONObject(t, path)
	if _, ok := root["preferences"]; !ok {
		t.Fatal("preferences was dropped")
	}
	if _, ok := root["coworkUserFilesPath"]; !ok {
		t.Fatal("coworkUserFilesPath was dropped")
	}
}

func TestMergeServerClaudeDesktopLocalStaysStdio(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	writeJSONFixture(t, path, map[string]any{
		"preferences":         map[string]any{"quickEntryShortcut": "off"},
		"coworkUserFilesPath": "/tmp/cowork",
	})
	c := Client{
		ID:       ClientClaudeDesktop,
		Name:     "Claude Desktop",
		Path:     path,
		Format:   FormatMcpServers,
		Existing: true,
	}
	if err := MergeServer(c, "local-one", ServerConfig{
		Command: "python3",
		Args:    []string{"server.py"},
		Env:     map[string]string{"FOO": "bar"},
	}); err != nil {
		t.Fatal(err)
	}

	root := readJSONObject(t, path)
	if _, ok := root["preferences"]; !ok {
		t.Fatal("preferences was dropped")
	}
	entry := claudeServerEntry(t, path, "local-one")
	if entry["command"] != "python3" {
		t.Errorf("command = %v, want python3", entry["command"])
	}
	args, ok := entry["args"].([]any)
	if !ok {
		t.Fatalf("args = %T %v", entry["args"], entry["args"])
	}
	if len(args) != 1 || fmt.Sprint(args[0]) != "server.py" {
		t.Errorf("args = %v, want [server.py]", args)
	}
	joined := fmt.Sprint(args)
	if strings.Contains(joined, "mcp-remote") {
		t.Error("local Claude entry must not use mcp-remote")
	}
	if _, has := entry["url"]; has {
		t.Error("local Claude entry must not include url")
	}
	if _, has := entry["type"]; has {
		t.Error("local Claude entry must not include type")
	}
	env, ok := entry["env"].(map[string]any)
	if !ok || env["FOO"] != "bar" {
		t.Errorf("env = %v, want FOO=bar", entry["env"])
	}
}

func TestMergeServerCursorRemoteStillUsesURLAndType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	c := Client{
		ID:     ClientCursor,
		Name:   "Cursor",
		Path:   path,
		Format: FormatMcpServers,
	}
	if err := MergeServer(c, "com.invokera/world-time", ServerConfig{
		URL:  "https://invokera.com/r/world-time",
		Type: "http",
	}); err != nil {
		t.Fatal(err)
	}
	root := readJSONObject(t, path)
	raw, ok := root["mcpServers"]
	if !ok {
		t.Fatal("mcpServers key missing")
	}
	var servers map[string]map[string]any
	if err := json.Unmarshal(raw, &servers); err != nil {
		t.Fatal(err)
	}
	entry := servers["com.invokera/world-time"]
	if entry["url"] != "https://invokera.com/r/world-time" {
		t.Errorf("url = %v", entry["url"])
	}
	if entry["type"] != "http" {
		t.Errorf("type = %v, want http", entry["type"])
	}
	if _, has := entry["command"]; has {
		t.Error("Cursor remote must not be rewritten as stdio/mcp-remote")
	}
}

func TestMergeServerClineRemoteKeepsURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cline_mcp_settings.json")
	c := Client{
		ID:     ClientCline,
		Name:   "Cline",
		Path:   path,
		Format: FormatMcpServers,
	}
	if err := MergeServer(c, "com.invokera/world-time", ServerConfig{
		URL:  "https://invokera.com/r/world-time",
		Type: "http",
	}); err != nil {
		t.Fatal(err)
	}
	root := readJSONObject(t, path)
	raw := root["mcpServers"]
	var servers map[string]map[string]any
	if err := json.Unmarshal(raw, &servers); err != nil {
		t.Fatal(err)
	}
	entry := servers["com.invokera/world-time"]
	if entry["url"] != "https://invokera.com/r/world-time" {
		t.Errorf("Cline url = %v, want native url (Cline accepts remotes)", entry["url"])
	}
	if entry["command"] == "npx" {
		t.Error("Cline remotes must not be forced through mcp-remote")
	}
}

func TestClientsByIDReturnsAllCursorPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}

	winRoot := isolateWindowsUsers(t)
	winUser := filepath.Join(winRoot, "chris")
	if err := os.MkdirAll(filepath.Join(winUser, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(winUser, ".cursor", "mcp.json"), []byte(`{"mcpServers":{}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	all := ClientsByID(ClientCursor)
	if len(all) != 2 {
		t.Fatalf("ClientsByID(cursor) = %d, want 2: %+v", len(all), all)
	}
	names := map[string]bool{}
	for _, c := range all {
		names[c.Name] = true
	}
	if !names["Cursor"] || !names["Cursor (Windows via WSL2)"] {
		t.Errorf("names = %v", names)
	}
	first := DetectByID(ClientCursor)
	if first == nil || first.Name != "Cursor" {
		t.Errorf("DetectByID still returns first only, got %+v", first)
	}
}

func TestDetectClaudeCodeRequiresExistingFile(t *testing.T) {
	isolateWindowsUsers(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if DetectByID(ClientClaudeCode) != nil {
		t.Fatal("Claude Code must not be detected from $HOME alone")
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"userID":"u1"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := DetectByID(ClientClaudeCode)
	if c == nil {
		t.Fatal("expected Claude Code once ~/.claude.json exists")
	}
	if c.Name != "Claude Code" {
		t.Errorf("name = %s", c.Name)
	}
}

func TestMergeServerClaudeCodeRemoteHasTypeAndURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	writeJSONFixture(t, path, map[string]any{
		"userID":          "abc",
		"machineID":       "m1",
		"firstStartTime":  1,
		"migrationVersion": 2,
	})
	c := Client{
		ID:       ClientClaudeCode,
		Name:     "Claude Code",
		Path:     path,
		Format:   FormatMcpServers,
		Existing: true,
	}
	if err := MergeServer(c, "com.invokera/world-time", ServerConfig{
		URL:  "https://invokera.com/r/world-time",
		Type: "streamable-http",
	}); err != nil {
		t.Fatal(err)
	}
	root := readJSONObject(t, path)
	for _, key := range []string{"userID", "machineID", "firstStartTime", "migrationVersion"} {
		if _, ok := root[key]; !ok {
			t.Errorf("Claude Code merge dropped %s", key)
		}
	}
	raw := root["mcpServers"]
	var servers map[string]map[string]any
	if err := json.Unmarshal(raw, &servers); err != nil {
		t.Fatal(err)
	}
	entry := servers["com.invokera/world-time"]
	if entry["type"] != "http" {
		t.Errorf("type = %v, want http (streamable-http mapped)", entry["type"])
	}
	if entry["url"] != "https://invokera.com/r/world-time" {
		t.Errorf("url = %v", entry["url"])
	}
	if _, has := entry["command"]; has {
		t.Error("Claude Code remote must not include command")
	}
}

func TestMergeServerClaudeCodeStdioIncludesType(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	c := Client{
		ID:     ClientClaudeCode,
		Name:   "Claude Code",
		Path:   path,
		Format: FormatMcpServers,
	}
	if err := MergeServer(c, "local-one", ServerConfig{
		Command: "python3",
		Args:    []string{"server.py"},
		Env:     map[string]string{"FOO": "bar"},
	}); err != nil {
		t.Fatal(err)
	}
	root := readJSONObject(t, path)
	var servers map[string]map[string]any
	if err := json.Unmarshal(root["mcpServers"], &servers); err != nil {
		t.Fatal(err)
	}
	entry := servers["local-one"]
	if entry["type"] != "stdio" {
		t.Errorf("type = %v, want stdio", entry["type"])
	}
	if entry["command"] != "python3" {
		t.Errorf("command = %v", entry["command"])
	}
	if _, has := entry["url"]; has {
		t.Error("stdio Code entry must not include url")
	}
}

func TestMergeServerClaudeCodeRemoteSSEAndWS(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	c := Client{ID: ClientClaudeCode, Path: path, Format: FormatMcpServers}
	if err := MergeServer(c, "sse-one", ServerConfig{URL: "https://ex/sse", Type: "sse"}); err != nil {
		t.Fatal(err)
	}
	if err := MergeServer(c, "ws-one", ServerConfig{URL: "wss://ex/ws", Type: "ws"}); err != nil {
		t.Fatal(err)
	}
	root := readJSONObject(t, path)
	var servers map[string]map[string]any
	if err := json.Unmarshal(root["mcpServers"], &servers); err != nil {
		t.Fatal(err)
	}
	if servers["sse-one"]["type"] != "sse" {
		t.Errorf("sse type = %v", servers["sse-one"]["type"])
	}
	if servers["ws-one"]["type"] != "ws" {
		t.Errorf("ws type = %v", servers["ws-one"]["type"])
	}
}

func TestMergeServerCursorKeepsExistingDocker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	writeJSONFixture(t, path, map[string]any{
		"mcpServers": map[string]any{
			"MCP_DOCKER": map[string]any{
				"command": "docker",
				"args":    []string{"mcp", "gateway", "run"},
			},
		},
	})
	c := Client{
		ID:       ClientCursor,
		Name:     "Cursor",
		Path:     path,
		Format:   FormatMcpServers,
		Existing: true,
	}
	if err := MergeServer(c, "com.invokera/world-time", ServerConfig{
		URL:  "https://invokera.com/r/world-time",
		Type: "http",
	}); err != nil {
		t.Fatal(err)
	}
	root := readJSONObject(t, path)
	var servers map[string]map[string]any
	if err := json.Unmarshal(root["mcpServers"], &servers); err != nil {
		t.Fatal(err)
	}
	if _, ok := servers["MCP_DOCKER"]; !ok {
		t.Fatal("MCP_DOCKER was clobbered")
	}
	entry := servers["com.invokera/world-time"]
	if entry["type"] != "http" || entry["url"] != "https://invokera.com/r/world-time" {
		t.Errorf("world-time = %v", entry)
	}
}

func TestRemoveServerHitsEveryCursorPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	linuxPath := filepath.Join(home, ".cursor", "mcp.json")
	writeJSONFixture(t, linuxPath, map[string]any{
		"mcpServers": map[string]any{
			"com.invokera/world-time": map[string]any{"type": "http", "url": "https://invokera.com/r/world-time"},
			"keep-me":                 map[string]any{"command": "npx"},
		},
	})

	winRoot := isolateWindowsUsers(t)
	winPath := filepath.Join(winRoot, "chris", ".cursor", "mcp.json")
	writeJSONFixture(t, winPath, map[string]any{
		"mcpServers": map[string]any{
			"com.invokera/world-time": map[string]any{"type": "http", "url": "https://invokera.com/r/world-time"},
			"MCP_DOCKER":              map[string]any{"command": "docker", "args": []string{"mcp", "gateway", "run"}},
		},
	})

	removed := 0
	for _, c := range Detect() {
		if c.ID != ClientCursor || !c.Existing {
			continue
		}
		if err := RemoveServer(c, "com.invokera/world-time"); err != nil {
			t.Fatalf("RemoveServer %s: %v", c.Name, err)
		}
		removed++
	}
	if removed != 2 {
		t.Fatalf("removed from %d cursor paths, want 2", removed)
	}

	linux := readJSONObject(t, linuxPath)
	linuxNames := mcpServerNames(t, linux)
	if linuxNames["com.invokera/world-time"] {
		t.Error("linux cursor still has world-time")
	}
	if !linuxNames["keep-me"] {
		t.Error("linux cursor lost keep-me")
	}

	win := readJSONObject(t, winPath)
	winNames := mcpServerNames(t, win)
	if winNames["com.invokera/world-time"] {
		t.Error("windows cursor still has world-time")
	}
	if !winNames["MCP_DOCKER"] {
		t.Error("windows cursor lost MCP_DOCKER")
	}
}
