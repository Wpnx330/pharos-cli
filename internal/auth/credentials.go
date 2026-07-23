// Package auth manages stored credentials at ~/.pharos/credentials.json.
package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Credentials is the on-disk credential model.
type Credentials struct {
	Token     string `json:"token"`
	Username  string `json:"username,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	StoredAt  string `json:"stored_at"`
}

// credentialsPath returns the path to the credentials file. It respects
// the HOME environment variable for test isolation.
func credentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pharos", "credentials.json"), nil
}

// Load reads stored credentials. Returns an error if the file does not
// exist so callers can distinguish "not logged in" from a parse error.
func Load() (*Credentials, error) {
	path, err := credentialsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no stored credentials (run `pharos login`): %w", err)
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	return &c, nil
}

// Save writes credentials to disk, creating parent directories.
func Save(c *Credentials) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	c.StoredAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Clear removes the credentials file if it exists.
func Clear() error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Token returns just the token string from stored credentials, or empty
// string if not logged in.
func Token() string {
	c, err := Load()
	if err != nil {
		return ""
	}
	return c.Token
}
