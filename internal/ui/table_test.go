package ui

import (
	"strings"
	"testing"
)

// TestRenderTable verifies that table rendering produces headers and rows.
func TestRenderTable(t *testing.T) {
	cols := []TableColumn{
		{Title: "NAME", Width: 10},
		{Title: "VERSION", Width: 8},
	}
	rows := []TableRow{
		{"pkg-a", "1.0.0"},
		{"pkg-b", "2.0.0"},
	}
	out := RenderTable(cols, rows)
	if !strings.Contains(out, "NAME") {
		t.Error("output missing NAME header")
	}
	if !strings.Contains(out, "VERSION") {
		t.Error("output missing VERSION header")
	}
	if !strings.Contains(out, "pkg-a") {
		t.Error("output missing pkg-a row")
	}
	if !strings.Contains(out, "pkg-b") {
		t.Error("output missing pkg-b row")
	}
}

// TestRenderTableEmpty verifies rendering works with no rows.
func TestRenderTableEmpty(t *testing.T) {
	cols := []TableColumn{{Title: "NAME", Width: 10}}
	out := RenderTable(cols, nil)
	if !strings.Contains(out, "NAME") {
		t.Error("output missing header")
	}
}

// TestTruncate verifies string truncation.
func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("short string should not be truncated")
	}
	if truncate("hello world", 8) != "hello w…" {
		t.Errorf("truncate = %q", truncate("hello world", 8))
	}
}

// TestFormatList verifies list formatting.
func TestFormatList(t *testing.T) {
	if FormatList([]string{"a", "b", "c"}) != "a, b, c" {
		t.Error("list formatting failed")
	}
	if FormatList(nil) != "" {
		t.Error("empty list should be empty string")
	}
}

// TestFormatBytes verifies byte formatting.
func TestFormatBytes(t *testing.T) {
	if FormatBytes(500) != "500 B" {
		t.Errorf("got %s", FormatBytes(500))
	}
	if FormatBytes(1024) != "1.0 KiB" {
		t.Errorf("got %s", FormatBytes(1024))
	}
}

// TestTableWidths verifies the width computation RenderTable uses: the
// title/Width floor, expansion to the widest cell, and per-column caps.
func TestTableWidths(t *testing.T) {
	cols := []TableColumn{
		{Title: "NAME", Width: 10},
		{Title: "VERSION", Width: 8, MaxWidth: 10},
	}
	rows := []TableRow{
		{"a-very-long-package-name", "1.0.0-beta-build"},
		{"short", "2.0"},
	}
	widths := TableWidths(cols, rows)
	if widths[0] != 24 {
		t.Errorf("NAME width = %d, want 24 (widest cell, no cap)", widths[0])
	}
	if widths[1] != 10 {
		t.Errorf("VERSION width = %d, want 10 (MaxWidth cap)", widths[1])
	}
}

// TestRenderTableSectionsShareWidths verifies stacked sections render on
// one shared width grid: content that widens a column in one section
// widens it in every section, so the tables align column-for-column.
func TestRenderTableSectionsShareWidths(t *testing.T) {
	cols := []TableColumn{{Title: "NAME", Width: 10}, {Title: "VERSION", Width: 8}}
	sponsored := []TableRow{{"gamma/three [boosted]", "1.0.0"}} // NAME needs 21
	organic := []TableRow{{"alpha/one", "2.0.0"}}

	outs := RenderTableSections(cols, sponsored, organic)
	if len(outs) != 2 || outs[0] == "" || outs[1] == "" {
		t.Fatalf("RenderTableSections = %d sections (need 2 non-empty)", len(outs))
	}
	// The shared NAME width must fit the sponsored marker: every line is
	// len("gamma/three [boosted]") + 2 + 8 (VERSION Width floor) = 31
	// visible runes, wider than the organic section's solo render (NAME
	// floor 10).
	want := len("gamma/three [boosted]") + 2 + 8
	for _, w := range append(visibleLinesWidth(outs[1]), visibleLinesWidth(outs[0])...) {
		if w != want {
			t.Errorf("section line width = %d, want %d (shared grid):\n%q\n%q", w, want, outs[0], outs[1])
		}
	}
}

// TestRenderTableUnchanged verifies the RenderTable convenience wrapper
// still renders exactly one section solo.
func TestRenderTableSoloEqualsSectionWidths(t *testing.T) {
	cols := []TableColumn{{Title: "NAME", Width: 10}}
	rows := []TableRow{{"alpha/one"}}
	if RenderTable(cols, rows) != renderTableSized(cols, TableWidths(cols, rows), rows) {
		t.Error("RenderTable and renderTableSized diverged")
	}
}

func visibleLinesWidth(table string) []int {
	var widths []int
	for _, line := range strings.Split(table, "\n") {
		if line == "" {
			continue
		}
		widths = append(widths, len([]rune(stripANSI(line))))
	}
	return widths
}

// TestStripANSI verifies ANSI code removal.
func TestStripANSI(t *testing.T) {
	result := stripANSI("\x1b[31mred\x1b[0m")
	if result != "red" {
		t.Errorf("stripANSI = %q", result)
	}
}
