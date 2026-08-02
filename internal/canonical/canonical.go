// Package canonical manages ~/.pharos/mcp.json — the Pharos canonical
// MCP server configuration file. This file is the single source of truth
// for which MCP servers are installed via Pharos and how they are
// configured. Agent makers and harness builders can read this file
// directly instead of maintaining their own config format.
//
// The canonical file is written on every `pharos install` and updated
// on every `pharos remove`. Client-specific configs (Cursor, Claude
// Desktop, etc.) are synced FROM this file.
package canonical

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// SchemaURL is the published JSON schema for IDE autocomplete.
const SchemaURL = "https://getpharos.dev/schema/mcp.json"

// CurrentVersion is the current canonical config schema version.
const CurrentVersion = 1

// Config is the top-level structure of ~/.pharos/mcp.json.
type Config struct {
	Schema  string             `json:"$schema"`
	Version int                `json:"version"`
	Servers map[string]Server  `json:"servers"`
}

// Server is a single MCP server entry in the canonical config.
type Server struct {
	Transport string         `json:"transport"`           // "stdio" | "http-sse" | "streamable-http"
	Command   string         `json:"command,omitempty"`    // executable for stdio; empty for pure remote
	Args      []string       `json:"args,omitempty"`       // arguments for stdio
	Env       map[string]string `json:"env,omitempty"`     // environment variables; supports ${secret:NAME}
	URL       string         `json:"url,omitempty"`        // URL for remote servers; empty for stdio
	Cwd       string         `json:"cwd,omitempty"`        // working directory for local servers
	Package   PackageInfo    `json:"package"`              // provenance metadata
	Enabled   bool           `json:"enabled"`              // if false, agents should not start this server
	InstalledAt string       `json:"installedAt"`          // ISO 8601 timestamp
}

// PackageInfo tracks where the server came from and its integrity.
type PackageInfo struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Integrity string `json:"integrity,omitempty"` // sha512 hash; empty for pure remote
	Source    string `json:"source"`              // "pharos" | "synced:github" etc.
}

// FilePath returns the absolute path to ~/.pharos/mcp.json.
func FilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, ".pharos", "mcp.json"), nil
}

// Load reads the canonical config file. If the file does not exist,
// returns an empty Config (not an error).
func Load() (*Config, error) {
	path, err := FilePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{
				Schema:  SchemaURL,
				Version: CurrentVersion,
				Servers: make(map[string]Server),
			}, nil
		}
		return nil, fmt.Errorf("read canonical config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse canonical config: %w", err)
	}

	if cfg.Servers == nil {
		cfg.Servers = make(map[string]Server)
	}
	if cfg.Schema == "" {
		cfg.Schema = SchemaURL
	}
	if cfg.Version == 0 {
		cfg.Version = CurrentVersion
	}

	return &cfg, nil
}

// Save writes the canonical config file, creating directories as needed.
func Save(cfg *Config) error {
	path, err := FilePath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	cfg.Schema = SchemaURL
	cfg.Version = CurrentVersion

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal canonical config: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write canonical config: %w", err)
	}

	return nil
}

// AddServer adds or replaces a server entry in the canonical config.
// If a server with the same name already exists, it is overwritten.
func AddServer(name string, srv Server) error {
	cfg, err := Load()
	if err != nil {
		return err
	}

	if srv.InstalledAt == "" {
		srv.InstalledAt = time.Now().UTC().Format(time.RFC3339)
	}
	if srv.Package.Source == "" {
		srv.Package.Source = "pharos"
	}

	cfg.Servers[name] = srv
	return Save(cfg)
}

// RemoveServer removes a server entry from the canonical config.
// Returns true if the server was found and removed, false if it wasn't present.
func RemoveServer(name string) (bool, error) {
	cfg, err := Load()
	if err != nil {
		return false, err
	}

	if _, exists := cfg.Servers[name]; !exists {
		return false, nil
	}

	delete(cfg.Servers, name)
	return true, Save(cfg)
}

// GetServer returns a server entry by name, or nil if not found.
func GetServer(name string) (*Server, error) {
	cfg, err := Load()
	if err != nil {
		return nil, err
	}

	srv, exists := cfg.Servers[name]
	if !exists {
		return nil, nil
	}
	return &srv, nil
}

// ListServers returns all server names in the canonical config, sorted
// alphabetically.
func ListServers() ([]string, error) {
	cfg, err := Load()
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// SetEnabled toggles the enabled flag for a server. This allows agent
// software to enable/disable a server without stopping it.
func SetEnabled(name string, enabled bool) error {
	cfg, err := Load()
	if err != nil {
		return err
	}

	srv, exists := cfg.Servers[name]
	if !exists {
		return fmt.Errorf("server %q not found in canonical config", name)
	}

	srv.Enabled = enabled
	cfg.Servers[name] = srv
	return Save(cfg)
}
