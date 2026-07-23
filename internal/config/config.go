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
	Registry string `json:"registry"`
	Token    string `json:"token,omitempty"`
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
