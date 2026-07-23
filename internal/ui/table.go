package ui

import (
	"fmt"
	"strings"
)

// TableColumn describes a single column in a table.
type TableColumn struct {
	Title string
	Width int
}

// TableRow is a slice of cell strings, one per column.
type TableRow = []string

// RenderTable renders a slice of rows as a formatted ASCII table.
// Columns are aligned by the width defined in cols; cell text is
// truncated to the column width.
func RenderTable(cols []TableColumn, rows []TableRow) string {
	var b strings.Builder

	// Header
	headers := make([]string, len(cols))
	for i, c := range cols {
		headers[i] = pad(Header.Render(c.Title), c.Width)
	}
	b.WriteString(strings.Join(headers, "  "))
	b.WriteString("\n")

	// Body
	for _, row := range rows {
		cells := make([]string, len(cols))
		for i, cell := range row {
			w := cols[i].Width
			cells[i] = pad(truncate(cell, w), w)
		}
		b.WriteString(strings.Join(cells, "  "))
		b.WriteString("\n")
	}

	return b.String()
}

// pad right-pads s with spaces to reach width n.
func pad(s string, n int) string {
	// strip ANSI for width calc but keep original styling
	plain := stripANSI(s)
	if len(plain) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(plain))
}

// truncate shortens s to n runes, appending "…" if trimmed.
func truncate(s string, n int) string {
	if n <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// stripANSI removes ANSI escape sequences so we can measure visible width.
func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEscape = true
		case inEscape && r == 'm':
			inEscape = false
		case !inEscape:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// FormatList formats a slice of strings as "a, b, c".
func FormatList(items []string) string {
	return strings.Join(items, ", ")
}

// FormatBytes converts a byte count into a human-readable string.
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
