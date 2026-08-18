package cmd

import (
	"errors"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/api"
)

func TestJoinInfoName(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{
			name: "unquoted words join with single space",
			in:   []string{"Filesystem", "MCP", "Server"},
			want: "Filesystem MCP Server",
		},
		{
			name: "quoted name is already one arg",
			in:   []string{"Filesystem MCP Server"},
			want: "Filesystem MCP Server",
		},
		{
			name: "scoped at-prefix unchanged",
			in:   []string{"@scope/pkg"},
			want: "@scope/pkg",
		},
		{
			name: "reverse-dns scoped id unchanged",
			in:   []string{"io.github.foo/bar"},
			want: "io.github.foo/bar",
		},
		{
			name: "simple unscoped name",
			in:   []string{"foo-bar"},
			want: "foo-bar",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinInfoName(tt.in); got != tt.want {
				t.Errorf("joinInfoName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestInfoCmdAcceptsMultipleArgs(t *testing.T) {
	if err := infoCmd.Args(infoCmd, []string{"Filesystem", "MCP", "Server"}); err != nil {
		t.Errorf("infoCmd.Args with unquoted words: unexpected error: %v", err)
	}
	if err := infoCmd.Args(infoCmd, []string{"Filesystem MCP Server"}); err != nil {
		t.Errorf("infoCmd.Args with one quoted name: unexpected error: %v", err)
	}
	if err := infoCmd.Args(infoCmd, []string{"@scope/pkg"}); err != nil {
		t.Errorf("infoCmd.Args with scoped id: unexpected error: %v", err)
	}
	if err := infoCmd.Args(infoCmd, []string{}); err == nil {
		t.Error("infoCmd.Args with 0 args: expected error, got nil")
	}
}

func TestInfoLookupHint(t *testing.T) {
	if got := infoLookupHint(errors.New("network down")); got != "" {
		t.Errorf("non-API error hint = %q, want empty", got)
	}
	if got := infoLookupHint(&api.APIError{StatusCode: 404, Body: []byte(`{"error":{"message":"not found"}}`)}); got != "" {
		t.Errorf("404 hint = %q, want empty (do not swallow other errors)", got)
	}
	got := infoLookupHint(&api.APIError{StatusCode: 400, Body: []byte(`{"error":{"message":"bad"}}`)})
	want := "use the exact PACKAGE ID from search (quote it if it has spaces)"
	if got != want {
		t.Errorf("400 hint = %q, want %q", got, want)
	}
}
