// Package api implements the HTTP client for the PHAROS registry API.
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is the PHAROS registry API client.
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// New creates a Client configured with the given base URL and optional
// auth token.
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// do performs an HTTP request and returns the raw response body.
// It sets the Authorization header when a token is configured.
func (c *Client) do(method, path string, body io.Reader) ([]byte, int, error) {
	u := c.BaseURL + path
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, 0, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode >= 400 {
		return data, resp.StatusCode, &APIError{
			StatusCode: resp.StatusCode,
			Body:       data,
		}
	}
	return data, resp.StatusCode, nil
}

// get is a convenience wrapper for GET requests.
func (c *Client) get(path string) ([]byte, error) {
	data, _, err := c.do(http.MethodGet, path, nil)
	return data, err
}

// APIError represents a non-2xx HTTP response from the registry.
type APIError struct {
	StatusCode int
	Body       []byte
}

// Error implements the error interface.
func (e *APIError) Error() string {
	var m struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(e.Body, &m) == nil && (m.Error != "" || m.Message != "") {
		if m.Error != "" {
			return fmt.Sprintf("API error (%d): %s", e.StatusCode, m.Error)
		}
		return fmt.Sprintf("API error (%d): %s", e.StatusCode, m.Message)
	}
	return fmt.Sprintf("API error (%d): %s", e.StatusCode, string(e.Body))
}

// encodeQuery URL-encodes a query string value.
func encodeQuery(s string) string {
	return url.QueryEscape(s)
}
