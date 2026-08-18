package api

import (
	"strings"
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
