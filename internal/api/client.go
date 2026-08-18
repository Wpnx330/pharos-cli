// Package api implements the HTTP client for the PHAROS registry API.
package api

import (
	"bytes"
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

// Post is a convenience wrapper for POST requests with a JSON body.
// It returns the raw response body.
func (c *Client) Post(path string, body []byte) ([]byte, error) {
	data, _, err := c.do(http.MethodPost, path, bytes.NewReader(body))
	return data, err
}

// APIError represents a non-2xx HTTP response from the registry.
type APIError struct {
	StatusCode int
	Body       []byte
}

// Error implements the error interface.
func (e *APIError) Error() string {
	// The registry sends errors as {"error": {"code": "...", "message": "..."}}.
	var nested struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(e.Body, &nested) == nil && nested.Error.Message != "" {
		return fmt.Sprintf("API error (%d): %s", e.StatusCode, nested.Error.Message)
	}
	// Fallback for flat error shapes.
	var flat struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(e.Body, &flat) == nil && (flat.Error != "" || flat.Message != "") {
		if flat.Error != "" {
			return fmt.Sprintf("API error (%d): %s", e.StatusCode, flat.Error)
		}
		return fmt.Sprintf("API error (%d): %s", e.StatusCode, flat.Message)
	}
	return fmt.Sprintf("API error (%d): %s", e.StatusCode, string(e.Body))
}

// encodeQuery URL-encodes a query string value.
func encodeQuery(s string) string {
	return url.QueryEscape(s)
}

// packagePath normalizes a package name into the URL path that the
// registry's chi router can match, handling both unscoped ("foo-bar")
// and scoped ("@scope/name" or "scope/name") names.
// Scoped names are prefixed with "@" so they match the scoped route
// /@{scope}/{name}.
//
// Each path segment is escaped with url.PathEscape so spaces and other
// package-ID characters reach the server. "/" is kept as the scope
// separator and is never escaped as a single blob — otherwise
// io.github.j0hanz/filesystem-mcp would miss the scoped route.
// Callers prepend their own resource prefix (e.g. "/v1/packages/").
func packagePath(name string) string {
	// Title-shaped package IDs contain spaces (and sometimes a slash
	// inside parentheses). Treat them as one unscoped segment so `/`
	// is %2F and chi stays on /packages/{name}.
	if strings.Contains(name, " ") {
		return url.PathEscape(name)
	}
	if !strings.HasPrefix(name, "@") {
		if idx := strings.Index(name, "/"); idx > 0 {
			// Unprefixed scope: scope/name → @scope/name
			name = "@" + name
		}
	}
	parts := strings.Split(name, "/")
	for i, part := range parts {
		// Keep a leading @ literal so the path matches /@{scope}/{name}.
		if i == 0 && strings.HasPrefix(part, "@") {
			parts[i] = "@" + url.PathEscape(part[1:])
			continue
		}
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}
