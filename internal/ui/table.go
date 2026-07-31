package ui

import (
	"fmt"
	"strings"
)

// TableColumn describes a single column in a table.
type TableColumn struct {
	Title string
	Width int  // minimum width
	MaxWidth int // 0 = no cap (use global maxColWidth); >0 = cap at this width
}

// TableRow is a slice of cell strings, one per column.
type TableRow = []string

// RenderTable renders a slice of rows as a formatted ASCII table.
// Columns auto-size to fit content, capped at maxColWidth. The header
// is bold/colored without border decorations (borders produce multi-line
// output that breaks single-line column alignment).
//
// All width calculations use VISIBLE width (ANSI escape sequences stripped),
// so styled cells (bold, colored) align correctly with unstyled cells.
func RenderTable(cols []TableColumn, rows []TableRow) string {
	const maxColWidth = 80
	widths := make([]int, len(cols))
	for i, c := range cols {
		plain := stripANSI(c.Title)
		w := runeWidth(plain)
		if c.Width > w {
			w = c.Width
		}
		widths[i] = w
	}
	// Expand to fit row content (but cap at per-column or global max)
	for _, row := range rows {
		for i, cell := range row {
			plain := stripANSI(cell)
			w := runeWidth(plain)
			if w > widths[i] {
				widths[i] = w
			}
			// Apply per-column cap if set, otherwise global cap
			if cols[i].MaxWidth > 0 && widths[i] > cols[i].MaxWidth {
				widths[i] = cols[i].MaxWidth
			} else if widths[i] > maxColWidth {
				widths[i] = maxColWidth
			}
		}
	}

	var b strings.Builder

	// Header — simple bold color, no borders
	headers := make([]string, len(cols))
	for i, c := range cols {
		headers[i] = padStyled(HeaderSimple.Render(c.Title), widths[i])
	}
	b.WriteString(strings.Join(headers, "  "))
	b.WriteString("\n")

	// Body
	for _, row := range rows {
		cells := make([]string, len(cols))
		for i, cell := range row {
			cells[i] = padStyled(truncateStyled(cell, widths[i]), widths[i])
		}
		b.WriteString(strings.Join(cells, "  "))
		b.WriteString("\n")
	}

	return b.String()
}

// runeWidth returns the visible width of a string (ANSI stripped).
func runeWidth(s string) int {
	return len([]rune(stripANSI(s)))
}

// padStyled right-pads a styled string with spaces to reach visible width n.
// Works correctly with ANSI-styled strings by measuring stripped width.
func padStyled(s string, n int) string {
	plain := stripANSI(s)
	visW := len([]rune(plain))
	if visW >= n {
		return s
	}
	return s + strings.Repeat(" ", n-visW)
}

// truncateStyled shortens a styled string to n VISIBLE runes, preserving
// ANSI styling. Strips ANSI, truncates the plain text, re-applies the
// original style prefix, and appends "…" if truncated.
func truncateStyled(s string, n int) string {
	if n <= 0 {
		return s
	}
	plain := stripANSI(s)
	visRunes := []rune(plain)
	if len(visRunes) <= n {
		return s // visible text fits, no truncation needed
	}
	// Extract the ANSI style prefix from the original string
	ansiPrefix := extractANSIPrefix(s)
	truncated := string(visRunes[:n-1])
	if ansiPrefix != "" {
		return ansiPrefix + truncated + "\x1b[0m…"
	}
	return truncated + "…"
}

// extractANSIPrefix pulls leading ANSI escape sequences from a string.
// lipgloss typically emits \x1b[...m prefix + text + \x1b[0m suffix.
func extractANSIPrefix(s string) string {
	var prefix strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			// Find end of escape sequence (ends with 'm' for SGR)
			j := i + 1
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				prefix.WriteString(s[i : j+1])
				i = j + 1
			} else {
				break
			}
		} else {
			break // First non-ANSI char = start of visible text
		}
	}
	return prefix.String()
}

// pad right-pads s with spaces to reach width n.
// Deprecated: use padStyled for ANSI-styled strings.
func pad(s string, n int) string {
	plain := stripANSI(s)
	if len(plain) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(plain))
}

// truncate shortens s to n runes, appending "…" if trimmed.
// Deprecated: use truncateStyled for ANSI-styled strings.
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
