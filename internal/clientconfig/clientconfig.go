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
	"sort"
	"strings"

	"github.com/Wpnx330/pharos-cli/internal/config"
)

// ClientID identifies a known MCP client.
type ClientID string

const (
	ClientClaudeDesktop ClientID = "claude-desktop"
	ClientCursor        ClientID = "cursor"
	ClientGeneric       ClientID = "generic"
)

// FormatMcpServers is the {"mcpServers": {...}} format used by Claude
// Desktop and Cursor.
const FormatMcpServers = "mcpServers"

// FormatArray is a flat JSON array of server entries, each identified by
// a "name" field.
const FormatArray = "array"

// Client describes a detected MCP client and its config file path.
type Client struct {
	ID       ClientID
	Name     string
	Path     string
	Format   string // "mcpServers" (default) or "array"
	Existing bool   // true if the config file already exists
	Custom   bool   // true if this is a user-registered custom client
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
//
// Custom clients registered via `pharos config add-client` are also
// included: a custom client is "detected" if its config file exists.
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

	// Append custom clients from config.json.
	clients = append(clients, detectCustom()...)

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
			ID:     ClientClaudeDesktop,
			Name:   "Claude Desktop",
			Path:   filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"),
			Format: FormatMcpServers,
		})
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			appdata = filepath.Join(home, "AppData", "Roaming")
		}
		clients = append(clients, Client{
			ID:     ClientClaudeDesktop,
			Name:   "Claude Desktop",
			Path:   filepath.Join(appdata, "Claude", "claude_desktop_config.json"),
			Format: FormatMcpServers,
		})
	default:
		// Linux: some users run Claude via electron; check XDG config.
		clients = append(clients, Client{
			ID:     ClientClaudeDesktop,
			Name:   "Claude Desktop",
			Path:   filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"),
			Format: FormatMcpServers,
		})
	}

	// Cursor: ~/.cursor/mcp.json
	clients = append(clients, Client{
		ID:     ClientCursor,
		Name:   "Cursor",
		Path:   filepath.Join(home, ".cursor", "mcp.json"),
		Format: FormatMcpServers,
	})

	// Generic: ~/.config/mcp/mcp.json
	clients = append(clients, Client{
		ID:     ClientGeneric,
		Name:   "Generic MCP",
		Path:   filepath.Join(home, ".config", "mcp", "mcp.json"),
		Format: FormatMcpServers,
	})

	return clients
}

// detectCustom loads custom clients from ~/.pharos/config.json and
// returns the ones whose config file exists on disk (i.e. "detected").
// Custom clients with a missing config file are excluded so that
// `pharos install` does not attempt to write to them, matching the
// behaviour of built-in clients whose app directory is absent.
func detectCustom() []Client {
	cfg, err := config.Load()
	if err != nil {
		return nil
	}
	// Sort custom clients by ID for deterministic ordering.
	sorted := make([]config.CustomClient, len(cfg.CustomClients))
	copy(sorted, cfg.CustomClients)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	var clients []Client
	for _, cc := range sorted {
		format := cc.Format
		if format == "" {
			format = FormatMcpServers
		}
		c := Client{
			ID:     ClientID(cc.ID),
			Name:   cc.ID,
			Path:   cc.Path,
			Format: format,
			Custom: true,
		}
		if _, err := os.Stat(cc.Path); err == nil {
			c.Existing = true
			clients = append(clients, c)
		} else if dirExists(filepath.Dir(cc.Path)) {
			// Config doesn't exist yet but parent dir does.
			clients = append(clients, c)
		}
		// If neither the file nor its parent dir exists, skip — same
		// rule as built-in clients.
	}
	return clients
}

// CandidatePaths returns the built-in client candidates (with detection
// status NOT resolved). It is used by list-clients to show every
// built-in client even when not detected.
func CandidatePaths() []Client {
	return candidatePaths()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// MergeServer reads the client config at c.Path, adds or replaces the
// server entry under `name`, and writes it back. Existing servers are
// preserved. If the config file doesn't exist it is created.
//
// The JSON structure written depends on c.Format:
//   - "mcpServers" (default): {"mcpServers": {...}}
//   - "array": a flat JSON array of objects, each with a "name" field
func MergeServer(c Client, name string, server ServerConfig) error {
	format := c.Format
	if format == "" {
		format = FormatMcpServers
	}

	if format == FormatArray {
		return mergeArray(c, name, server)
	}

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

// arrayEntry is a single server entry in the "array" config format.
type arrayEntry struct {
	Name    string            `json:"name"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Type    string            `json:"type,omitempty"`
}

// mergeArray handles MergeServer for the "array" config format: a flat
// JSON array of objects, each carrying a "name" field.
func mergeArray(c Client, name string, server ServerConfig) error {
	var entries []arrayEntry

	if c.Existing {
		data, err := os.ReadFile(c.Path)
		if err != nil {
			return fmt.Errorf("read config %s: %w", c.Path, err)
		}
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &entries); err != nil {
				return fmt.Errorf("parse config %s: %w", c.Path, err)
			}
		}
	}

	entry := arrayEntry{
		Name:    name,
		Command: server.Command,
		Args:    server.Args,
		Env:     server.Env,
		URL:     server.URL,
		Type:    server.Type,
	}

	found := false
	for i := range entries {
		if entries[i].Name == name {
			entries[i] = entry
			found = true
			break
		}
	}
	if !found {
		entries = append(entries, entry)
	}

	return writeArrayConfig(c.Path, entries)
}

// writeArrayConfig marshals and writes an array-format config file.
func writeArrayConfig(path string, entries []arrayEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
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
//
// This assumes the {"mcpServers": {...}} format. For the "array" format
// use ReadServersFormat.
func ReadServers(path string) (map[string]json.RawMessage, error) {
	return ReadServersFormat(path, FormatMcpServers)
}

// ReadServersFormat reads a config file and returns the server entries
// as a name→raw-JSON map. For the "array" format, each array element's
// "name" field is used as the key. Returns an empty map if the file
// doesn't exist.
func ReadServersFormat(path, format string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]json.RawMessage), nil
		}
		return nil, err
	}
	if format == FormatArray {
		return readArrayServers(data)
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

// readArrayServers parses an array-format config into a name→raw map.
func readArrayServers(data []byte) (map[string]json.RawMessage, error) {
	var entries []arrayEntry
	if len(strings.TrimSpace(string(data))) == 0 {
		return make(map[string]json.RawMessage), nil
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	out := make(map[string]json.RawMessage, len(entries))
	for _, e := range entries {
		// Re-marshal the entry without the "name" field so the value
		// matches the shape produced by the mcpServers format.
		clone := e
		clone.Name = ""
		raw, _ := json.Marshal(clone)
		out[e.Name] = raw
	}
	return out, nil
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
	return loadFromPath(id, path, FormatMcpServers)
}

// loadFromPath reads a config file at the given path using the given
// format and parses it into a ClientConfig.
func loadFromPath(id ClientID, path, format string) (*ClientConfig, error) {
	raw, err := ReadServersFormat(path, format)
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
		cc, err := loadFromPath(c.ID, c.Path, c.Format)
		if err != nil {
			continue
		}
		configs = append(configs, cc)
	}
	return configs, nil
}
