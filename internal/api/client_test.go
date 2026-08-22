package api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestPackagePath verifies that packagePath correctly normalizes
// unscoped names, scoped names with "@", and scoped names without "@".
func TestPackagePath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "unscoped simple name",
			input: "test-echo-server",
			want:  "test-echo-server",
		},
		{
			name:  "unscoped with dots",
			input: "foo.bar.baz",
			want:  "foo.bar.baz",
		},
		{
			name:  "scoped with at prefix",
			input: "@scope/name",
			want:  "@scope/name",
		},
		{
			name:  "scoped without at prefix",
			input: "io.github.cyanheads/epa-mcp-server",
			want:  "@io.github.cyanheads/epa-mcp-server",
		},
		{
			name:  "scoped short",
			input: "myorg/myserver",
			want:  "@myorg/myserver",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "only at sign",
			input: "@",
			want:  "@",
		},
		{
			name:  "slash at start returns as-is (idx not > 0)",
			input: "/name",
			want:  "/name",
		},
		{
			name:  "name with trailing slash",
			input: "foo/",
			want:  "@foo/",
		},
		{
			name:  "multiple slashes",
			input: "org/sub/name",
			want:  "@org/sub/name",
		},
		{
			name:  "at prefix with multiple slashes",
			input: "@org/sub/name",
			want:  "@org/sub/name",
		},
		{
			name:  "single char before slash",
			input: "a/b",
			want:  "@a/b",
		},
		{
			name:  "reverse-dns scoped id stays scoped",
			input: "io.github.j0hanz/filesystem-mcp",
			want:  "@io.github.j0hanz/filesystem-mcp",
		},
		{
			name:  "spaced display name is escaped not rejected",
			input: "Filesystem MCP Server",
			want:  "Filesystem%20MCP%20Server",
		},
		{
			name:  "spaced title with slash is one escaped segment",
			input: "Filesystem MCP Server (@shtse8/filesystem-mcp)",
			want:  "Filesystem%20MCP%20Server%20%28@shtse8%2Ffilesystem-mcp%29",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := packagePath(tt.input)
			if got != tt.want {
				t.Errorf("packagePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestPackagePathUnscoped verifies that the unscoped path does not
// contain an "@" character or a "/".
func TestPackagePathUnscoped(t *testing.T) {
	got := packagePath("my-server")
	if strings.Contains(got, "@") {
		t.Errorf("packagePath(\"my-server\") = %q, should not contain @", got)
	}
}

// TestPackagePathScopedAddsAt verifies that scoped names without "@"
// get the "@" prefix prepended.
func TestPackagePathScopedAddsAt(t *testing.T) {
	got := packagePath("org/pkg")
	if !strings.HasPrefix(got, "@") {
		t.Errorf("packagePath(\"org/pkg\") = %q, should start with @", got)
	}
}

func TestPackagePathKeepsScopedSlash(t *testing.T) {
	got := packagePath("io.github.j0hanz/filesystem-mcp")
	if !strings.Contains(got, "/") {
		t.Fatalf("packagePath scoped id = %q, must keep / as scope separator", got)
	}
	if strings.Contains(got, "%2F") {
		t.Fatalf("packagePath scoped id = %q, must not escape / as a single blob", got)
	}
	if !strings.HasPrefix(got, "@io.github.j0hanz/") {
		t.Errorf("packagePath scoped id = %q, want scoped route prefix", got)
	}
}

func TestPackagePathEscapesSpacedName(t *testing.T) {
	got := packagePath("Filesystem MCP Server")
	if got != "Filesystem%20MCP%20Server" {
		t.Errorf("packagePath spaced name = %q, want escaped spaces", got)
	}
	if strings.Contains(got, " ") {
		t.Errorf("packagePath(%q) still contains a raw space", got)
	}
}

// --- 429 retry tests ---

func TestRetryDelay_ExponentialBackoff(t *testing.T) {
	tests := []struct {
		attempt int
		want    string
	}{
		{0, "2s"},
		{1, "4s"},
		{2, "8s"},
	}
	for _, tt := range tests {
		got := retryDelay(tt.attempt, "")
		if got.String() != tt.want {
			t.Errorf("retryDelay(%d, \"\") = %v, want %s", tt.attempt, got, tt.want)
		}
	}
}

func TestRetryDelay_RetryAfterHeader(t *testing.T) {
	got := retryDelay(0, "5")
	if got.String() != "5s" {
		t.Errorf("retryDelay(0, \"5\") = %v, want 5s", got)
	}
}

func TestRetryDelay_Capped(t *testing.T) {
	got := retryDelay(0, "60")
	if got.String() != "30s" {
		t.Errorf("retryDelay(0, \"60\") = %v, want 30s (capped)", got)
	}
}

func TestRetryDelay_ExponentialCapped(t *testing.T) {
	got := retryDelay(10, "")
	if got.String() != "30s" {
		t.Errorf("retryDelay(10, \"\") = %v, want 30s (capped)", got)
	}
}

func TestDo_RetriesOn429(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	data, status, err := c.do(http.MethodGet, "/test", nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(data) != `{"ok":true}` {
		t.Errorf("data = %q, want {\"ok\":true}", string(data))
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

func TestDo_MaxRetriesExhausted(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limit exceeded"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	data, status, err := c.do(http.MethodGet, "/test", nil)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 429 {
		t.Errorf("APIError.StatusCode = %d, want 429", apiErr.StatusCode)
	}
	if status != 429 {
		t.Errorf("status = %d, want 429", status)
	}
	if len(data) == 0 {
		t.Error("expected non-empty body data")
	}
	if got := atomic.LoadInt32(&attempts); got != 4 {
		t.Errorf("attempts = %d, want 4 (1 initial + 3 retries)", got)
	}
}

func TestDo_RetryAfterHeader(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	data, status, err := c.do(http.MethodGet, "/test", nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(data) != `{"ok":true}` {
		t.Errorf("data = %q, want {\"ok\":true}", string(data))
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
}

func TestDo_NoRetryOn500(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"internal"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	_, _, err := c.do(http.MethodGet, "/test", nil)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 500 {
		t.Errorf("APIError.StatusCode = %d, want 500", apiErr.StatusCode)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("attempts = %d, want 1 (no retries on 500)", got)
	}
}

func TestDo_RetriesWithBody(t *testing.T) {
	var attempts int32
	var lastBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		bodyBytes, _ := io.ReadAll(r.Body)
		lastBody = string(bodyBytes)
		if n < 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	postBody := []byte(`{"name":"test-pkg"}`)
	data, status, err := c.do(http.MethodPost, "/publish", bytes.NewReader(postBody))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != 200 {
		t.Errorf("status = %d, want 200", status)
	}
	if string(data) != `{"ok":true}` {
		t.Errorf("data = %q, want {\"ok\":true}", string(data))
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
	if lastBody != `{"name":"test-pkg"}` {
		t.Errorf("last request body = %q, want {\"name\":\"test-pkg\"}", lastBody)
	}
}
