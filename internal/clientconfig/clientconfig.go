// Package clientconfig detects installed MCP clients (Claude Desktop,
// Cursor, generic), reads their config files, and merges MCP server
// entries without clobbering existing servers.
package clientconfig

import (
	"encoding/json"
	"errors"
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
	ClientClaudeCode    ClientID = "claude-code"
	ClientCursor        ClientID = "cursor"
	ClientGeneric       ClientID = "generic"
	ClientCline         ClientID = "cline"
	ClientOpenCode      ClientID = "opencode"
	ClientHermes        ClientID = "hermes"
	ClientVSCode        ClientID = "vscode"
	ClientWindsurf      ClientID = "windsurf"
	ClientGemini        ClientID = "gemini"
	ClientAmazonQ       ClientID = "amazonq"
	ClientRooCode       ClientID = "roo-code"
)

// SkipClaudeDesktopRemote is the user-facing reason when a remote/HTTP
// server cannot be written into claude_desktop_config.json. Official
// Desktop remotes are Settings → Connectors, not JSON.
const SkipClaudeDesktopRemote = "Claude Desktop remotes are Settings → Connectors, not claude_desktop_config.json"

// SkipError means the client was not configured because Pharos cannot
// write a shape that client accepts. It is not a write failure.
type SkipError struct {
	Reason string
}

func (e *SkipError) Error() string {
	if e == nil || e.Reason == "" {
		return "client skipped"
	}
	return e.Reason
}

// IsSkip reports whether err is a SkipError (client not configurable).
func IsSkip(err error) bool {
	var skip *SkipError
	return errors.As(err, &skip)
}

// SkippedClient is a detected client that MergeServer refused to write.
type SkippedClient struct {
	Client Client
	Reason string
}

// FormatMcpServers is the {"mcpServers": {...}} format used by Claude
// Desktop and Cursor.
const FormatMcpServers = "mcpServers"

// FormatArray is a flat JSON array of server entries, each identified by
// a "name" field.
const FormatArray = "array"

// FormatOpenCode is OpenCode's official JSON shape: top-level "mcp"
// (never "mcpServers"), with local/remote entries. Env, if present, is
// an array of "KEY=VALUE" strings inside each mcp entry.
const FormatOpenCode = "opencode"

// json key names for the two object-based MCP maps.
const (
	keyMcpServers = "mcpServers"
	keyMcp        = "mcp"
)

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
		} else if c.ID == ClientClaudeCode {
			// User-scope Claude Code is ~/.claude.json. The parent is
			// $HOME, which always exists — do not create this file
			// for people who do not use Code.
			continue
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

// DetectByID returns the first detected client with the given ID, or
// nil if none. Prefer ClientsByID when a client can exist at more than
// one home-level path (Linux + Windows via WSL2).
func DetectByID(id ClientID) *Client {
	all := ClientsByID(id)
	if len(all) == 0 {
		return nil
	}
	c := all[0]
	return &c
}

// ClientsByID returns every detected client with the given ID. The same
// ID can appear twice (WSL $HOME and a Windows profile via /mnt/c/Users).
func ClientsByID(id ClientID) []Client {
	var out []Client
	for _, c := range Detect() {
		if c.ID == id {
			out = append(out, c)
		}
	}
	return out
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
			}
		}
	}

	// Cursor: ~/.cursor/mcp.json (Linux/macOS/Windows user home)
	clients = append(clients, Client{
		ID:     ClientCursor,
		Name:   "Cursor",
		Path:   filepath.Join(home, ".cursor", "mcp.json"),
		Format: FormatMcpServers,
	})
	// WSL2: Cursor on Windows reads %USERPROFILE%\.cursor\mcp.json.
	if runtime.GOOS == "linux" {
		for _, wu := range windowsUserDirs() {
			clients = append(clients, Client{
				ID:     ClientCursor,
				Name:   "Cursor (Windows via WSL2)",
				Path:   filepath.Join(wu, ".cursor", "mcp.json"),
				Format: FormatMcpServers,
			})
		}
	}

	// Claude Code: ~/.claude.json user-scope (top-level mcpServers).
	// Detect() requires the file itself — $HOME always exists and must
	// not cause us to invent a Code config.
	clients = append(clients, Client{
		ID:     ClientClaudeCode,
		Name:   "Claude Code",
		Path:   filepath.Join(home, ".claude.json"),
		Format: FormatMcpServers,
	})
	if runtime.GOOS == "linux" {
		for _, wu := range windowsUserDirs() {
			clients = append(clients, Client{
				ID:     ClientClaudeCode,
				Name:   "Claude Code (Windows via WSL2)",
				Path:   filepath.Join(wu, ".claude.json"),
				Format: FormatMcpServers,
			})
		}
	}

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

	// VS Code (GitHub Copilot): ~/.copilot/mcp-config.json
	clients = append(clients, Client{
		ID:     ClientVSCode,
		Name:   "VS Code (GitHub Copilot)",
		Path:   filepath.Join(home, ".copilot", "mcp-config.json"),
		Format: FormatMcpServers,
	})
	// WSL2: VS Code on Windows reads %USERPROFILE%\.copilot\mcp-config.json.
	if runtime.GOOS == "linux" {
		for _, wu := range windowsUserDirs() {
			clients = append(clients, Client{
				ID:     ClientVSCode,
				Name:   "VS Code (GitHub Copilot) (Windows via WSL2)",
				Path:   filepath.Join(wu, ".copilot", "mcp-config.json"),
				Format: FormatMcpServers,
			})
		}
	}

	// Windsurf: OS-specific config paths
	windsurfPath := ""
	switch runtime.GOOS {
	case "darwin":
		windsurfPath = filepath.Join(home, "Library", "Application Support", "Codeium", "windsurf", "mcp_config.json")
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			appdata = filepath.Join(home, "AppData", "Roaming")
		}
		windsurfPath = filepath.Join(appdata, "Codeium", "windsurf", "mcp_config.json")
	default:
		windsurfPath = filepath.Join(home, ".codeium", "windsurf", "mcp_config.json")
	}
	clients = append(clients, Client{
		ID:     ClientWindsurf,
		Name:   "Windsurf",
		Path:   windsurfPath,
		Format: FormatMcpServers,
	})
	// WSL2: Windsurf on Windows reads %APPDATA%\Codeium\windsurf\mcp_config.json.
	if runtime.GOOS == "linux" {
		for _, wu := range windowsUserDirs() {
			clients = append(clients, Client{
				ID:     ClientWindsurf,
				Name:   "Windsurf (Windows via WSL2)",
				Path:   filepath.Join(wu, "AppData", "Roaming", "Codeium", "windsurf", "mcp_config.json"),
				Format: FormatMcpServers,
			})
		}
	}

	// Gemini CLI: ~/.gemini/settings.json
	clients = append(clients, Client{
		ID:     ClientGemini,
		Name:   "Gemini CLI",
		Path:   filepath.Join(home, ".gemini", "settings.json"),
		Format: FormatMcpServers,
	})
	// WSL2: Gemini CLI on Windows reads %USERPROFILE%\.gemini\settings.json.
	if runtime.GOOS == "linux" {
		for _, wu := range windowsUserDirs() {
			clients = append(clients, Client{
				ID:     ClientGemini,
				Name:   "Gemini CLI (Windows via WSL2)",
				Path:   filepath.Join(wu, ".gemini", "settings.json"),
				Format: FormatMcpServers,
			})
		}
	}

	// Amazon Q Developer: ~/.aws/amazonq/mcp.json
	clients = append(clients, Client{
		ID:     ClientAmazonQ,
		Name:   "Amazon Q Developer",
		Path:   filepath.Join(home, ".aws", "amazonq", "mcp.json"),
		Format: FormatMcpServers,
	})
	// WSL2: Amazon Q on Windows reads %USERPROFILE%\.aws\amazonq\mcp.json.
	if runtime.GOOS == "linux" {
		for _, wu := range windowsUserDirs() {
			clients = append(clients, Client{
				ID:     ClientAmazonQ,
				Name:   "Amazon Q Developer (Windows via WSL2)",
				Path:   filepath.Join(wu, ".aws", "amazonq", "mcp.json"),
				Format: FormatMcpServers,
			})
		}
	}

	// Roo Code: VS Code extension config.
	rooCodePaths := rooCodeCandidatePaths(home)
	clients = append(clients, rooCodePaths...)

	return clients
}

// windowsUsersRoot is the directory whose children are Windows user
// profiles. Tests may override via PHAROS_WINDOWS_USERS_ROOT so Detect
// never walks the live /mnt/c/Users tree.
func windowsUsersRoot() string {
	if override := strings.TrimSpace(os.Getenv("PHAROS_WINDOWS_USERS_ROOT")); override != "" {
		return override
	}
	return "/mnt/c/Users"
}

// windowsUserDirs returns likely Windows user home directories under
// /mnt/c/Users/ on WSL2. Returns empty on non-WSL2 systems.
func windowsUserDirs() []string {
	// Only meaningful on Linux (WSL2).
	if runtime.GOOS != "linux" {
		return nil
	}
	root := windowsUsersRoot()
	entries, err := os.ReadDir(root)
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
		dirs = append(dirs, filepath.Join(root, name))
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
			}
		}
		return clients
	}
}

// rooCodeCandidatePaths returns Roo Code client entries for the current OS.
func rooCodeCandidatePaths(home string) []Client {
	switch runtime.GOOS {
	case "darwin":
		return []Client{{
			ID:     ClientRooCode,
			Name:   "Roo Code",
			Path:   filepath.Join(home, "Library", "Application Support", "Code", "User", "globalStorage", "rooveterinaryinc.roo-cline", "settings", "roo_mcp_settings.json"),
			Format: FormatMcpServers,
		}}
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			appdata = filepath.Join(home, "AppData", "Roaming")
		}
		return []Client{{
			ID:     ClientRooCode,
			Name:   "Roo Code",
			Path:   filepath.Join(appdata, "Code", "User", "globalStorage", "rooveterinaryinc.roo-cline", "settings", "roo_mcp_settings.json"),
			Format: FormatMcpServers,
		}}
	default:
		var clients []Client
		// Linux native VS Code
		clients = append(clients, Client{
			ID:     ClientRooCode,
			Name:   "Roo Code",
			Path:   filepath.Join(home, ".config", "Code", "User", "globalStorage", "rooveterinaryinc.roo-cline", "settings", "roo_mcp_settings.json"),
			Format: FormatMcpServers,
		})
		// WSL2: VS Code installed on Windows
		for _, wu := range windowsUserDirs() {
			winPath := filepath.Join(wu, "AppData", "Roaming", "Code", "User", "globalStorage", "rooveterinaryinc.roo-cline", "settings", "roo_mcp_settings.json")
			if _, err := os.Stat(winPath); err == nil {
				clients = append(clients, Client{
					ID:     ClientRooCode,
					Name:   "Roo Code (Windows via WSL2)",
					Path:   winPath,
					Format: FormatMcpServers,
				})
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
	if reason := skipMergeReason(c, server); reason != "" {
		return &SkipError{Reason: reason}
	}

	format := c.Format
	if format == "" {
		format = FormatMcpServers
	}

	switch format {
	case FormatArray:
		return mergeArray(c, name, server)
	case FormatHermes:
		return mergeHermes(c, name, server)
	}

	// Object formats (mcpServers / OpenCode mcp). Patch the existing
	// document so unknown top-level keys survive ($schema, model,
	// provider, theme, preferences, …).
	servers, err := ReadServersFormat(c.Path, format)
	if err != nil {
		return err
	}

	entry, err := buildEntry(c.ID, server)
	if err != nil {
		return err
	}
	servers[name] = entry

	if format == FormatOpenCode {
		return PatchOpenCodeMcp(c.Path, servers)
	}
	return PatchMcpServers(c.Path, servers)
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

// skipMergeReason returns a non-empty skip reason when Pharos cannot
// write a shape this client accepts. The config file must be left
// untouched.
func skipMergeReason(c Client, server ServerConfig) string {
	if c.ID == ClientClaudeDesktop && strings.TrimSpace(server.URL) != "" {
		return SkipClaudeDesktopRemote
	}
	return ""
}

// writeArrayConfig marshals and writes an array-format config file.
func writeArrayConfig(path string, entries []arrayEntry) error {
	if entries == nil {
		entries = []arrayEntry{}
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')
	return SafeWriteConfig(path, data, FormatArray)
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
	case FormatArray:
		var verify []json.RawMessage
		if err := json.Unmarshal(writtenData, &verify); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("validation failed: written JSON is not a parseable array: %w", err)
		}
	default:
		// Object formats (mcpServers / OpenCode) must remain objects.
		// Unmarshal into a generic map so extra top-level keys are accepted.
		var verify map[string]json.RawMessage
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

// PatchMcpServers writes servers into the existing JSON object's
// "mcpServers" key and leaves every other top-level key untouched.
// If the file does not exist, a new object containing only mcpServers
// is created. A nil servers map is written as {}.
func PatchMcpServers(path string, servers map[string]json.RawMessage) error {
	return patchJSONServerMap(path, keyMcpServers, servers)
}

// PatchOpenCodeMcp writes servers into OpenCode's official "mcp" key and
// leaves every other top-level key untouched ($schema, model, provider,
// theme, …). A leftover "mcpServers" key from older Pharos writes is
// deleted so OpenCode never sees an unrecognized key. A nil servers map
// is written as {}.
func PatchOpenCodeMcp(path string, servers map[string]json.RawMessage) error {
	return patchJSONServerMap(path, keyMcp, servers)
}

// patchJSONServerMap writes servers into root[key] of an existing JSON
// object and preserves every other top-level key. When key is "mcp"
// (OpenCode), any leftover "mcpServers" key is removed so both keys
// never coexist. A nil servers map is written as {}.
func patchJSONServerMap(path, key string, servers map[string]json.RawMessage) error {
	root := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	if servers == nil {
		servers = make(map[string]json.RawMessage)
	}
	serversJSON, err := json.Marshal(servers)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", key, err)
	}
	root[key] = serversJSON
	if key == keyMcp {
		delete(root, keyMcpServers)
	}

	output, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	output = append(output, '\n')
	return SafeWriteConfig(path, output, key)
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
		// Official OpenCode shape (opencode.ai/docs/mcp-servers):
		//   remote: {type: "remote", url, enabled}
		//   local:  {type: "local", command: [bin, ...args], enabled}
		// Env stays an array of "KEY=VALUE" strings inside the mcp
		// entry — do not invent a third env shape.
		entry := map[string]any{"enabled": true}
		if server.URL != "" {
			entry["type"] = "remote"
			entry["url"] = server.URL
		} else {
			entry["type"] = "local"
			cmd := make([]string, 0, 1+len(server.Args))
			if server.Command != "" {
				cmd = append(cmd, server.Command)
			}
			cmd = append(cmd, server.Args...)
			entry["command"] = cmd
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

	case ClientClaudeCode:
		// Official Claude Code user-scope: top-level mcpServers.
		// Remote requires type+url (url without type is skipped by Code).
		// streamable-http maps to http. Stdio always includes type.
		entry := map[string]any{}
		if server.URL != "" {
			entry["type"] = claudeCodeRemoteType(server.Type)
			entry["url"] = server.URL
		} else {
			entry["type"] = "stdio"
			entry["command"] = server.Command
			if len(server.Args) > 0 {
				entry["args"] = server.Args
			}
			if len(server.Env) > 0 {
				entry["env"] = server.Env
			}
		}
		return json.Marshal(entry)

	case ClientClaudeDesktop:
		// Official Desktop JSON is stdio only: command + optional
		// args/env. Remotes are Settings → Connectors (skip, never
		// write {type,url} or npx mcp-remote). Do not emit type/url.
		if server.URL != "" {
			return nil, &SkipError{Reason: SkipClaudeDesktopRemote}
		}
		entry := map[string]any{"command": server.Command}
		if len(server.Args) > 0 {
			entry["args"] = server.Args
		}
		if len(server.Env) > 0 {
			entry["env"] = server.Env
		}
		return json.Marshal(entry)

	case ClientVSCode, ClientWindsurf, ClientGemini, ClientAmazonQ:
		// These clients support a "type" field for remote connections.
		// Default remote type is "http" (modern, preferred over sse).
		entry := map[string]any{}
		if server.URL != "" {
			entry["url"] = server.URL
			if server.Type != "" {
				entry["type"] = server.Type
			} else {
				entry["type"] = "http"
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

	default:
		// Generic + Cline + Roo Code: stdio uses command/args/env, http/sse uses
		// url (+ type). Cline accepts native url remotes.
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

// claudeCodeRemoteType maps Pharos transport names onto the type Code
// accepts. url without type is skipped by Code, so this never returns "".
func claudeCodeRemoteType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "sse":
		return "sse"
	case "ws", "websocket":
		return "ws"
	case "http", "streamable-http", "":
		return "http"
	default:
		return "http"
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
	if len(strings.TrimSpace(string(data))) == 0 {
		return make(map[string]json.RawMessage), nil
	}
	switch format {
	case FormatArray:
		return readArrayServers(data)
	case FormatHermes:
		return readHermesServers(data)
	case FormatOpenCode:
		return readOpenCodeServers(data)
	default:
		// FormatMcpServers: {"mcpServers": {...}}.
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

// readOpenCodeServers reads official root["mcp"]. If a legacy file still
// has only "mcpServers" (older Pharos writes) and no "mcp", that map is
// returned so RemoveServer / MergeServer can migrate it on write.
func readOpenCodeServers(data []byte) (map[string]json.RawMessage, error) {
	root := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(data))) == 0 {
		return make(map[string]json.RawMessage), nil
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if raw, ok := root[keyMcp]; ok {
		servers, err := unmarshalServerMap(raw)
		if err != nil {
			return nil, fmt.Errorf("parse mcp: %w", err)
		}
		return servers, nil
	}
	if raw, ok := root[keyMcpServers]; ok {
		servers, err := unmarshalServerMap(raw)
		if err != nil {
			return nil, fmt.Errorf("parse mcpServers: %w", err)
		}
		return servers, nil
	}
	return make(map[string]json.RawMessage), nil
}

func unmarshalServerMap(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(raw, &servers); err != nil {
		return nil, err
	}
	if servers == nil {
		servers = make(map[string]json.RawMessage)
	}
	return servers, nil
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
	ClientClaudeDesktop, ClientClaudeCode, ClientCursor, ClientGeneric,
	ClientCline, ClientOpenCode, ClientHermes,
	ClientVSCode, ClientWindsurf, ClientGemini, ClientAmazonQ, ClientRooCode,
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

// RemoveServer deletes name from the client's MCP server list and writes
// the file back as a patch. Unknown top-level keys are preserved.
// Hermes YAML is patched in place; array-format files stay a bare array.
func RemoveServer(c Client, name string) error {
	format := c.Format
	if format == "" {
		format = FormatMcpServers
	}
	switch format {
	case FormatArray:
		return removeArrayServer(c.Path, name)
	case FormatHermes:
		_, err := removeHermesServer(c.Path, name)
		return err
	case FormatOpenCode:
		servers, err := ReadServersFormat(c.Path, format)
		if err != nil {
			return err
		}
		delete(servers, name)
		return PatchOpenCodeMcp(c.Path, servers)
	default:
		servers, err := ReadServersFormat(c.Path, format)
		if err != nil {
			return err
		}
		delete(servers, name)
		return PatchMcpServers(c.Path, servers)
	}
}

// removeArrayServer drops a named entry from a bare JSON array config.
func removeArrayServer(path, name string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var entries []arrayEntry
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &entries); err != nil {
			return fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	kept := make([]arrayEntry, 0, len(entries))
	for _, e := range entries {
		if e.Name != name {
			kept = append(kept, e)
		}
	}
	return writeArrayConfig(path, kept)
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
	format := FormatMcpServers
	for _, c := range candidatePaths() {
		if c.ID == id {
			format = c.Format
			break
		}
	}
	return loadFromPath(id, path, format)
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
			// OpenCode stores command as [bin, ...args].
			if cmds, ok := m["command"].([]any); ok {
				for i, a := range cmds {
					str, ok := a.(string)
					if !ok {
						continue
					}
					if i == 0 && s.Command == "" {
						s.Command = str
					} else {
						s.Args = append(s.Args, str)
					}
				}
			}
			if v, ok := m["url"].(string); ok {
				s.URL = v
			}
			if v, ok := m["type"].(string); ok {
				s.Type = v
			}
			if args, ok := m["args"].([]any); ok && len(s.Args) == 0 {
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
