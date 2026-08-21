package cmd

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/api"
)

// This file is the end-to-end half of the search tests: the ones in
// search_test.go cover rendering helpers in isolation, these drive the actual
// `pharos search` command — flag parsing, the request it builds, and what it
// prints — against a stand-in registry. No network, so they run in CI.

// searchCorpus is the fixture catalog the stand-in registry filters over.
var searchCorpus = []api.SearchResult{
	{Name: "echo-http", Version: "1.0.0", Description: "echo over http", Transport: []string{"http"}, SourceRegistry: "pharos"},
	{Name: "echo-http-mirror", Version: "1.1.0", Description: "echo over http", Transport: []string{"http"}, SourceRegistry: "mcp.io"},
	{Name: "echo-stdio", Version: "2.0.0", Description: "echo over stdio", Transport: []string{"stdio"}, SourceRegistry: "pharos"},
	{Name: "echo-dual", Version: "3.0.0", Description: "echo over both", Transport: []string{"stdio", "http"}, SourceRegistry: "mcp.io"},
	{Name: "echo-legacy", Version: "0.0.1", Description: "echo, no metadata", SourceRegistry: "mcp.io"},
}

// startTestRegistry serves /v1/search over searchCorpus, applying the same
// filters the live registry does, and records every query it was asked.
func startTestRegistry(t *testing.T, queries *[]url.Values) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		*queries = append(*queries, q)

		var hits []api.SearchResult
		for _, pkg := range searchCorpus {
			if transport := q.Get("transport"); transport != "" && !hasTransport(pkg, transport) {
				continue
			}
			if registry := q.Get("registry"); registry != "" && pkg.SourceRegistry != registry {
				continue
			}
			hits = append(hits, pkg)
		}

		offset := decodeTestCursor(q.Get("cursor"))
		limit := len(hits)
		if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 {
			limit = v
		}
		total := len(hits)
		if offset > len(hits) {
			offset = len(hits)
		}
		hits = hits[offset:]
		next := ""
		if len(hits) > limit {
			hits = hits[:limit]
			next = base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset + limit)))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.SearchResponse{Results: hits, NextCursor: next, Total: total})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func hasTransport(pkg api.SearchResult, want string) bool {
	for _, tr := range pkg.Transport {
		if tr == want {
			return true
		}
	}
	return false
}

func decodeTestCursor(cursor string) int {
	if cursor == "" {
		return 0
	}
	raw, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// pointCLIAtRegistry writes a config.json naming the given registry into a
// temporary HOME, which is what config.Load reads.
func pointCLIAtRegistry(t *testing.T, registryURL string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	dir := filepath.Join(home, ".pharos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"registry":` + strconv.Quote(registryURL) + `}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runSearch executes `pharos search ...` and returns what it printed. The
// search flags are package-level vars that cobra does not reset between runs,
// so they are restored afterwards.
func runSearch(t *testing.T, args ...string) string {
	t.Helper()
	limit, page, registry, transport, asJSON := searchLimit, searchPage, searchRegistry, searchTransport, jsonFlag
	t.Cleanup(func() {
		searchLimit, searchPage, searchRegistry, searchTransport, jsonFlag = limit, page, registry, transport, asJSON
		rootCmd.SetArgs(nil)
	})

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w

	rootCmd.SetArgs(append([]string{"search"}, args...))
	execErr := rootCmd.Execute()

	os.Stdout = stdout
	_ = w.Close()
	out, _ := io.ReadAll(r)
	_ = r.Close()

	if execErr != nil {
		t.Fatalf("pharos search %s: %v", strings.Join(args, " "), execErr)
	}
	return string(out)
}

func TestSearchFilters(t *testing.T) {
	t.Run("transport filter reaches the registry and narrows the results", func(t *testing.T) {
		var queries []url.Values
		srv := startTestRegistry(t, &queries)
		pointCLIAtRegistry(t, srv.URL)

		out := runSearch(t, "echo", "--transport", "http", "--json")

		if len(queries) != 1 {
			t.Fatalf("expected 1 registry request, got %d", len(queries))
		}
		if got := queries[0].Get("transport"); got != "http" {
			t.Errorf("registry saw transport=%q, want %q", got, "http")
		}

		for _, r := range decodeResults(t, out) {
			if !hasTransport(r, "http") {
				t.Errorf("%s came back for --transport http with transports %v", r.Name, r.Transport)
			}
		}
		if strings.Contains(out, "echo-stdio") {
			t.Error("a stdio-only package survived --transport http")
		}
	})

	t.Run("registry filter reaches the registry and narrows the results", func(t *testing.T) {
		var queries []url.Values
		srv := startTestRegistry(t, &queries)
		pointCLIAtRegistry(t, srv.URL)

		out := runSearch(t, "echo", "--registry", "pharos", "--json")

		if got := queries[0].Get("registry"); got != "pharos" {
			t.Errorf("registry saw registry=%q, want %q", got, "pharos")
		}
		results := decodeResults(t, out)
		if len(results) == 0 {
			t.Fatal("no results for --registry pharos")
		}
		for _, r := range results {
			if r.SourceRegistry != "pharos" {
				t.Errorf("%s came back for --registry pharos but is from %q", r.Name, r.SourceRegistry)
			}
		}
	})

	t.Run("the two filters compose", func(t *testing.T) {
		var queries []url.Values
		srv := startTestRegistry(t, &queries)
		pointCLIAtRegistry(t, srv.URL)

		out := runSearch(t, "echo", "--transport", "http", "--registry", "mcp.io", "--json")

		if queries[0].Get("transport") != "http" || queries[0].Get("registry") != "mcp.io" {
			t.Fatalf("registry saw %v, want both filters", queries[0])
		}
		for _, r := range decodeResults(t, out) {
			if !hasTransport(r, "http") || r.SourceRegistry != "mcp.io" {
				t.Errorf("%s (%v, %s) does not satisfy both filters", r.Name, r.Transport, r.SourceRegistry)
			}
		}
	})

	t.Run("page 2 returns a different window than page 1", func(t *testing.T) {
		var queries []url.Values
		srv := startTestRegistry(t, &queries)
		pointCLIAtRegistry(t, srv.URL)

		first := decodeResults(t, runSearch(t, "echo", "-n", "2", "--json"))
		second := decodeResults(t, runSearch(t, "echo", "-n", "2", "-p", "2", "--json"))

		if queries[0].Get("cursor") != "" {
			t.Errorf("page 1 sent cursor=%q, want none", queries[0].Get("cursor"))
		}
		if got := decodeTestCursor(queries[1].Get("cursor")); got != 2 {
			t.Errorf("page 2 sent cursor offset %d, want 2", got)
		}
		if len(first) == 0 || len(second) == 0 {
			t.Fatalf("page 1 returned %d results, page 2 returned %d", len(first), len(second))
		}
		for _, a := range first {
			for _, b := range second {
				if a.Name == b.Name {
					t.Errorf("%s appears on both page 1 and page 2", a.Name)
				}
			}
		}
	})

	t.Run("no filters sends no filter params", func(t *testing.T) {
		var queries []url.Values
		srv := startTestRegistry(t, &queries)
		pointCLIAtRegistry(t, srv.URL)

		out := runSearch(t, "echo", "--json")

		if queries[0].Has("transport") || queries[0].Has("registry") {
			t.Errorf("unfiltered search sent filter params: %v", queries[0])
		}
		if len(decodeResults(t, out)) != len(searchCorpus) {
			t.Errorf("unfiltered search should return the whole corpus, got %s", out)
		}
	})

	t.Run("the table output honours the filter too", func(t *testing.T) {
		var queries []url.Values
		srv := startTestRegistry(t, &queries)
		pointCLIAtRegistry(t, srv.URL)

		out := runSearch(t, "echo", "--transport", "stdio")

		if !strings.Contains(out, "echo-stdio") {
			t.Errorf("stdio package missing from the table: %s", out)
		}
		if strings.Contains(out, "echo-http-mirror") {
			t.Errorf("http-only package rendered under --transport stdio: %s", out)
		}
	})
}

// decodeResults parses the --json envelope the search command prints.
func decodeResults(t *testing.T, out string) []api.SearchResult {
	t.Helper()
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("no JSON in search output: %q", out)
	}
	var resp api.SearchResponse
	if err := json.Unmarshal([]byte(out[start:]), &resp); err != nil {
		t.Fatalf("search --json did not print valid JSON (%v): %s", err, out)
	}
	return resp.Results
}
