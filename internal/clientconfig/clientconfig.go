// Package clientconfig detects installed MCP clients (Claude Desktop,
// Cursor, generic), reads their config files, and merges MCP server
// entries without clobbering existing servers.
package clientconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ClientID identifies a known MCP client.
type ClientID string

const (
	ClientClaudeDesktop ClientID = "claude-desktop"
	ClientCursor        ClientID = "cursor"
	ClientGeneric       ClientID = "generic"
)

// Client describes a detected MCP client and its config file path.
type Client struct {
	ID       ClientID
	Name     string
	Path     string
	Existing bool // true if the config file already exists
}

// ServerConfig is the MCP server entry written into client config files.
// For stdio servers: Command + Args + Env.
// For http/sse servers: URL (and Type for Cursor).
type ServerConfig struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Type    string            `json:"type,omitempty"` // "stdio" | "sse" | "http" (Cursor uses this)
}

// configFile represents the JSON structure shared by Claude Desktop and
// Cursor: {"mcpServers": {...}}.
type configFile struct {
	McpServers map[string]json.RawMessage `json:"mcpServers"`
}

// Detect probes well-known paths and returns every client whose config
// directory or file is present. A client is "detected" if its config
// file exists OR its parent application directory exists.
func Detect() []Client {
	var clients []Client
	for _, c := range candidatePaths() {
		existing := false
		if _, err := os.Stat(c.Path); err == nil {
			existing = true
		} else if dirExists(filepath.Dir(c.Path)) {
			// Config doesn't exist yet but the app directory does —
			// we can create it.
			existing = false
		} else {
			// Neither config nor app dir present — skip.
			continue
		}
		c.Existing = existing
		clients = append(clients, c)
	}
	return clients
}

// DetectByID returns a single client by ID, or nil if not detected.
func DetectByID(id ClientID) *Client {
	for _, c := range Detect() {
		if c.ID == id {
			return &c
		}
	}
	return nil
}

// candidatePaths returns the well-known config paths for the current OS.
func candidatePaths() []Client {
	home, _ := os.UserHomeDir()
	var clients []Client

	switch runtime.GOOS {
	case "darwin":
		clients = append(clients, Client{
			ID:   ClientClaudeDesktop,
			Name: "Claude Desktop",
			Path: filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"),
		})
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			appdata = filepath.Join(home, "AppData", "Roaming")
		}
		clients = append(clients, Client{
			ID:   ClientClaudeDesktop,
			Name: "Claude Desktop",
			Path: filepath.Join(appdata, "Claude", "claude_desktop_config.json"),
		})
	default:
		// Linux: some users run Claude via electron; check XDG config.
		clients = append(clients, Client{
			ID:   ClientClaudeDesktop,
			Name: "Claude Desktop",
			Path: filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"),
		})
	}

	// Cursor: ~/.cursor/mcp.json
	clients = append(clients, Client{
		ID:   ClientCursor,
		Name: "Cursor",
		Path: filepath.Join(home, ".cursor", "mcp.json"),
	})

	// Generic: ~/.config/mcp/mcp.json
	clients = append(clients, Client{
		ID:   ClientGeneric,
		Name: "Generic MCP",
		Path: filepath.Join(home, ".config", "mcp", "mcp.json"),
	})

	return clients
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// MergeServer reads the client config at c.Path, adds or replaces the
// server entry under `name`, and writes it back. Existing servers are
// preserved. If the config file doesn't exist it is created.
func MergeServer(c Client, name string, server ServerConfig) error {
	cfg := configFile{
		McpServers: make(map[string]json.RawMessage),
	}

	if c.Existing {
		data, err := os.ReadFile(c.Path)
		if err != nil {
			return fmt.Errorf("read config %s: %w", c.Path, err)
		}
		// The file may be empty.
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &cfg); err != nil {
				return fmt.Errorf("parse config %s: %w", c.Path, err)
			}
			if cfg.McpServers == nil {
				cfg.McpServers = make(map[string]json.RawMessage)
			}
		}
	}

	entry, err := buildEntry(c.ID, server)
	if err != nil {
		return err
	}
	cfg.McpServers[name] = entry

	return writeConfig(c.Path, &cfg)
}

// writeConfig marshals and writes the config file, creating dirs.
func writeConfig(path string, cfg *configFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	// Trailing newline for friendliness with editors.
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

// buildEntry produces the JSON raw message for a server entry, tailored
// to the client's expected format.
func buildEntry(id ClientID, server ServerConfig) (json.RawMessage, error) {
	switch id {
	case ClientCursor:
		// Cursor supports a "type" field for sse/http.
		entry := map[string]any{}
		if server.URL != "" {
			entry["url"] = server.URL
			if server.Type != "" {
				entry["type"] = server.Type
			} else {
				entry["type"] = "sse"
			}
		} else {
			entry["command"] = server.Command
			entry["args"] = server.Args
			if len(server.Env) > 0 {
				entry["env"] = server.Env
			}
		}
		return json.Marshal(entry)

	default:
		// Claude Desktop + generic: stdio uses command/args/env,
		// http/sse uses url (+ type for generic).
		entry := map[string]any{}
		if server.URL != "" {
			entry["url"] = server.URL
			if server.Type != "" {
				entry["type"] = server.Type
			}
		} else {
			entry["command"] = server.Command
			if len(server.Args) > 0 {
				entry["args"] = server.Args
			}
			if len(server.Env) > 0 {
				entry["env"] = server.Env
			}
		}
		return json.Marshal(entry)
	}
}

// ReadServers reads the config file and returns the map of server names
// to raw JSON entries. Returns an empty map if the file doesn't exist.
func ReadServers(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]json.RawMessage), nil
		}
		return nil, err
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.McpServers == nil {
		cfg.McpServers = make(map[string]json.RawMessage)
	}
	return cfg.McpServers, nil
}

// AllClients returns the list of all known client IDs regardless of
// whether they're installed.
var AllClients = []ClientID{ClientClaudeDesktop, ClientCursor, ClientGeneric}

// ConfigPath returns the config file path for a client ID on the current
// OS, or "" if the ID is unknown.
func ConfigPath(id ClientID) string {
	for _, c := range candidatePaths() {
		if c.ID == id {
			return c.Path
		}
	}
	return ""
}

// RemoveServer removes a server entry from the config file of the given
// client ID. Returns (true, nil) if the server was found and removed,
// (false, nil) if it wasn't present.
func RemoveServer(id ClientID, name string) (bool, error) {
	path := ConfigPath(id)
	if path == "" {
		return false, fmt.Errorf("unknown client ID: %s", id)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false, fmt.Errorf("parse config: %w", err)
	}
	if cfg.McpServers == nil {
		return false, nil
	}
	if _, ok := cfg.McpServers[name]; !ok {
		return false, nil
	}
	delete(cfg.McpServers, name)
	return true, writeConfig(path, &cfg)
}

// ClientConfig is a loaded client config with its associated metadata,
// used by the import command.
type ClientConfig struct {
	ClientID ClientID
	Path     string
	Servers  []ClientConfigServer
}

// ClientConfigServer is a single server entry parsed from a client config.
type ClientConfigServer struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	URL     string
	Type    string
}

// Load reads and parses a single client's config file.
func Load(id ClientID) (*ClientConfig, error) {
	path := ConfigPath(id)
	if path == "" {
		return nil, fmt.Errorf("unknown client ID: %s", id)
	}
	raw, err := ReadServers(path)
	if err != nil {
		return nil, err
	}
	cc := &ClientConfig{ClientID: id, Path: path}
	for name, entry := range raw {
		var s ClientConfigServer
		s.Name = name
		var m map[string]any
		if json.Unmarshal(entry, &m) == nil {
			if v, ok := m["command"].(string); ok {
				s.Command = v
			}
			if v, ok := m["url"].(string); ok {
				s.URL = v
			}
			if v, ok := m["type"].(string); ok {
				s.Type = v
			}
			if args, ok := m["args"].([]any); ok {
				for _, a := range args {
					if str, ok := a.(string); ok {
						s.Args = append(s.Args, str)
					}
				}
			}
			if env, ok := m["env"].(map[string]any); ok {
				s.Env = make(map[string]string)
				for k, v := range env {
					if str, ok := v.(string); ok {
						s.Env[k] = str
					}
				}
			}
		}
		cc.Servers = append(cc.Servers, s)
	}
	return cc, nil
}

// LoadAll loads configs from all detected clients.
func LoadAll() ([]*ClientConfig, error) {
	var configs []*ClientConfig
	for _, c := range Detect() {
		cc, err := Load(c.ID)
		if err != nil {
			continue
		}
		configs = append(configs, cc)
	}
	return configs, nil
}
