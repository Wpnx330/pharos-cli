package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSearch makes a mock search request and verifies parsing.
func TestSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/search" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("q") != "mcp" {
			t.Errorf("unexpected query: %s", r.URL.Query().Get("q"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"results": [
				{"name":"flight-mcp-server","version":"1.0.0","title":"Flight","description":"Flight tracking","downloads30d":42}
			],
			"nextCursor": "",
			"total": 1
		}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	resp, err := c.Search("mcp", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(resp.Results))
	}
	r := resp.Results[0]
	if r.Name != "flight-mcp-server" {
		t.Errorf("name = %s", r.Name)
	}
	if r.Version != "1.0.0" {
		t.Errorf("version = %s", r.Version)
	}
	if r.Downloads != 42 {
		t.Errorf("downloads = %d", r.Downloads)
	}
}

// TestSearchError verifies that HTTP errors are propagated.
func TestSearchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	_, err := c.Search("mcp", 1, 10)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestGetPackage verifies package detail parsing.
func TestGetPackage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/packages/flight-mcp-server" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"name":"flight-mcp-server",
			"title":"Flight MCP Server",
			"description":"Flight tracking MCP",
			"license":"MIT",
			"repo_url":"https://github.com/u/r",
			"dist_tags":{"latest":"1.1.0"},
			"versions":[
				{"version":"1.0.0","manifest":{"name":"x","version":"1.0.0","transport":"stdio"},"deprecated":false,"status":"active","created_at":"2026-01-01T00:00:00Z"},
				{"version":"1.1.0","manifest":{"name":"x","version":"1.1.0","transport":"stdio"},"deprecated":false,"status":"active","created_at":"2026-01-02T00:00:00Z"}
			]
		}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	pkg, err := c.GetPackage("flight-mcp-server")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "flight-mcp-server" {
		t.Errorf("name = %s", pkg.Name)
	}
	if pkg.License != "MIT" {
		t.Errorf("license = %s", pkg.License)
	}
	if pkg.DistTags["latest"] != "1.1.0" {
		t.Errorf("latest = %s", pkg.DistTags["latest"])
	}
	if len(pkg.Versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(pkg.Versions))
	}
}

// TestGetVersions verifies version list parsing.
func TestGetVersions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"version":"1.0.0","created_at":"2026-01-01","status":"active","deprecated":false}]`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	versions, err := c.GetVersions("test")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected 1 version, got %d", len(versions))
	}
	if versions[0].Version != "1.0.0" {
		t.Errorf("version = %s", versions[0].Version)
	}
}

// TestGetDistTags verifies dist-tags parsing.
func TestGetDistTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"latest":"1.1.0","beta":"1.2.0-beta"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	tags, err := c.GetDistTags("test")
	if err != nil {
		t.Fatal(err)
	}
	if tags["latest"] != "1.1.0" {
		t.Errorf("latest = %s", tags["latest"])
	}
	if tags["beta"] != "1.2.0-beta" {
		t.Errorf("beta = %s", tags["beta"])
	}
}

// TestHealth verifies health check parsing.
func TestHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{"status":"ok","version":"0.5.1"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	h, err := c.Health()
	if err != nil {
		t.Fatal(err)
	}
	if h.Status != "ok" {
		t.Errorf("status = %s", h.Status)
	}
	if h.Version != "0.5.1" {
		t.Errorf("version = %s", h.Version)
	}
}

// TestAPIError verifies that APIError messages are formatted correctly.
func TestAPIError(t *testing.T) {
	e := &APIError{StatusCode: 404, Body: []byte(`{"error":"not found"}`)}
	if e.Error() != "API error (404): not found" {
		t.Errorf("error message = %q", e.Error())
	}
}
