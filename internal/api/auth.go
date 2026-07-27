package api

import (
	"bytes"
	"encoding/json"
	"net/http"
)

// CurrentUser is the response from GET /v1/auth/me.
type CurrentUser struct {
	ID         string   `json:"sub"`
	Username   string   `json:"namespace"`
	Scope      string   `json:"scope,omitempty"`
	GitHubID   int64    `json:"github_id,omitempty"`
	Email      string   `json:"email,omitempty"`
	AvatarURL  string   `json:"avatar_url,omitempty"`
	Namespaces []string `json:"namespaces,omitempty"`
}

// GetCurrentUser calls GET /v1/auth/me with the configured bearer token.
func (c *Client) GetCurrentUser() (*CurrentUser, error) {
	data, err := c.get("/v1/auth/me")
	if err != nil {
		return nil, err
	}
	var u CurrentUser
	if err := json.Unmarshal(data, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// LoginResponse is the token response from the OAuth callback exchange.
type LoginResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// ExchangeCodeForToken posts an OAuth code to the registry and receives
// a JWT token back. This is used by the login callback flow.
func (c *Client) ExchangeCodeForToken(code string) (*LoginResponse, error) {
	body, _ := json.Marshal(map[string]string{"code": code})
	data, _, err := c.do(http.MethodPost, "/v1/auth/github/callback", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var lr LoginResponse
	if err := json.Unmarshal(data, &lr); err != nil {
		return nil, err
	}
	return &lr, nil
}
