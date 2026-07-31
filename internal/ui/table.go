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
// Columns auto-size to fit content, capped at maxColWidth. The header
// is bold/colored without border decorations (borders produce multi-line
// output that breaks single-line column alignment).
func RenderTable(cols []TableColumn, rows []TableRow) string {
	const maxColWidth = 60
	widths := make([]int, len(cols))
	for i, c := range cols {
		plain := stripANSI(c.Title)
		w := len([]rune(plain))
		if c.Width > w {
			w = c.Width
		}
		widths[i] = w
	}
	// Expand to fit row content (but cap at maxColWidth)
	for _, row := range rows {
		for i, cell := range row {
			plain := stripANSI(cell)
			w := len([]rune(plain))
			if w > widths[i] {
				widths[i] = w
			}
			if widths[i] > maxColWidth {
				widths[i] = maxColWidth
			}
		}
	}

	var b strings.Builder

	// Header — simple bold color, no borders
	headers := make([]string, len(cols))
	for i, c := range cols {
		headers[i] = pad(HeaderSimple.Render(c.Title), widths[i])
	}
	b.WriteString(strings.Join(headers, "  "))
	b.WriteString("\n")

	// Body
	for _, row := range rows {
		cells := make([]string, len(cols))
		for i, cell := range row {
			cells[i] = pad(truncate(cell, widths[i]), widths[i])
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
