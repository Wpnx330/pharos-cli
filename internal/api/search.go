package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// SearchResult is a single package returned by the search endpoint.
// Transport and SourceRegistry are present on live registry hits
// (transport is an array of strings, e.g. ["stdio"]). Version is
// printed as received — synced metadata-only rows often send "0.0.0".
type SearchResult struct {
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Score          float64  `json:"score"`
	Downloads      int64    `json:"downloads30d"`
	Transport      []string `json:"transport"`
	SourceRegistry string   `json:"source_registry"`
}

// SearchResponse is the response envelope for the search endpoint.
type SearchResponse struct {
	Results    []SearchResult `json:"results"`
	NextCursor string         `json:"nextCursor"`
	Total      int            `json:"total"`
}

// SearchParams holds query, pagination, and filter arguments for GET /v1/search.
// Live search uses cursor pagination (base64 offset), not page=.
type SearchParams struct {
	Query     string
	Page      int
	Limit     int
	Registry  string
	Transport string
}

// Search queries the registry for packages matching the given query.
// Page is converted to a cursor offset: (page-1)*limit. Page <= 1 omits cursor.
// Empty Registry / Transport values are treated as unset and omitted.
func (c *Client) Search(params SearchParams) (*SearchResponse, error) {
	path := "/v1/search?q=" + encodeQuery(params.Query)
	if params.Limit > 0 {
		path += fmtLimitParam(params.Limit)
	}
	if cursor := pageCursor(params.Page, params.Limit); cursor != "" {
		path += "&cursor=" + encodeQuery(cursor)
	}
	if registry := strings.TrimSpace(params.Registry); registry != "" {
		path += "&registry=" + encodeQuery(registry)
	}
	if transport := strings.TrimSpace(params.Transport); transport != "" {
		path += "&transport=" + encodeQuery(transport)
	}
	data, err := c.get(path)
	if err != nil {
		return nil, err
	}
	var resp SearchResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// pageCursor converts a 1-based page number into the registry's opaque
// offset cursor. Page <= 1 (or a non-positive offset) omits the cursor.
func pageCursor(page, limit int) string {
	if page <= 1 || limit <= 0 {
		return ""
	}
	offset := (page - 1) * limit
	if offset <= 0 {
		return ""
	}
	return encodeCursor(offset)
}

// decodeCursor decodes a base64-encoded offset string. An empty cursor yields
// offset 0. Must stay identical to the registry store implementation.
func decodeCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("negative offset")
	}
	return n, nil
}

// encodeCursor encodes an integer offset as an opaque base64 string.
// Must stay identical to the registry store implementation.
func encodeCursor(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

// fmtLimitParam formats the limit query parameter.
func fmtLimitParam(limit int) string {
	return "&limit=" + itoa(limit)
}

// itoa converts an int to its decimal string representation without
// importing strconv (keeps this file self-contained for clarity).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
