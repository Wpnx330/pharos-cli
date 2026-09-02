package cmd

import (
	"strings"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

func TestSearchTableColumnsOrder(t *testing.T) {
	cols := searchTableColumns()
	want := []string{"NAME", "VERSION", "TRANSPORT", "REGISTRY", "OWNER", "CATEGORY", "DESCRIPTION", "DOWNLOADS"}
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

func TestSearchTableNewSignalColumnsHaveMaxWidthCaps(t *testing.T) {
	// Narrow-mode plan: the renderer has no terminal-width presets, so the
	// new OWNER/CATEGORY columns rely on per-column MaxWidth caps like the
	// rest of the table. VERSION widens to fit the version_status suffix.
	for _, c := range searchTableColumns() {
		switch c.Title {
		case "VERSION":
			if c.MaxWidth != 18 {
				t.Errorf("VERSION MaxWidth = %d, want 18 (room for \" (stale)\" suffix)", c.MaxWidth)
			}
		case "OWNER":
			if c.MaxWidth != 18 {
				t.Errorf("OWNER MaxWidth = %d, want 18", c.MaxWidth)
			}
		case "CATEGORY":
			if c.MaxWidth != 16 {
				t.Errorf("CATEGORY MaxWidth = %d, want 16", c.MaxWidth)
			}
		}
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
		Publisher:      "moodmnky",
		Category:       "lifestyle",
	})
	if len(row) != 8 {
		t.Fatalf("row cells = %d, want 8: %#v", len(row), row)
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
	if row[4] != "moodmnky" {
		t.Errorf("OWNER = %q, want moodmnky (flattened publisher.namespace)", row[4])
	}
	if row[5] != "lifestyle" {
		t.Errorf("CATEGORY = %q, want lifestyle", row[5])
	}
	if row[6] == "" {
		t.Error("DESCRIPTION is empty")
	}
	if row[7] != "0" {
		t.Errorf("DOWNLOADS = %q, want 0", row[7])
	}
}

func TestSearchTableRowVersionStatusSuffix(t *testing.T) {
	row := searchTableRow(api.SearchResult{
		Name:          "stale-hit",
		Version:       "1.2.3",
		VersionStatus: "stale",
	})
	if row[1] != "1.2.3 (stale)" {
		t.Errorf("VERSION = %q, want \"1.2.3 (stale)\" (status shown only when != active)", row[1])
	}
}

func TestSearchTableRowVersionStatusActiveOmitted(t *testing.T) {
	for _, status := range []string{"active", "", "  "} {
		row := searchTableRow(api.SearchResult{
			Name:          "healthy",
			Version:       "2.0.0",
			VersionStatus: status,
		})
		if row[1] != "2.0.0" {
			t.Errorf("VERSION with status %q = %q, want bare 2.0.0", status, row[1])
		}
	}
}

func TestSearchVersionCell(t *testing.T) {
	tests := []struct {
		name    string
		version string
		status  string
		want    string
	}{
		{name: "active stays bare", version: "1.0.0", status: "active", want: "1.0.0"},
		{name: "empty status stays bare", version: "1.0.0", status: "", want: "1.0.0"},
		{name: "stale suffix", version: "1.2.3", status: "stale", want: "1.2.3 (stale)"},
		{name: "deprecated suffix", version: "0.9.0", status: "deprecated", want: "0.9.0 (deprecated)"},
		{name: "status with spaces trimmed", version: "1.0.0", status: " yanked ", want: "1.0.0 (yanked)"},
		{name: "empty version shows status alone", version: "", status: "stale", want: "(stale)"},
		{name: "both empty", version: "", status: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := searchVersionCell(tt.version, tt.status); got != tt.want {
				t.Errorf("searchVersionCell(%q, %q) = %q, want %q", tt.version, tt.status, got, tt.want)
			}
		})
	}
}

func TestFormatSearchDownloads(t *testing.T) {
	tests := []struct {
		name string
		in   int64
		want string
	}{
		{name: "zero", in: 0, want: "0"},
		{name: "under a thousand stays raw", in: 950, want: "950"},
		{name: "one thousand trims .0", in: 1000, want: "1k"},
		{name: "one decimal k", in: 1234, want: "1.2k"},
		{name: "tens of k", in: 34500, want: "34.5k"},
		{name: "millions", in: 5300000, want: "5.3m"},
		{name: "million trims .0", in: 1000000, want: "1m"},
		{name: "billions", in: 2000000000, want: "2b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatSearchDownloads(tt.in); got != tt.want {
				t.Errorf("formatSearchDownloads(%d) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSearchTableRowEmptyOwnerAndCategoryUseDash(t *testing.T) {
	row := searchTableRow(api.SearchResult{
		Name:    "legacy-hit",
		Version: "1.2.3",
	})
	if row[4] != listDash {
		t.Errorf("empty OWNER = %q, want %q", row[4], listDash)
	}
	if row[5] != listDash {
		t.Errorf("empty CATEGORY = %q, want %q", row[5], listDash)
	}
	if row[1] != "1.2.3" {
		t.Errorf("VERSION without status = %q, want 1.2.3", row[1])
	}
}

func TestSearchTableRendersSignalColumnsWithTruncation(t *testing.T) {
	// Integration through the real renderer: long OWNER/CATEGORY cells are
	// truncated (MaxWidth discipline) instead of blowing up row width.
	r := api.SearchResult{
		Name:          "long-namespace-hit",
		Version:       "1.0.0",
		Publisher:     "io.github.very-long-namespace-name",
		Category:      "developer-tools-integrations",
		Downloads:     1234,
		VersionStatus: "stale",
	}
	out := ui.RenderTable(searchTableColumns(), []ui.TableRow{searchTableRow(r)})
	for _, header := range []string{"OWNER", "CATEGORY", "DOWNLOADS"} {
		if !strings.Contains(out, header) {
			t.Errorf("rendered table missing %q header:\n%s", header, out)
		}
	}
	if !strings.Contains(out, "1.2k") {
		t.Errorf("DOWNLOADS not humanized in table:\n%s", out)
	}
	if !strings.Contains(out, "(stale)") {
		t.Errorf("version_status suffix missing from table:\n%s", out)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("long OWNER/CATEGORY cells were not truncated:\n%s", out)
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
