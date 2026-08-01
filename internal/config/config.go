// Package config manages the PHAROS CLI configuration stored at
// ~/.pharos/config.json. It persists the registry base URL and an
// optional authentication token used by the publish command.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config is the on-disk configuration model.
type Config struct {
	Registry      string         `json:"registry"`
	Token         string         `json:"token,omitempty"`
	CustomClients []CustomClient `json:"custom_clients,omitempty"`
}

// CustomClient represents a user-registered MCP client that is not
// auto-detected by the built-in detection logic.
type CustomClient struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Format string `json:"format"`
}

// DefaultRegistry is the default PHAROS registry base URL.
const DefaultRegistry = "https://getpharos.dev"

// Default returns a Config populated with default values.
func Default() *Config {
	return &Config{
		Registry: DefaultRegistry,
	}
}

// configPath returns the absolute path to the config file. It respects
// the HOME environment variable so tests can override it.
func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pharos", "config.json"), nil
}

// Load reads the configuration from disk. If the file does not exist
// a default config is returned without error.
func Load() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if c.Registry == "" {
		c.Registry = DefaultRegistry
	}
	return &c, nil
}

// Save writes the configuration to disk, creating parent directories
// as needed.
func (c *Config) Save() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Get returns the value for the given config key, or an error if the
// key is unknown.
func (c *Config) Get(key string) (string, error) {
	switch key {
	case "registry":
		return c.Registry, nil
	case "token":
		return c.Token, nil
	default:
		return "", fmt.Errorf("unknown config key: %q", key)
	}
}

// Set assigns a value to the given config key.
func (c *Config) Set(key, value string) error {
	switch key {
	case "registry":
		c.Registry = value
		return nil
	case "token":
		c.Token = value
		return nil
	default:
		return fmt.Errorf("unknown config key: %q", key)
	}
}

// AddCustomClient registers a custom MCP client. If a client with the
// same ID already exists, it is replaced. Format defaults to "mcpServers"
// when empty.
func (c *Config) AddCustomClient(id, path, format string) error {
	if id == "" {
		return fmt.Errorf("client id cannot be empty")
	}
	if path == "" {
		return fmt.Errorf("client path cannot be empty")
	}
	if format == "" {
		format = "mcpServers"
	}
	if format != "mcpServers" && format != "array" {
		return fmt.Errorf("unknown format %q: must be \"mcpServers\" or \"array\"", format)
	}
	cc := CustomClient{ID: id, Path: path, Format: format}
	for i, existing := range c.CustomClients {
		if existing.ID == id {
			c.CustomClients[i] = cc
			return nil
		}
	}
	c.CustomClients = append(c.CustomClients, cc)
	return nil
}

// RemoveCustomClient removes a custom client by ID. Returns true if the
// client was found and removed.
func (c *Config) RemoveCustomClient(id string) bool {
	for i, cc := range c.CustomClients {
		if cc.ID == id {
			c.CustomClients = append(c.CustomClients[:i], c.CustomClients[i+1:]...)
			return true
		}
	}
	return false
}

// GetCustomClient returns a pointer to the custom client with the given
// ID, or nil if not found.
func (c *Config) GetCustomClient(id string) *CustomClient {
	for i := range c.CustomClients {
		if c.CustomClients[i].ID == id {
			return &c.CustomClients[i]
		}
	}
	return nil
}
