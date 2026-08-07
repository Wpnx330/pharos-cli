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
	"gopkg.in/yaml.v3"
)

// ClientID identifies a known MCP client.
type ClientID string

const (
	ClientClaudeDesktop ClientID = "claude-desktop"
	ClientCursor        ClientID = "cursor"
	ClientGeneric       ClientID = "generic"
	ClientCline         ClientID = "cline"
	ClientOpenCode      ClientID = "opencode"
	ClientHermes        ClientID = "hermes"
)

// FormatMcpServers is the {"mcpServers": {...}} format used by Claude
// Desktop and Cursor.
const FormatMcpServers = "mcpServers"

// FormatArray is a flat JSON array of server entries, each identified by
// a "name" field.
const FormatArray = "array"

// FormatOpenCode is the {"mcpServers": {...}} JSON format but with env
// as an array of "KEY=VALUE" strings instead of an object.
const FormatOpenCode = "opencode"

// FormatHermes is the YAML format with a top-level mcp_servers: key.
const FormatHermes = "hermes-yaml"

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
		// WSL2: Claude Desktop may be installed on Windows. Check
		// /mnt/c/Users/<user>/AppData/Roaming/Claude/ for a config.
		for _, wu := range windowsUserDirs() {
			winPath := filepath.Join(wu, "AppData", "Roaming", "Claude", "claude_desktop_config.json")
			if _, err := os.Stat(winPath); err == nil {
				clients = append(clients, Client{
					ID:     ClientClaudeDesktop,
					Name:   "Claude Desktop (Windows via WSL2)",
					Path:   winPath,
					Format: FormatMcpServers,
				})
				break
			}
		}
	}

	// Cursor: ~/.cursor/mcp.json
	clients = append(clients, Client{
		ID:     ClientCursor,
		Name:   "Cursor",
		Path:   filepath.Join(home, ".cursor", "mcp.json"),
		Format: FormatMcpServers,
	})

	// OpenCode: ~/.config/opencode/opencode.json
	clients = append(clients, Client{
		ID:     ClientOpenCode,
		Name:   "OpenCode",
		Path:   filepath.Join(home, ".config", "opencode", "opencode.json"),
		Format: FormatOpenCode,
	})

	// Hermes: ~/.hermes/config.yaml (YAML format)
	clients = append(clients, Client{
		ID:     ClientHermes,
		Name:   "Hermes Agent",
		Path:   filepath.Join(home, ".hermes", "config.yaml"),
		Format: FormatHermes,
	})

	// Cline: VS Code extension config.
	clinePaths := clineCandidatePaths(home)
	clients = append(clients, clinePaths...)

	// Generic: ~/.config/mcp/mcp.json
	clients = append(clients, Client{
		ID:     ClientGeneric,
		Name:   "Generic MCP",
		Path:   filepath.Join(home, ".config", "mcp", "mcp.json"),
		Format: FormatMcpServers,
	})

	return clients
}

// windowsUserDirs returns likely Windows user home directories under
// /mnt/c/Users/ on WSL2. Returns empty on non-WSL2 systems.
func windowsUserDirs() []string {
	// Only meaningful on Linux (WSL2).
	if runtime.GOOS != "linux" {
		return nil
	}
	entries, err := os.ReadDir("/mnt/c/Users")
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Skip system/default profiles.
		if name == "Public" || name == "Default" || name == "Default User" ||
			name == "All Users" || strings.HasPrefix(name, ".") {
			continue
		}
		dirs = append(dirs, filepath.Join("/mnt/c", "Users", name))
	}
	return dirs
}

// clineCandidatePaths returns Cline client entries for the current OS.
func clineCandidatePaths(home string) []Client {
	switch runtime.GOOS {
	case "darwin":
		return []Client{{
			ID:     ClientCline,
			Name:   "Cline",
			Path:   filepath.Join(home, "Library", "Application Support", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"),
			Format: FormatMcpServers,
		}}
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			appdata = filepath.Join(home, "AppData", "Roaming")
		}
		return []Client{{
			ID:     ClientCline,
			Name:   "Cline",
			Path:   filepath.Join(appdata, "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"),
			Format: FormatMcpServers,
		}}
	default:
		var clients []Client
		// Linux native VS Code
		clients = append(clients, Client{
			ID:     ClientCline,
			Name:   "Cline",
			Path:   filepath.Join(home, ".config", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"),
			Format: FormatMcpServers,
		})
		// WSL2: VS Code installed on Windows
		for _, wu := range windowsUserDirs() {
			winPath := filepath.Join(wu, "AppData", "Roaming", "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json")
			if _, err := os.Stat(winPath); err == nil {
				clients = append(clients, Client{
					ID:     ClientCline,
					Name:   "Cline (Windows via WSL2)",
					Path:   winPath,
					Format: FormatMcpServers,
				})
				break
			}
		}
		return clients
	}
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

	switch format {
	case FormatArray:
		return mergeArray(c, name, server)
	case FormatHermes:
		return mergeHermes(c, name, server)
	case FormatOpenCode:
		// OpenCode uses the mcpServers JSON root key but a different
		// env format — fall through to the standard mcpServers path;
		// buildEntry handles the env-as-array difference.
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

// hermesServerEntry is the YAML representation of a single MCP server
// in the Hermes config.yaml file. Field tags use snake_case to match
// the existing Hermes format.
type hermesServerEntry struct {
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	Enabled bool              `yaml:"enabled"`
	URL     string            `yaml:"url,omitempty"`
}

// mergeHermes reads the Hermes config.yaml, adds or replaces the server
// entry under mcp_servers:, and writes it back. All existing top-level
// keys (provider settings, mcp_servers, etc.) are preserved.
func mergeHermes(c Client, name string, server ServerConfig) error {
	data, err := os.ReadFile(c.Path)
	if err != nil {
		if os.IsNotExist(err) {
			// Create a minimal config with just the server.
			root := map[string]any{
				"mcp_servers": map[string]any{
					name: hermesServerEntry{
						Command: server.Command,
						Args:    server.Args,
						Env:     server.Env,
						Enabled: true,
						URL:     server.URL,
					},
				},
			}
			out, _ := yaml.Marshal(root)
			return writeHermesConfig(c.Path, out)
		}
		return fmt.Errorf("read config %s: %w", c.Path, err)
	}

	// Parse into a generic map to preserve all existing keys.
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse config %s: %w", c.Path, err)
	}
	if root == nil {
		root = make(map[string]any)
	}

	// Get or create the mcp_servers section.
	servers, _ := root["mcp_servers"].(map[string]any)
	if servers == nil {
		servers = make(map[string]any)
	}

	// Build the new entry.
	entry := hermesServerEntry{
		Command: server.Command,
		Args:    server.Args,
		Env:     server.Env,
		Enabled: true,
		URL:     server.URL,
	}
	servers[name] = entry
	root["mcp_servers"] = servers

	out, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return writeHermesConfig(c.Path, out)
}

// safeWriteConfig writes data to the config file using a safe copy-modify-
// validate-swap pattern:
//  1. Read the original file (if it exists) and record its size.
//  2. Write the new data to a temp file in the same directory.
//  3. Validate the temp file: it must be non-empty, and if the original
//     was >100 bytes, the new file must be at least 50% of the original
//     size (catches accidental truncation/corruption).
//  4. Rename the temp file over the original (atomic on most filesystems).
//
// If any step fails, the original file is left untouched and the temp
// file is cleaned up.
func SafeWriteConfig(path string, data []byte, format string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// 1. Record original size (if file exists).
	origSize := 0
	if origData, err := os.ReadFile(path); err == nil {
		origSize = len(origData)
	}

	// 2. Write to temp file.
	tmpPath := path + ".pharos-tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}

	// 3. Validate.
	writtenData, err := os.ReadFile(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("read back temp config: %w", err)
	}
	writtenSize := len(writtenData)

	// Non-empty check.
	if writtenSize == 0 {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("validation failed: wrote 0 bytes")
	}

	// Size ratio check: if the original was substantial and the new
	// file is less than 25% of it, something is probably wrong.
	// We use 25% (not 50%) because removing a server from a 2-entry
	// config legitimately halves the size, but even that stays above
	// 25%. Going from 10KB to 23 bytes (the Hermes corruption bug)
	// would be caught at any reasonable threshold.
	if origSize > 200 && writtenSize < origSize/4 {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("validation failed: wrote %d bytes but original was %d bytes (possible truncation)", writtenSize, origSize)
	}

	// Format check: re-parse to confirm the written file is valid.
	switch format {
	case "hermes-yaml":
		var verify map[string]any
		if err := yaml.Unmarshal(writtenData, &verify); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("validation failed: written YAML is not parseable: %w", err)
		}
	default:
		var verify configFile
		if err := json.Unmarshal(writtenData, &verify); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("validation failed: written JSON is not parseable: %w", err)
		}
	}

	// 4. Atomic swap.
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("swap config %s: %w", path, err)
	}
	return nil
}

// writeHermesConfig writes the YAML data to the config file safely.
func writeHermesConfig(path string, data []byte) error {
	return SafeWriteConfig(path, data, "hermes-yaml")
}

// removeHermesServer removes a server entry from the Hermes YAML config.
func removeHermesServer(path, name string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return false, fmt.Errorf("parse config: %w", err)
	}
	servers, _ := root["mcp_servers"].(map[string]any)
	if servers == nil {
		return false, nil
	}
	if _, ok := servers[name]; !ok {
		return false, nil
	}
	delete(servers, name)
	root["mcp_servers"] = servers
	out, err := yaml.Marshal(root)
	if err != nil {
		return false, fmt.Errorf("marshal config: %w", err)
	}
	return true, writeHermesConfig(path, out)
}

// writeConfig marshals and writes the config file safely.
func writeConfig(path string, cfg *configFile) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	// Trailing newline for friendliness with editors.
	data = append(data, '\n')
	return SafeWriteConfig(path, data, "mcpServers")
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

	case ClientOpenCode:
		// OpenCode uses mcpServers format but env is an array of
		// "KEY=VALUE" strings, and it has a "type" field (stdio|sse).
		entry := map[string]any{}
		if server.URL != "" {
			entry["url"] = server.URL
			entry["type"] = "sse"
		} else {
			entry["command"] = server.Command
			if len(server.Args) > 0 {
				entry["args"] = server.Args
			}
			entry["type"] = "stdio"
			if len(server.Env) > 0 {
				envArr := make([]string, 0, len(server.Env))
				for k, v := range server.Env {
					envArr = append(envArr, k+"="+v)
				}
				sort.Strings(envArr)
				entry["env"] = envArr
			}
		}
		return json.Marshal(entry)

	default:
		// Claude Desktop + generic + Cline: stdio uses command/args/env,
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
// "name" field is used as the key. For the "hermes-yaml" format, the
// YAML mcp_servers: section is parsed. Returns an empty map if the file
// doesn't exist.
func ReadServersFormat(path, format string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]json.RawMessage), nil
		}
		return nil, err
	}
	switch format {
	case FormatArray:
		return readArrayServers(data)
	case FormatHermes:
		return readHermesServers(data)
	default:
		// FormatMcpServers and FormatOpenCode both use JSON
		// {\"mcpServers\": {...}} as the root structure.
		var cfg configFile
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
		if cfg.McpServers == nil {
			cfg.McpServers = make(map[string]json.RawMessage)
		}
		return cfg.McpServers, nil
	}
}

// readHermesServers parses a Hermes YAML config and returns the
// mcp_servers entries as a name→raw-JSON map (converted to JSON for
// uniformity with other formats).
func readHermesServers(data []byte) (map[string]json.RawMessage, error) {
	var root map[string]any
	if len(strings.TrimSpace(string(data))) == 0 {
		return make(map[string]json.RawMessage), nil
	}
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	servers, _ := root["mcp_servers"].(map[string]any)
	if servers == nil {
		return make(map[string]json.RawMessage), nil
	}
	out := make(map[string]json.RawMessage, len(servers))
	for name, raw := range servers {
		// Re-marshal the YAML-parsed value to JSON.
		j, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		out[name] = j
	}
	return out, nil
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
var AllClients = []ClientID{
	ClientClaudeDesktop, ClientCursor, ClientGeneric,
	ClientCline, ClientOpenCode, ClientHermes,
}

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
	// Find the client to check its format.
	var format string
	var path string
	for _, c := range candidatePaths() {
		if c.ID == id {
			format = c.Format
			path = c.Path
			break
		}
	}
	if path == "" {
		return false, fmt.Errorf("unknown client ID: %s", id)
	}

	// Hermes uses YAML format.
	if format == FormatHermes {
		return removeHermesServer(path, name)
	}

	// OpenCode and standard mcpServers both use JSON configFile.
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
			// OpenCode stores env as an array of "KEY=VALUE" strings.
			if envArr, ok := m["env"].([]any); ok && s.Env == nil {
				s.Env = make(map[string]string)
				for _, e := range envArr {
					if str, ok := e.(string); ok {
						if idx := strings.Index(str, "="); idx > 0 {
							s.Env[str[:idx]] = str[idx+1:]
						}
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
