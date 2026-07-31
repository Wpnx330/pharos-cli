// Package ui provides terminal styling and table rendering helpers
// built on top of lipgloss.
package ui

import "github.com/charmbracelet/lipgloss"

// Brand color palette (for reference / web alignment).
const (
	Navy = "#0A1A3F"
	Gold = "#D4A017"
)

// Pre-built styles used across commands.
var (
	// PackageName styles package names in bold gold.
	PackageName = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(Gold))

	// Success styles success messages in green.
	Success = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#4CAF50")).
			Bold(true)

	// Error styles error messages in red and bold.
	Error = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#EF5350")).
			Bold(true)

	// Label styles informational labels in bright cyan for readability
	// on dark terminal backgrounds. Navy (#0A1A3F) is nearly invisible
	// on black, so we use a lighter accent that preserves the brand feel.
	Label = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#4FC3F7")).
			Bold(true)

	// Muted renders dim, secondary text.
	Muted = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9E9E9E"))

	// Header styles table column headers.
	// Uses border-free styling — borders produce multi-line output
	// that breaks column alignment in single-line rendering.
	HeaderSimple = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(Gold))

	// Header is kept for backward compatibility but should not be
	// used in table rendering (use HeaderSimple instead).
	Header = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(Gold)).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true)
)
