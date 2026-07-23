// Package ui provides terminal styling and table rendering helpers
// built on top of lipgloss.
package ui

import "github.com/charmbracelet/lipgloss"

// Brand color palette.
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
		Foreground(lipgloss.Color("#2E7D32")).
		Bold(true)

	// Error styles error messages in red and bold.
	Error = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#C62828")).
		Bold(true)

	// Label styles informational labels in navy.
	Label = lipgloss.NewStyle().
		Foreground(lipgloss.Color(Navy)).
		Bold(true)

	// Muted renders dim, secondary text.
	Muted = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#888888"))

	// Header styles table column headers.
	Header = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(Gold)).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true)
)
