package cmd

import (
	"strings"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

func TestSearchTableColumnsOrder(t *testing.T) {
	cols := searchTableColumns()
	want := []string{"NAME", "VERSION", "TRANSPORT", "REGISTRY", "DESCRIPTION", "DOWNLOADS"}
	if len(cols) != len(want) {
		t.Fatalf("column count = %d, want %d (%v)", len(cols), len(want), titlesOf(cols))
	}
	for i, title := range want {
		if cols[i].Title != title {
			t.Errorf("column[%d] = %q, want %q", i, cols[i].Title, title)
		}
	}
}

func TestSearchTableColumnsTransportAndRegistryAfterVersion(t *testing.T) {
	cols := searchTableColumns()
	titles := titlesOf(cols)
	joined := strings.Join(titles, " ")
	if !strings.Contains(joined, "VERSION TRANSPORT REGISTRY") {
		t.Errorf("titles %v do not place TRANSPORT and REGISTRY immediately after VERSION", titles)
	}
}

func TestSearchTableRowPopulatedTransportAndRegistry(t *testing.T) {
	row := searchTableRow(api.SearchResult{
		Name:           "MoodMNKY MCP Server Stack",
		Version:        "0.0.0",
		Description:    "MoodMNKY MCP server stack",
		Downloads:      0,
		Transport:      []string{"stdio"},
		SourceRegistry: "mcp.io",
	})
	if len(row) != 6 {
		t.Fatalf("row cells = %d, want 6: %#v", len(row), row)
	}
	if row[1] != "0.0.0" {
		t.Errorf("VERSION = %q, want API value 0.0.0 (do not invent a version)", row[1])
	}
	if row[2] != "stdio" {
		t.Errorf("TRANSPORT = %q, want stdio", row[2])
	}
	if row[3] != "mcp.io" {
		t.Errorf("REGISTRY = %q, want mcp.io", row[3])
	}
	if row[4] != "MoodMNKY MCP Server Stack" && row[4] != "MoodMNKY MCP server stack" {
		// Description is passed through; renderer truncates via MaxWidth.
		if row[4] == "" {
			t.Error("DESCRIPTION is empty")
		}
	}
	if row[5] != "0" {
		t.Errorf("DOWNLOADS = %q, want 0", row[5])
	}
}

func TestSearchTableRowMultipleTransportsJoined(t *testing.T) {
	row := searchTableRow(api.SearchResult{
		Name:      "dual",
		Version:   "1.0.0",
		Transport: []string{"http-sse", "streamable-http"},
	})
	if row[2] != "http-sse,streamable-http" {
		t.Errorf("TRANSPORT = %q, want comma-joined values", row[2])
	}
}

func TestSearchTableRowEmptyTransportAndRegistryUseDash(t *testing.T) {
	row := searchTableRow(api.SearchResult{
		Name:    "legacy-hit",
		Version: "1.2.3",
	})
	if row[2] != listDash {
		t.Errorf("empty TRANSPORT = %q, want %q", row[2], listDash)
	}
	if row[3] != listDash {
		t.Errorf("empty REGISTRY = %q, want %q", row[3], listDash)
	}
}

func TestSearchTableRowBlankTransportStringsUseDash(t *testing.T) {
	row := searchTableRow(api.SearchResult{
		Transport:      []string{"", ""},
		SourceRegistry: "",
	})
	if row[2] != listDash {
		t.Errorf("blank TRANSPORT entries = %q, want %q", row[2], listDash)
	}
}

func TestFormatSearchTransport(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{name: "nil", in: nil, want: listDash},
		{name: "empty", in: []string{}, want: listDash},
		{name: "stdio", in: []string{"stdio"}, want: "stdio"},
		{name: "join", in: []string{"stdio", "http-sse"}, want: "stdio,http-sse"},
		{name: "skip blanks", in: []string{"", "http-sse", ""}, want: "http-sse"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatSearchTransport(tt.in); got != tt.want {
				t.Errorf("formatSearchTransport(%#v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSearchInfoFooter(t *testing.T) {
	got := searchInfoFooter()
	want := `Use: pharos info "<PACKAGE ID>"   (quote IDs that contain spaces)`
	if got != want {
		t.Errorf("searchInfoFooter() = %q, want %q", got, want)
	}
	if strings.Contains(got, "display name") {
		t.Errorf("searchInfoFooter must not say display name: %q", got)
	}
}

func TestSearchCmdRegistryAndTransportFlags(t *testing.T) {
	reg := searchCmd.Flags().Lookup("registry")
	if reg == nil {
		t.Fatal("search command missing --registry flag")
	}
	if reg.DefValue != "" {
		t.Errorf("search --registry default = %q, want empty", reg.DefValue)
	}
	tr := searchCmd.Flags().Lookup("transport")
	if tr == nil {
		t.Fatal("search command missing --transport flag")
	}
	if tr.DefValue != "" {
		t.Errorf("search --transport default = %q, want empty", tr.DefValue)
	}
	if searchCmd.Flags().Lookup("page") == nil {
		t.Fatal("search command must keep --page")
	}
}

func TestSearchNextPageHint(t *testing.T) {
	if got := searchNextPageHint("filesystem", 1, "", "", ""); got != "" {
		t.Errorf("empty nextCursor should yield empty hint, got %q", got)
	}
	got := searchNextPageHint("filesystem", 1, "", "", "MTA=")
	want := `next page: pharos search "filesystem" --page 2`
	if got != want {
		t.Errorf("hint = %q, want %q", got, want)
	}
	got = searchNextPageHint("filesystem", 2, "mcp.io", "stdio", "MjA=")
	want = `next page: pharos search "filesystem" --page 3 --registry mcp.io --transport stdio`
	if got != want {
		t.Errorf("filtered hint = %q, want %q", got, want)
	}
}

func titlesOf(cols []ui.TableColumn) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Title
	}
	return out
}
