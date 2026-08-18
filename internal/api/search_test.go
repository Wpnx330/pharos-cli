package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// productionSearchHitJSON matches live GET /v1/search hits
// (transport is an array; source_registry is a string).
const productionSearchHitJSON = `{
  "name": "MoodMNKY MCP Server Stack",
  "version": "0.0.0",
  "title": "MoodMNKY",
  "description": "MoodMNKY MCP server stack",
  "score": 1.2,
  "downloads30d": 0,
  "transport": ["stdio"],
  "source_registry": "mcp.io"
}`

const searchHitMissingTransportJSON = `{
  "name": "legacy-hit",
  "version": "1.2.3",
  "title": "Legacy",
  "description": "no transport or registry keys",
  "score": 0.1,
  "downloads30d": 7
}`

func TestSearchResultUnmarshalProductionTransportAndRegistry(t *testing.T) {
	var got SearchResult
	if err := json.Unmarshal([]byte(productionSearchHitJSON), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "MoodMNKY MCP Server Stack" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Version != "0.0.0" {
		t.Errorf("Version = %q, want 0.0.0 as sent by the API (do not invent a version)", got.Version)
	}
	if len(got.Transport) != 1 || got.Transport[0] != "stdio" {
		t.Errorf("Transport = %#v, want [stdio]", got.Transport)
	}
	if got.SourceRegistry != "mcp.io" {
		t.Errorf("SourceRegistry = %q, want mcp.io", got.SourceRegistry)
	}
	if got.Downloads != 0 {
		t.Errorf("Downloads = %d, want 0", got.Downloads)
	}
}

func TestSearchResultUnmarshalMissingTransportAndRegistry(t *testing.T) {
	var got SearchResult
	if err := json.Unmarshal([]byte(searchHitMissingTransportJSON), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "legacy-hit" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Version != "1.2.3" {
		t.Errorf("Version = %q", got.Version)
	}
	if len(got.Transport) != 0 {
		t.Errorf("Transport = %#v, want empty when key is absent", got.Transport)
	}
	if got.SourceRegistry != "" {
		t.Errorf("SourceRegistry = %q, want empty when key is absent", got.SourceRegistry)
	}
}

func TestSearchResponseUnmarshalResultsEnvelope(t *testing.T) {
	raw := `{
  "results": [` + productionSearchHitJSON + `],
  "nextCursor": "",
  "total": 1
}`
	var resp SearchResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Total != 1 || len(resp.Results) != 1 {
		t.Fatalf("envelope total=%d results=%d", resp.Total, len(resp.Results))
	}
	if resp.Results[0].SourceRegistry != "mcp.io" {
		t.Errorf("nested SourceRegistry = %q", resp.Results[0].SourceRegistry)
	}
}

func TestEncodeDecodeCursorRoundTrip(t *testing.T) {
	for _, offset := range []int{0, 1, 10, 20, 100, 9999} {
		got, err := decodeCursor(encodeCursor(offset))
		if err != nil {
			t.Fatalf("decodeCursor(encodeCursor(%d)): %v", offset, err)
		}
		if got != offset {
			t.Errorf("round-trip offset = %d, want %d", got, offset)
		}
	}
}

func TestDecodeCursorEmptyIsZero(t *testing.T) {
	got, err := decodeCursor("")
	if err != nil {
		t.Fatalf("decodeCursor(\"\"): %v", err)
	}
	if got != 0 {
		t.Errorf("decodeCursor(\"\") = %d, want 0", got)
	}
}

func TestEncodeCursorOffset10MatchesRegistry(t *testing.T) {
	// registry encodeCursor is StdEncoding of the decimal offset digits.
	got := encodeCursor(10)
	if got != "MTA=" {
		t.Errorf("encodeCursor(10) = %q, want MTA=", got)
	}
	n, err := decodeCursor("MTA=")
	if err != nil {
		t.Fatalf("decodeCursor(MTA=): %v", err)
	}
	if n != 10 {
		t.Errorf("decodeCursor(MTA=) = %d, want 10", n)
	}
}

func TestDecodeCursorRejectsNegativeAndGarbage(t *testing.T) {
	if _, err := decodeCursor("!!!"); err == nil {
		t.Error("decodeCursor(invalid base64) should error")
	}
	notNum := encodeCursor(0)[:0] + "YQ==" // "a"
	if _, err := decodeCursor(notNum); err == nil {
		t.Error("decodeCursor(non-numeric) should error")
	}
	neg := encodeCursor(-1)
	if _, err := decodeCursor(neg); err == nil {
		t.Error("decodeCursor(negative offset) should error")
	}
}

func TestSearchPage1OmitsCursor(t *testing.T) {
	gotPath := captureSearchPath(t, SearchParams{Query: "filesystem", Page: 1, Limit: 10})
	if strings.Contains(gotPath, "cursor=") {
		t.Errorf("page 1 path %q must not include cursor=", gotPath)
	}
	if strings.Contains(gotPath, "page=") {
		t.Errorf("path %q must not send page= (live /v1/search ignores it)", gotPath)
	}
	if !strings.Contains(gotPath, "q=filesystem") {
		t.Errorf("path %q missing q=filesystem", gotPath)
	}
}

func TestSearchPage2Limit10CursorIsOffset10(t *testing.T) {
	gotPath := captureSearchPath(t, SearchParams{Query: "filesystem", Page: 2, Limit: 10})
	if strings.Contains(gotPath, "page=") {
		t.Errorf("path %q must not send page=", gotPath)
	}
	u, err := url.Parse(gotPath)
	if err != nil {
		t.Fatalf("parse path %q: %v", gotPath, err)
	}
	cursor := u.Query().Get("cursor")
	if cursor == "" {
		t.Fatalf("page 2 path %q missing cursor=", gotPath)
	}
	offset, err := decodeCursor(cursor)
	if err != nil {
		t.Fatalf("decodeCursor(%q): %v", cursor, err)
	}
	if offset != 10 {
		t.Errorf("page 2 limit 10 cursor offset = %d, want 10 (path %q)", offset, gotPath)
	}
	if cursor != encodeCursor(10) {
		t.Errorf("cursor = %q, want encodeCursor(10)=%q", cursor, encodeCursor(10))
	}
}

func TestSearchPage3Limit10CursorIsOffset20(t *testing.T) {
	gotPath := captureSearchPath(t, SearchParams{Query: "filesystem", Page: 3, Limit: 10})
	u, err := url.Parse(gotPath)
	if err != nil {
		t.Fatalf("parse path %q: %v", gotPath, err)
	}
	offset, err := decodeCursor(u.Query().Get("cursor"))
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if offset != 20 {
		t.Errorf("page 3 limit 10 cursor offset = %d, want 20", offset)
	}
}

func TestSearchRegistryAndTransportQueryParams(t *testing.T) {
	gotPath := captureSearchPath(t, SearchParams{
		Query:     "filesystem",
		Page:      1,
		Limit:     10,
		Registry:  "mcp.io",
		Transport: "stdio",
	})
	u, err := url.Parse(gotPath)
	if err != nil {
		t.Fatalf("parse path %q: %v", gotPath, err)
	}
	q := u.Query()
	if q.Get("registry") != "mcp.io" {
		t.Errorf("registry = %q, want mcp.io (path %q)", q.Get("registry"), gotPath)
	}
	if q.Get("transport") != "stdio" {
		t.Errorf("transport = %q, want stdio (path %q)", q.Get("transport"), gotPath)
	}
}

func TestSearchOmitsEmptyRegistryAndTransport(t *testing.T) {
	gotPath := captureSearchPath(t, SearchParams{
		Query:     "filesystem",
		Registry:  "",
		Transport: "",
	})
	if strings.Contains(gotPath, "registry=") {
		t.Errorf("empty registry must omit param, path %q", gotPath)
	}
	if strings.Contains(gotPath, "transport=") {
		t.Errorf("empty transport must omit param, path %q", gotPath)
	}
}

func captureSearchPath(t *testing.T, params SearchParams) string {
	t.Helper()
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[],"nextCursor":"","total":0}`))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "")
	if _, err := c.Search(params); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotPath == "" {
		t.Fatal("handler was not called")
	}
	return gotPath
}
