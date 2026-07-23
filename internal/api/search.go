package api

import "encoding/json"

// SearchResult is a single package returned by the search endpoint.
type SearchResult struct {
	Name        string  `json:"name"`
	Version     string  `json:"version"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Score       float64 `json:"score"`
	Downloads   int64   `json:"downloads30d"`
}

// SearchResponse is the response envelope for the search endpoint.
type SearchResponse struct {
	Results    []SearchResult `json:"results"`
	NextCursor string         `json:"nextCursor"`
	Total      int            `json:"total"`
}

// Search queries the registry for packages matching the given query.
// page and limit control pagination.
func (c *Client) Search(query string, page, limit int) (*SearchResponse, error) {
	path := "/v1/search?q=" + encodeQuery(query)
	if page > 0 {
		path += fmtPageParam(page)
	}
	if limit > 0 {
		path += fmtLimitParam(limit)
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

// fmtPageParam formats the page query parameter.
func fmtPageParam(page int) string {
	return "&page=" + itoa(page)
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
