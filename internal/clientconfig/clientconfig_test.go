package clientconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
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
		case FormatMcpServers, FormatArray, FormatOpenCode, FormatHermes, FormatTOML, FormatZed, FormatAider:
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
			"quickEntryShortcut":          "off",
			"coworkScheduledTasksEnabled": false,
			"coworkWebSearchEnabled":      true,
			"allowAllBrowserActions":      false,
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
		"userID":           "abc",
		"machineID":        "m1",
		"firstStartTime":   1,
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

func TestClientsByIDReturnsAllVSCodePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".copilot"), 0o755); err != nil {
		t.Fatal(err)
	}

	winRoot := isolateWindowsUsers(t)
	winUser := filepath.Join(winRoot, "chris")
	if err := os.MkdirAll(filepath.Join(winUser, ".copilot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(winUser, ".copilot", "mcp-config.json"), []byte(`{"mcpServers":{}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	all := ClientsByID(ClientVSCode)
	if len(all) != 2 {
		t.Fatalf("ClientsByID(vscode) = %d, want 2: %+v", len(all), all)
	}
	names := map[string]bool{}
	for _, c := range all {
		names[c.Name] = true
	}
	if !names["VS Code (GitHub Copilot)"] || !names["VS Code (GitHub Copilot) (Windows via WSL2)"] {
		t.Errorf("names = %v", names)
	}
}

func TestRemoveServerHitsEveryVSCodePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	linuxPath := filepath.Join(home, ".copilot", "mcp-config.json")
	writeJSONFixture(t, linuxPath, map[string]any{
		"mcpServers": map[string]any{
			"com.invokera/world-time": map[string]any{"type": "http", "url": "https://invokera.com/r/world-time"},
			"keep-me":                 map[string]any{"command": "npx"},
		},
	})

	winRoot := isolateWindowsUsers(t)
	winPath := filepath.Join(winRoot, "chris", ".copilot", "mcp-config.json")
	writeJSONFixture(t, winPath, map[string]any{
		"mcpServers": map[string]any{
			"com.invokera/world-time": map[string]any{"type": "http", "url": "https://invokera.com/r/world-time"},
			"MCP_DOCKER":              map[string]any{"command": "docker", "args": []string{"mcp", "gateway", "run"}},
		},
	})

	removed := 0
	for _, c := range Detect() {
		if c.ID != ClientVSCode || !c.Existing {
			continue
		}
		if err := RemoveServer(c, "com.invokera/world-time"); err != nil {
			t.Fatalf("RemoveServer %s: %v", c.Name, err)
		}
		removed++
	}
	if removed != 2 {
		t.Fatalf("removed from %d vscode paths, want 2", removed)
	}

	linux := readJSONObject(t, linuxPath)
	linuxNames := mcpServerNames(t, linux)
	if linuxNames["com.invokera/world-time"] {
		t.Error("linux vscode still has world-time")
	}
	if !linuxNames["keep-me"] {
		t.Error("linux vscode lost keep-me")
	}

	win := readJSONObject(t, winPath)
	winNames := mcpServerNames(t, win)
	if winNames["com.invokera/world-time"] {
		t.Error("windows vscode still has world-time")
	}
	if !winNames["MCP_DOCKER"] {
		t.Error("windows vscode lost MCP_DOCKER")
	}
}

func TestClientsByIDReturnsAllGeminiPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o755); err != nil {
		t.Fatal(err)
	}

	winRoot := isolateWindowsUsers(t)
	winUser := filepath.Join(winRoot, "chris")
	if err := os.MkdirAll(filepath.Join(winUser, ".gemini"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(winUser, ".gemini", "settings.json"), []byte(`{"mcpServers":{}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	all := ClientsByID(ClientGemini)
	if len(all) != 2 {
		t.Fatalf("ClientsByID(gemini) = %d, want 2: %+v", len(all), all)
	}
}

func TestClientsByIDReturnsAllAmazonQPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".aws", "amazonq"), 0o755); err != nil {
		t.Fatal(err)
	}

	winRoot := isolateWindowsUsers(t)
	winUser := filepath.Join(winRoot, "chris")
	if err := os.MkdirAll(filepath.Join(winUser, ".aws", "amazonq"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(winUser, ".aws", "amazonq", "mcp.json"), []byte(`{"mcpServers":{}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	all := ClientsByID(ClientAmazonQ)
	if len(all) != 2 {
		t.Fatalf("ClientsByID(amazonq) = %d, want 2: %+v", len(all), all)
	}
}

func TestClientsByIDReturnsAllWindsurfPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".codeium", "windsurf"), 0o755); err != nil {
		t.Fatal(err)
	}

	winRoot := isolateWindowsUsers(t)
	winUser := filepath.Join(winRoot, "chris")
	winDir := filepath.Join(winUser, "AppData", "Roaming", "Codeium", "windsurf")
	if err := os.MkdirAll(winDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(winDir, "mcp_config.json"), []byte(`{"mcpServers":{}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	all := ClientsByID(ClientWindsurf)
	if len(all) != 2 {
		t.Fatalf("ClientsByID(windsurf) = %d, want 2: %+v", len(all), all)
	}
}

func TestClientsByIDReturnsAllRooCodePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	linuxDir := filepath.Join(home, ".config", "Code", "User", "globalStorage", "rooveterinaryinc.roo-cline", "settings")
	if err := os.MkdirAll(linuxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linuxDir, "roo_mcp_settings.json"), []byte(`{"mcpServers":{}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	winRoot := isolateWindowsUsers(t)
	winDir := filepath.Join(winRoot, "chris", "AppData", "Roaming", "Code", "User", "globalStorage", "rooveterinaryinc.roo-cline", "settings")
	if err := os.MkdirAll(winDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(winDir, "roo_mcp_settings.json"), []byte(`{"mcpServers":{}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	all := ClientsByID(ClientRooCode)
	if len(all) != 2 {
		t.Fatalf("ClientsByID(roo-code) = %d, want 2: %+v", len(all), all)
	}
	names := map[string]bool{}
	for _, c := range all {
		names[c.Name] = true
	}
	if !names["Roo Code"] || !names["Roo Code (Windows via WSL2)"] {
		t.Errorf("names = %v", names)
	}
}

func TestNewClientMergeRemove(t *testing.T) {
	isolateWindowsUsers(t)

	clients := []struct {
		id   ClientID
		name string
		path string
	}{
		{ClientVSCode, "VS Code", "copilot/mcp-config.json"},
		{ClientWindsurf, "Windsurf", "windsurf/mcp_config.json"},
		{ClientGemini, "Gemini CLI", "gemini/settings.json"},
		{ClientAmazonQ, "Amazon Q Developer", "amazonq/mcp.json"},
		{ClientRooCode, "Roo Code", "roo/roo_mcp_settings.json"},
	}

	for _, tc := range clients {
		t.Run(string(tc.id), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tc.path)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			orig := writeJSONFixture(t, path, map[string]any{
				"version": "1.0",
				"mcpServers": map[string]any{
					"existing": map[string]any{"command": "foo"},
				},
			})

			c := Client{
				ID:       tc.id,
				Name:     tc.name,
				Path:     path,
				Format:   FormatMcpServers,
				Existing: true,
			}

			// 1. Merge a stdio server
			stdioServer := ServerConfig{
				Command: "npx",
				Args:    []string{"-y", "@scope/pkg"},
				Env:     map[string]string{"FOO": "bar"},
			}
			if err := MergeServer(c, "my-stdio", stdioServer); err != nil {
				t.Fatalf("MergeServer stdio: %v", err)
			}

			root := readJSONObject(t, path)

			// non-mcpServers keys preserved
			if _, ok := root["version"]; !ok {
				t.Error("version key was dropped after stdio merge")
			}

			names := mcpServerNames(t, root)
			if !names["existing"] {
				t.Error("existing server was clobbered by stdio merge")
			}
			if !names["my-stdio"] {
				t.Error("my-stdio was not written")
			}

			// verify stdio entry content
			var servers map[string]map[string]any
			if err := json.Unmarshal(root["mcpServers"], &servers); err != nil {
				t.Fatal(err)
			}
			stdioEntry := servers["my-stdio"]
			if stdioEntry["command"] != "npx" {
				t.Errorf("stdio command = %v, want npx", stdioEntry["command"])
			}

			// 2. Merge a remote http server
			remoteServer := ServerConfig{URL: "https://example.com/mcp"}
			if err := MergeServer(c, "my-remote", remoteServer); err != nil {
				t.Fatalf("MergeServer remote: %v", err)
			}

			root = readJSONObject(t, path)
			if _, ok := root["version"]; !ok {
				t.Error("version key was dropped after remote merge")
			}

			if err := json.Unmarshal(root["mcpServers"], &servers); err != nil {
				t.Fatal(err)
			}
			names = mcpServerNames(t, root)
			if !names["existing"] {
				t.Error("existing server was clobbered by remote merge")
			}
			if !names["my-stdio"] {
				t.Error("my-stdio was lost after remote merge")
			}
			if !names["my-remote"] {
				t.Error("my-remote was not written")
			}

			remoteEntry := servers["my-remote"]
			if remoteEntry["url"] != "https://example.com/mcp" {
				t.Errorf("remote url = %v, want https://example.com/mcp", remoteEntry["url"])
			}
			expectedType := "http"
			if tc.id == ClientRooCode {
				expectedType = ""
			}
			if expectedType != "" {
				if remoteEntry["type"] != expectedType {
					t.Errorf("remote type = %v, want %s", remoteEntry["type"], expectedType)
				}
			}

			// 3. Remove the remote server
			if err := RemoveServer(c, "my-remote"); err != nil {
				t.Fatalf("RemoveServer: %v", err)
			}

			root = readJSONObject(t, path)
			if _, ok := root["version"]; !ok {
				t.Error("version key was dropped after remove")
			}
			names = mcpServerNames(t, root)
			if names["my-remote"] {
				t.Error("my-remote was not removed")
			}
			if !names["existing"] {
				t.Error("existing server was lost after remove")
			}
			if !names["my-stdio"] {
				t.Error("my-stdio was lost after remove")
			}

			// file didn't shrink too much
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(after)*2 < len(orig) {
				t.Fatalf("file shrank too much: %d -> %d", len(orig), len(after))
			}
		})
	}
}

// readTOMLRoot parses a TOML file into a map for test assertions.
func readTOMLRoot(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := toml.Unmarshal(data, &root); err != nil {
		t.Fatalf("parse TOML %s: %v\n%s", path, err, data)
	}
	return root
}

// readYAMLRoot parses a YAML file into a map for test assertions.
func readYAMLRoot(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("parse YAML %s: %v\n%s", path, err, data)
	}
	return root
}

// contextServerNames returns the set of server names under the
// "context_servers" key in a Zed settings.json root.
func contextServerNames(t *testing.T, root map[string]json.RawMessage) map[string]bool {
	t.Helper()
	return namedServers(t, root, "context_servers")
}

// writeTOMLFixture writes a TOML string to a file, creating parent dirs.
func writeTOMLFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeYAMLFixture writes a YAML string to a file, creating parent dirs.
func writeYAMLFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Codex CLI tests (TOML)
// ---------------------------------------------------------------------------

func TestClientsByIDReturnsAllCodexPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}

	winRoot := isolateWindowsUsers(t)
	winUser := filepath.Join(winRoot, "chris")
	if err := os.MkdirAll(filepath.Join(winUser, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(winUser, ".codex", "config.toml"), []byte("# codex\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	all := ClientsByID(ClientCodex)
	if len(all) != 2 {
		t.Fatalf("ClientsByID(codex) = %d, want 2: %+v", len(all), all)
	}
	names := map[string]bool{}
	for _, c := range all {
		names[c.Name] = true
	}
	if !names["Codex CLI"] || !names["Codex CLI (Windows via WSL2)"] {
		t.Errorf("names = %v", names)
	}
}

func TestCodexMergeStdioPreservesExistingKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeTOMLFixture(t, path, `model = "o4-mini"
model_providers = []

[mcp_servers.existing]
command = "node"
args = ["old.js"]
`)
	c := Client{
		ID:       ClientCodex,
		Name:     "Codex CLI",
		Path:     path,
		Format:   FormatTOML,
		Existing: true,
	}
	server := ServerConfig{
		Command: "npx",
		Args:    []string{"-y", "mcp-server"},
		Env:     map[string]string{"API_KEY": "secret"},
	}
	if err := MergeServer(c, "my-server", server); err != nil {
		t.Fatalf("MergeServer: %v", err)
	}

	root := readTOMLRoot(t, path)
	if root["model"] != "o4-mini" {
		t.Errorf("model = %v, want o4-mini", root["model"])
	}
	if _, ok := root["model_providers"]; !ok {
		t.Error("model_providers was dropped")
	}
	servers, _ := root["mcp_servers"].(map[string]any)
	if servers == nil {
		t.Fatal("mcp_servers missing")
	}
	if _, ok := servers["existing"]; !ok {
		t.Error("existing server was clobbered")
	}
	if _, ok := servers["my-server"]; !ok {
		t.Error("my-server not written")
	}
	entry, _ := servers["my-server"].(map[string]any)
	if entry == nil {
		t.Fatal("my-server entry is nil")
	}
	if entry["command"] != "npx" {
		t.Errorf("command = %v, want npx", entry["command"])
	}
}

func TestCodexMergeRemoteWritesURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	c := Client{
		ID:     ClientCodex,
		Name:   "Codex CLI",
		Path:   path,
		Format: FormatTOML,
	}
	server := ServerConfig{URL: "https://example.com/mcp"}
	if err := MergeServer(c, "my-remote", server); err != nil {
		t.Fatalf("MergeServer: %v", err)
	}
	root := readTOMLRoot(t, path)
	servers, _ := root["mcp_servers"].(map[string]any)
	if servers == nil {
		t.Fatal("mcp_servers missing")
	}
	entry, _ := servers["my-remote"].(map[string]any)
	if entry == nil {
		t.Fatal("my-remote entry is nil")
	}
	if entry["url"] != "https://example.com/mcp" {
		t.Errorf("url = %v, want https://example.com/mcp", entry["url"])
	}
	if _, has := entry["command"]; has {
		t.Error("remote entry must not include command")
	}
}

func TestCodexRemovePreservesOtherData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeTOMLFixture(t, path, `model = "o4-mini"

[mcp_servers.keep-me]
command = "node"

[mcp_servers.drop-me]
command = "python3"
`)
	c := Client{
		ID:       ClientCodex,
		Name:     "Codex CLI",
		Path:     path,
		Format:   FormatTOML,
		Existing: true,
	}
	if err := RemoveServer(c, "drop-me"); err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}
	root := readTOMLRoot(t, path)
	if root["model"] != "o4-mini" {
		t.Error("model was dropped")
	}
	servers, _ := root["mcp_servers"].(map[string]any)
	if _, ok := servers["keep-me"]; !ok {
		t.Error("keep-me was lost")
	}
	if _, ok := servers["drop-me"]; ok {
		t.Error("drop-me was not removed")
	}
}

func TestCodexRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	c := Client{
		ID:     ClientCodex,
		Name:   "Codex CLI",
		Path:   path,
		Format: FormatTOML,
	}
	server := ServerConfig{
		Command: "npx",
		Args:    []string{"-y", "mcp-server"},
		Env:     map[string]string{"FOO": "bar"},
	}
	if err := MergeServer(c, "round-trip", server); err != nil {
		t.Fatal(err)
	}
	servers, err := ReadServersFormat(path, FormatTOML)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := servers["round-trip"]
	if !ok {
		t.Fatal("round-trip server not found after read")
	}
	var entry map[string]any
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatal(err)
	}
	if entry["command"] != "npx" {
		t.Errorf("command = %v, want npx", entry["command"])
	}
}

// ---------------------------------------------------------------------------
// Grok Build tests (TOML, headers for remotes)
// ---------------------------------------------------------------------------

func TestClientsByIDReturnsAllGrokPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".grok"), 0o755); err != nil {
		t.Fatal(err)
	}

	winRoot := isolateWindowsUsers(t)
	winUser := filepath.Join(winRoot, "chris")
	if err := os.MkdirAll(filepath.Join(winUser, ".grok"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(winUser, ".grok", "config.toml"), []byte("# grok\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	all := ClientsByID(ClientGrok)
	if len(all) != 2 {
		t.Fatalf("ClientsByID(grok) = %d, want 2: %+v", len(all), all)
	}
}

func TestGrokMergeStdioPreservesExistingKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeTOMLFixture(t, path, `model = "grok-3"

[mcp_servers.existing]
command = "node"
`)
	c := Client{
		ID:       ClientGrok,
		Name:     "Grok Build",
		Path:     path,
		Format:   FormatTOML,
		Existing: true,
	}
	server := ServerConfig{
		Command: "npx",
		Args:    []string{"-y", "mcp-server"},
		Env:     map[string]string{"API_KEY": "value"},
	}
	if err := MergeServer(c, "my-server", server); err != nil {
		t.Fatalf("MergeServer: %v", err)
	}
	root := readTOMLRoot(t, path)
	if root["model"] != "grok-3" {
		t.Errorf("model = %v, want grok-3", root["model"])
	}
	servers, _ := root["mcp_servers"].(map[string]any)
	if _, ok := servers["existing"]; !ok {
		t.Error("existing server was clobbered")
	}
	entry, _ := servers["my-server"].(map[string]any)
	if entry["command"] != "npx" {
		t.Errorf("command = %v, want npx", entry["command"])
	}
}

func TestGrokMergeRemoteUsesHeaders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	c := Client{
		ID:     ClientGrok,
		Name:   "Grok Build",
		Path:   path,
		Format: FormatTOML,
	}
	server := ServerConfig{
		URL: "https://example.com/mcp",
		Env: map[string]string{"Authorization": "Bearer token"},
	}
	if err := MergeServer(c, "my-remote", server); err != nil {
		t.Fatalf("MergeServer: %v", err)
	}
	root := readTOMLRoot(t, path)
	servers, _ := root["mcp_servers"].(map[string]any)
	entry, _ := servers["my-remote"].(map[string]any)
	if entry == nil {
		t.Fatal("my-remote entry is nil")
	}
	if entry["url"] != "https://example.com/mcp" {
		t.Errorf("url = %v", entry["url"])
	}
	headers, _ := entry["headers"].(map[string]any)
	if headers == nil {
		t.Fatal("headers missing from Grok remote entry")
	}
	if headers["Authorization"] != "Bearer token" {
		t.Errorf("Authorization = %v, want 'Bearer token'", headers["Authorization"])
	}
}

func TestGrokRemovePreservesOtherData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeTOMLFixture(t, path, `model = "grok-3"

[mcp_servers.keep-me]
command = "node"

[mcp_servers.drop-me]
command = "python3"
`)
	c := Client{
		ID:       ClientGrok,
		Name:     "Grok Build",
		Path:     path,
		Format:   FormatTOML,
		Existing: true,
	}
	if err := RemoveServer(c, "drop-me"); err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}
	root := readTOMLRoot(t, path)
	if root["model"] != "grok-3" {
		t.Error("model was dropped")
	}
	servers, _ := root["mcp_servers"].(map[string]any)
	if _, ok := servers["keep-me"]; !ok {
		t.Error("keep-me was lost")
	}
	if _, ok := servers["drop-me"]; ok {
		t.Error("drop-me was not removed")
	}
}

// ---------------------------------------------------------------------------
// Zed tests (JSON, context_servers key)
// ---------------------------------------------------------------------------

func TestClientsByIDReturnsAllZedPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".config", "zed"), 0o755); err != nil {
		t.Fatal(err)
	}

	winRoot := isolateWindowsUsers(t)
	winDir := filepath.Join(winRoot, "chris", "AppData", "Roaming", "Zed")
	if err := os.MkdirAll(winDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(winDir, "settings.json"), []byte(`{}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	all := ClientsByID(ClientZed)
	if len(all) != 2 {
		t.Fatalf("ClientsByID(zed) = %d, want 2: %+v", len(all), all)
	}
	names := map[string]bool{}
	for _, c := range all {
		names[c.Name] = true
	}
	if !names["Zed"] || !names["Zed (Windows via WSL2)"] {
		t.Errorf("names = %v", names)
	}
}

func TestZedMergeStdioPreservesExistingKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeJSONFixture(t, path, map[string]any{
		"theme":            "monokai",
		"buffer_font_size": 14,
		"context_servers": map[string]any{
			"existing": map[string]any{"command": "node"},
		},
	})
	c := Client{
		ID:       ClientZed,
		Name:     "Zed",
		Path:     path,
		Format:   FormatZed,
		Existing: true,
	}
	server := ServerConfig{
		Command: "npx",
		Args:    []string{"-y", "mcp-server"},
		Env:     map[string]string{"FOO": "bar"},
	}
	if err := MergeServer(c, "my-server", server); err != nil {
		t.Fatalf("MergeServer: %v", err)
	}
	root := readJSONObject(t, path)
	if _, ok := root["theme"]; !ok {
		t.Error("theme was dropped")
	}
	if _, ok := root["buffer_font_size"]; !ok {
		t.Error("buffer_font_size was dropped")
	}
	names := contextServerNames(t, root)
	if !names["existing"] {
		t.Error("existing server was clobbered")
	}
	if !names["my-server"] {
		t.Error("my-server not written")
	}
	var servers map[string]map[string]any
	if err := json.Unmarshal(root["context_servers"], &servers); err != nil {
		t.Fatal(err)
	}
	entry := servers["my-server"]
	if entry["command"] != "npx" {
		t.Errorf("command = %v, want npx", entry["command"])
	}
}

func TestZedMergeRemoteWritesURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	c := Client{
		ID:     ClientZed,
		Name:   "Zed",
		Path:   path,
		Format: FormatZed,
	}
	server := ServerConfig{URL: "https://example.com/mcp"}
	if err := MergeServer(c, "my-remote", server); err != nil {
		t.Fatalf("MergeServer: %v", err)
	}
	root := readJSONObject(t, path)
	var servers map[string]map[string]any
	if err := json.Unmarshal(root["context_servers"], &servers); err != nil {
		t.Fatal(err)
	}
	entry := servers["my-remote"]
	if entry["url"] != "https://example.com/mcp" {
		t.Errorf("url = %v, want https://example.com/mcp", entry["url"])
	}
	if _, has := entry["command"]; has {
		t.Error("remote entry must not include command")
	}
}

func TestZedRemovePreservesOtherData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	writeJSONFixture(t, path, map[string]any{
		"theme": "monokai",
		"context_servers": map[string]any{
			"keep-me": map[string]any{"command": "node"},
			"drop-me": map[string]any{"command": "python3"},
		},
	})
	c := Client{
		ID:       ClientZed,
		Name:     "Zed",
		Path:     path,
		Format:   FormatZed,
		Existing: true,
	}
	if err := RemoveServer(c, "drop-me"); err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}
	root := readJSONObject(t, path)
	if _, ok := root["theme"]; !ok {
		t.Error("theme was dropped")
	}
	names := contextServerNames(t, root)
	if !names["keep-me"] {
		t.Error("keep-me was lost")
	}
	if names["drop-me"] {
		t.Error("drop-me was not removed")
	}
}

func TestZedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	c := Client{
		ID:     ClientZed,
		Name:   "Zed",
		Path:   path,
		Format: FormatZed,
	}
	server := ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}}
	if err := MergeServer(c, "rt", server); err != nil {
		t.Fatal(err)
	}
	servers, err := ReadServersFormat(path, FormatZed)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := servers["rt"]; !ok {
		t.Fatal("rt server not found")
	}
}

// ---------------------------------------------------------------------------
// Aider tests (YAML, mcp-servers list, stdio only)
// ---------------------------------------------------------------------------

func TestClientsByIDReturnsAllAiderPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Aider config file is at ~/.aider.conf.yml — parent is $HOME which
	// always exists. But Detect() requires the file itself to exist.
	if err := os.WriteFile(filepath.Join(home, ".aider.conf.yml"), []byte("# aider\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	winRoot := isolateWindowsUsers(t)
	winUser := filepath.Join(winRoot, "chris")
	if err := os.MkdirAll(winUser, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(winUser, ".aider.conf.yml"), []byte("# aider\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	all := ClientsByID(ClientAider)
	if len(all) != 2 {
		t.Fatalf("ClientsByID(aider) = %d, want 2: %+v", len(all), all)
	}
}

func TestAiderMergeStdioPreservesExistingKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".aider.conf.yml")
	writeYAMLFixture(t, path, `model: gpt-4
auto_commits: true
mcp-servers:
  - name: existing
    command: node
    args:
      - old.js
`)
	c := Client{
		ID:       ClientAider,
		Name:     "Aider",
		Path:     path,
		Format:   FormatAider,
		Existing: true,
	}
	server := ServerConfig{
		Command: "npx",
		Args:    []string{"-y", "mcp-server"},
		Env:     map[string]string{"API_KEY": "value"},
	}
	if err := MergeServer(c, "my-server", server); err != nil {
		t.Fatalf("MergeServer: %v", err)
	}
	root := readYAMLRoot(t, path)
	if root["model"] != "gpt-4" {
		t.Errorf("model = %v, want gpt-4", root["model"])
	}
	if root["auto_commits"] != true {
		t.Error("auto_commits was dropped or changed")
	}
	rawList, _ := root["mcp-servers"].([]any)
	if rawList == nil {
		t.Fatal("mcp-servers list missing")
	}
	names := make(map[string]bool)
	for _, item := range rawList {
		m, _ := item.(map[string]any)
		if n, _ := m["name"].(string); n != "" {
			names[n] = true
		}
	}
	if !names["existing"] {
		t.Error("existing server was clobbered")
	}
	if !names["my-server"] {
		t.Error("my-server not written")
	}
}

func TestAiderMergeRemoteIsSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".aider.conf.yml")
	writeYAMLFixture(t, path, `model: gpt-4
mcp-servers: []
`)
	c := Client{
		ID:       ClientAider,
		Name:     "Aider",
		Path:     path,
		Format:   FormatAider,
		Existing: true,
	}
	server := ServerConfig{URL: "https://example.com/mcp"}
	err := MergeServer(c, "my-remote", server)
	if err == nil {
		t.Fatal("expected Aider remote merge to skip, got nil")
	}
	if !IsSkip(err) {
		t.Fatalf("err = %v, want SkipError", err)
	}
	if !strings.Contains(err.Error(), "stdio") {
		t.Errorf("skip reason = %q, want stdio mention", err.Error())
	}
	root := readYAMLRoot(t, path)
	if root["model"] != "gpt-4" {
		t.Error("model was modified during skip")
	}
}

func TestAiderRemovePreservesOtherData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".aider.conf.yml")
	writeYAMLFixture(t, path, `model: gpt-4
auto_commits: false
mcp-servers:
  - name: keep-me
    command: node
  - name: drop-me
    command: python3
`)
	c := Client{
		ID:       ClientAider,
		Name:     "Aider",
		Path:     path,
		Format:   FormatAider,
		Existing: true,
	}
	if err := RemoveServer(c, "drop-me"); err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}
	root := readYAMLRoot(t, path)
	if root["model"] != "gpt-4" {
		t.Error("model was dropped")
	}
	if root["auto_commits"] != false {
		t.Error("auto_commits was dropped")
	}
	rawList, _ := root["mcp-servers"].([]any)
	names := make(map[string]bool)
	for _, item := range rawList {
		m, _ := item.(map[string]any)
		if n, _ := m["name"].(string); n != "" {
			names[n] = true
		}
	}
	if !names["keep-me"] {
		t.Error("keep-me was lost")
	}
	if names["drop-me"] {
		t.Error("drop-me was not removed")
	}
}

func TestAiderRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".aider.conf.yml")
	c := Client{
		ID:     ClientAider,
		Name:   "Aider",
		Path:   path,
		Format: FormatAider,
	}
	server := ServerConfig{
		Command: "npx",
		Args:    []string{"-y", "mcp-server"},
		Env:     map[string]string{"FOO": "bar"},
	}
	if err := MergeServer(c, "round-trip", server); err != nil {
		t.Fatal(err)
	}
	servers, err := ReadServersFormat(path, FormatAider)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := servers["round-trip"]
	if !ok {
		t.Fatal("round-trip server not found")
	}
	var entry map[string]any
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatal(err)
	}
	if entry["command"] != "npx" {
		t.Errorf("command = %v, want npx", entry["command"])
	}
	if entry["name"] != "round-trip" {
		t.Errorf("name = %v, want round-trip", entry["name"])
	}
}

// ---------------------------------------------------------------------------
// Combined new-client merge/remove subtests
// ---------------------------------------------------------------------------

func TestNewClientMergeRemoveCodexGrokZedAider(t *testing.T) {
	isolateWindowsUsers(t)

	tomlClients := []struct {
		id   ClientID
		name string
	}{
		{ClientCodex, "Codex CLI"},
		{ClientGrok, "Grok Build"},
	}

	for _, tc := range tomlClients {
		t.Run(string(tc.id), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			writeTOMLFixture(t, path, `model = "test-model"

[mcp_servers.existing]
command = "foo"
`)
			c := Client{
				ID:       tc.id,
				Name:     tc.name,
				Path:     path,
				Format:   FormatTOML,
				Existing: true,
			}

			// 1. Merge stdio
			stdioServer := ServerConfig{
				Command: "npx",
				Args:    []string{"-y", "pkg"},
				Env:     map[string]string{"FOO": "bar"},
			}
			if err := MergeServer(c, "my-stdio", stdioServer); err != nil {
				t.Fatalf("MergeServer stdio: %v", err)
			}
			root := readTOMLRoot(t, path)
			if root["model"] != "test-model" {
				t.Error("model was dropped after stdio merge")
			}
			servers, _ := root["mcp_servers"].(map[string]any)
			if _, ok := servers["existing"]; !ok {
				t.Error("existing server was clobbered")
			}
			if _, ok := servers["my-stdio"]; !ok {
				t.Error("my-stdio was not written")
			}

			// 2. Merge remote
			remoteServer := ServerConfig{URL: "https://example.com/mcp"}
			if err := MergeServer(c, "my-remote", remoteServer); err != nil {
				t.Fatalf("MergeServer remote: %v", err)
			}
			root = readTOMLRoot(t, path)
			if root["model"] != "test-model" {
				t.Error("model was dropped after remote merge")
			}
			servers, _ = root["mcp_servers"].(map[string]any)
			if _, ok := servers["existing"]; !ok {
				t.Error("existing server was clobbered by remote merge")
			}
			if _, ok := servers["my-stdio"]; !ok {
				t.Error("my-stdio was lost after remote merge")
			}
			if _, ok := servers["my-remote"]; !ok {
				t.Error("my-remote was not written")
			}
			entry, _ := servers["my-remote"].(map[string]any)
			if entry["url"] != "https://example.com/mcp" {
				t.Errorf("remote url = %v", entry["url"])
			}

			// 3. Remove the remote server
			if err := RemoveServer(c, "my-remote"); err != nil {
				t.Fatalf("RemoveServer: %v", err)
			}
			root = readTOMLRoot(t, path)
			if root["model"] != "test-model" {
				t.Error("model was dropped after remove")
			}
			servers, _ = root["mcp_servers"].(map[string]any)
			if _, ok := servers["my-remote"]; ok {
				t.Error("my-remote was not removed")
			}
			if _, ok := servers["existing"]; !ok {
				t.Error("existing was lost after remove")
			}
			if _, ok := servers["my-stdio"]; !ok {
				t.Error("my-stdio was lost after remove")
			}
		})
	}

	// Zed subtest
	t.Run(string(ClientZed), func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "settings.json")
		writeJSONFixture(t, path, map[string]any{
			"theme": "dark",
			"context_servers": map[string]any{
				"existing": map[string]any{"command": "foo"},
			},
		})
		c := Client{
			ID:       ClientZed,
			Name:     "Zed",
			Path:     path,
			Format:   FormatZed,
			Existing: true,
		}

		// 1. Merge stdio
		stdioServer := ServerConfig{
			Command: "npx",
			Args:    []string{"-y", "pkg"},
			Env:     map[string]string{"FOO": "bar"},
		}
		if err := MergeServer(c, "my-stdio", stdioServer); err != nil {
			t.Fatalf("MergeServer stdio: %v", err)
		}
		root := readJSONObject(t, path)
		if _, ok := root["theme"]; !ok {
			t.Error("theme was dropped after stdio merge")
		}
		names := contextServerNames(t, root)
		if !names["existing"] {
			t.Error("existing server was clobbered")
		}
		if !names["my-stdio"] {
			t.Error("my-stdio was not written")
		}

		// 2. Merge remote
		remoteServer := ServerConfig{URL: "https://example.com/mcp"}
		if err := MergeServer(c, "my-remote", remoteServer); err != nil {
			t.Fatalf("MergeServer remote: %v", err)
		}
		root = readJSONObject(t, path)
		if _, ok := root["theme"]; !ok {
			t.Error("theme was dropped after remote merge")
		}
		names = contextServerNames(t, root)
		if !names["existing"] || !names["my-stdio"] || !names["my-remote"] {
			t.Errorf("servers = %v", names)
		}

		// 3. Remove the remote server
		if err := RemoveServer(c, "my-remote"); err != nil {
			t.Fatalf("RemoveServer: %v", err)
		}
		root = readJSONObject(t, path)
		if _, ok := root["theme"]; !ok {
			t.Error("theme was dropped after remove")
		}
		names = contextServerNames(t, root)
		if names["my-remote"] {
			t.Error("my-remote was not removed")
		}
		if !names["existing"] || !names["my-stdio"] {
			t.Error("existing or my-stdio was lost after remove")
		}
	})

	// Aider subtest (stdio only)
	t.Run(string(ClientAider), func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".aider.conf.yml")
		writeYAMLFixture(t, path, `model: gpt-4
mcp-servers:
  - name: existing
    command: foo
`)
		c := Client{
			ID:       ClientAider,
			Name:     "Aider",
			Path:     path,
			Format:   FormatAider,
			Existing: true,
		}

		// 1. Merge stdio
		stdioServer := ServerConfig{
			Command: "npx",
			Args:    []string{"-y", "pkg"},
			Env:     map[string]string{"FOO": "bar"},
		}
		if err := MergeServer(c, "my-stdio", stdioServer); err != nil {
			t.Fatalf("MergeServer stdio: %v", err)
		}
		root := readYAMLRoot(t, path)
		if root["model"] != "gpt-4" {
			t.Error("model was dropped after stdio merge")
		}
		rawList, _ := root["mcp-servers"].([]any)
		names := make(map[string]bool)
		for _, item := range rawList {
			m, _ := item.(map[string]any)
			if n, _ := m["name"].(string); n != "" {
				names[n] = true
			}
		}
		if !names["existing"] {
			t.Error("existing server was clobbered")
		}
		if !names["my-stdio"] {
			t.Error("my-stdio was not written")
		}

		// 2. Merge remote — should be skipped
		remoteServer := ServerConfig{URL: "https://example.com/mcp"}
		err := MergeServer(c, "my-remote", remoteServer)
		if err == nil || !IsSkip(err) {
			t.Fatalf("expected skip for Aider remote, got %v", err)
		}

		// 3. Remove the stdio server
		if err := RemoveServer(c, "my-stdio"); err != nil {
			t.Fatalf("RemoveServer: %v", err)
		}
		root = readYAMLRoot(t, path)
		if root["model"] != "gpt-4" {
			t.Error("model was dropped after remove")
		}
		rawList, _ = root["mcp-servers"].([]any)
		names = make(map[string]bool)
		for _, item := range rawList {
			m, _ := item.(map[string]any)
			if n, _ := m["name"].(string); n != "" {
				names[n] = true
			}
		}
		if names["my-stdio"] {
			t.Error("my-stdio was not removed")
		}
		if !names["existing"] {
			t.Error("existing was lost after remove")
		}
	})
}

// writeMSIXPackage creates <packagesRoot>/<family>/LocalCache/Roaming/
// Claude with an empty claude_desktop_config.json inside, mimicking a
// launched Microsoft Store (MSIX) Claude Desktop install. Returns the
// package directory path.
func writeMSIXPackage(t *testing.T, packagesRoot, family string) string {
	t.Helper()
	claudeDir := filepath.Join(packagesRoot, family, "LocalCache", "Roaming", "Claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "claude_desktop_config.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(packagesRoot, family)
}

func TestMSIXClaudeDesktopCandidates_Windows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only: exercises the %LOCALAPPDATA% MSIX branch")
	}
	home := t.TempDir()
	local := filepath.Join(home, "AppData", "Local")
	packages := filepath.Join(local, "Packages")
	writeMSIXPackage(t, packages, "Claude_pzs8sxrjxfjjc")

	t.Run("env LOCALAPPDATA", func(t *testing.T) {
		t.Setenv("LOCALAPPDATA", local)
		got := msixClaudeDesktopCandidates(home)
		if len(got) != 1 {
			t.Fatalf("got %d candidates, want 1: %+v", len(got), got)
		}
		c := got[0]
		if c.ID != ClientClaudeDesktop {
			t.Errorf("ID = %q, want %q", c.ID, ClientClaudeDesktop)
		}
		if c.Name != "Claude Desktop (Microsoft Store)" {
			t.Errorf("Name = %q, want %q", c.Name, "Claude Desktop (Microsoft Store)")
		}
		wantPath := filepath.Join(packages, "Claude_pzs8sxrjxfjjc", "LocalCache", "Roaming", "Claude", "claude_desktop_config.json")
		if c.Path != wantPath {
			t.Errorf("Path = %q, want %q", c.Path, wantPath)
		}
		if c.Format != FormatMcpServers {
			t.Errorf("Format = %q, want %q", c.Format, FormatMcpServers)
		}
	})

	t.Run("fallback to home/AppData/Local", func(t *testing.T) {
		t.Setenv("LOCALAPPDATA", "")
		got := msixClaudeDesktopCandidates(home)
		if len(got) != 1 {
			t.Fatalf("got %d candidates, want 1: %+v", len(got), got)
		}
		wantPath := filepath.Join(packages, "Claude_pzs8sxrjxfjjc", "LocalCache", "Roaming", "Claude", "claude_desktop_config.json")
		if got[0].Path != wantPath {
			t.Errorf("Path = %q, want %q", got[0].Path, wantPath)
		}
	})
}

func TestMSIXClaudeDesktopCandidates_NeverLaunched(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T, pkg string)
	}{
		{"bare package dir", func(t *testing.T, pkg string) {
			if err := os.MkdirAll(pkg, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"LocalCache only", func(t *testing.T, pkg string) {
			if err := os.MkdirAll(filepath.Join(pkg, "LocalCache"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"LocalCache/Roaming only", func(t *testing.T, pkg string) {
			if err := os.MkdirAll(filepath.Join(pkg, "LocalCache", "Roaming"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			packages := filepath.Join(root, "Packages")
			tc.build(t, filepath.Join(packages, "Claude_pzs8sxrjxfjjc"))
			if got := msixScan(packages); len(got) != 0 {
				t.Fatalf("got %d candidates, want 0: %+v", len(got), got)
			}
		})
	}
}

func TestMSIXClaudeDesktopCandidates_PrefixMatch(t *testing.T) {
	cases := []struct {
		family string
		want   bool
	}{
		{family: "Claude_abc", want: true},
		{family: "Claudette_xyz", want: false},
		{family: "Claude-Anything_abc", want: false},
		{family: "MyClaude_def", want: false},
	}
	root := t.TempDir()
	packages := filepath.Join(root, "Packages")
	for _, tc := range cases {
		writeMSIXPackage(t, packages, tc.family)
	}

	got := msixScan(packages)
	var wantPaths []string
	for _, tc := range cases {
		if tc.want {
			wantPaths = append(wantPaths, filepath.Join(packages, tc.family, "LocalCache", "Roaming", "Claude", "claude_desktop_config.json"))
		}
	}
	if len(got) != len(wantPaths) {
		t.Fatalf("got %d candidates (%+v), want %d", len(got), got, len(wantPaths))
	}
	gotPaths := make(map[string]bool, len(got))
	for _, c := range got {
		gotPaths[c.Path] = true
	}
	for _, want := range wantPaths {
		if !gotPaths[want] {
			t.Errorf("missing candidate for %q; got %v", want, gotPaths)
		}
	}
}

func TestMSIXClaudeDesktopCandidates_MultiUser_WSL2(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only: exercises the WSL2 windowsUserDirs MSIX branch")
	}
	root := isolateWindowsUsers(t)
	u1 := filepath.Join(root, "u1")
	u2 := filepath.Join(root, "u2")
	for _, u := range []string{u1, u2} {
		if err := os.MkdirAll(u, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeMSIXPackage(t, filepath.Join(u1, "AppData", "Local", "Packages"), "Claude_pzs8sxrjxfjjc")

	got := msixClaudeDesktopCandidates("home-ignored-on-linux")
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1 (only u1 has MSIX Claude): %+v", len(got), got)
	}
	wantPath := filepath.Join(u1, "AppData", "Local", "Packages", "Claude_pzs8sxrjxfjjc", "LocalCache", "Roaming", "Claude", "claude_desktop_config.json")
	if got[0].Path != wantPath {
		t.Errorf("Path = %q, want %q", got[0].Path, wantPath)
	}
	if got[0].Name != "Claude Desktop (Microsoft Store)" {
		t.Errorf("Name = %q, want %q", got[0].Name, "Claude Desktop (Microsoft Store)")
	}
}

func TestDetectDualClaudeInstall(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("MSIX detection applies to windows and WSL2 only")
	}
	isolateWindowsUsers(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	var classicDir string
	switch runtime.GOOS {
	case "windows":
		appdata := filepath.Join(home, "AppData", "Roaming")
		t.Setenv("APPDATA", appdata)
		classicDir = filepath.Join(appdata, "Claude")
	default:
		classicDir = filepath.Join(home, ".config", "Claude")
	}
	if err := os.MkdirAll(classicDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var packages string
	if runtime.GOOS == "windows" {
		local := filepath.Join(home, "AppData", "Local")
		t.Setenv("LOCALAPPDATA", local)
		packages = filepath.Join(local, "Packages")
	} else {
		userDir := filepath.Join(windowsUsersRoot(), "u1")
		if err := os.MkdirAll(userDir, 0o755); err != nil {
			t.Fatal(err)
		}
		packages = filepath.Join(userDir, "AppData", "Local", "Packages")
	}
	writeMSIXPackage(t, packages, "Claude_pzs8sxrjxfjjc")

	var detected []Client
	for _, c := range Detect() {
		if c.ID == ClientClaudeDesktop {
			detected = append(detected, c)
		}
	}
	if len(detected) != 2 {
		t.Fatalf("got %d Claude Desktop detections, want 2 (classic + MSIX): %+v", len(detected), detected)
	}
	if detected[0].Name != "Claude Desktop" {
		t.Errorf("classic candidate must be index 0 (load-bearing for DetectByID/ConfigPath/resolveWriteTargets), got %q at index 0: %+v", detected[0].Name, detected)
	}
	names := make(map[string]bool, len(detected))
	for _, c := range detected {
		names[c.Name] = true
	}
	if !names["Claude Desktop"] {
		t.Errorf("classic candidate missing from detections: %v", detected)
	}
	if !names["Claude Desktop (Microsoft Store)"] {
		t.Errorf("MSIX candidate missing from detections: %v", detected)
	}
	if names["Claude Desktop"] && names["Claude Desktop (Microsoft Store)"] && len(names) != 2 {
		t.Errorf("unexpected extra Claude Desktop variants: %v", detected)
	}
}
