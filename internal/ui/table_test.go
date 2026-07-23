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

// TestStripANSI verifies ANSI code removal.
func TestStripANSI(t *testing.T) {
	result := stripANSI("\x1b[31mred\x1b[0m")
	if result != "red" {
		t.Errorf("stripANSI = %q", result)
	}
}
