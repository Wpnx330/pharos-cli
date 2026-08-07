package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/manifest"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var initYes bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a pharos.json manifest in the current directory",
	Long: ui.Label.Render("pharos init") + ` creates a pharos.json manifest for your MCP server package.

It interactively prompts for name, version, description, transport, runtime,
run command, and capabilities. Use --yes to accept defaults non-interactively.`,
	Run: func(cmd *cobra.Command, args []string) {
		m := buildManifestInteractive()
		if m == nil {
			return
		}
		writeManifest(m)
		writeGitignore()
	},
}

// stdinReader is a buffered reader over os.Stdin for text prompts.
var stdinReader = bufio.NewReader(os.Stdin)

// buildManifestInteractive collects manifest fields via interactive prompts.
// Uses arrow-key selectors for fixed-option fields and text input for freeform fields.
// Returns nil if the user cancels.
// If initYes is true, all defaults are accepted without prompting.
func buildManifestInteractive() *manifest.Manifest {
	if initYes {
		return &manifest.Manifest{
			Name:         "my-mcp-server",
			Version:      "0.1.0",
			Description:  "",
			Transport:    "stdio",
			Runtime:      "node",
			Bin:          "node server.js",
			Capabilities: []string{"tools"},
			License:      "MIT",
		}
	}

	m := &manifest.Manifest{}

	m.Name = textPrompt("Package name", "my-mcp-server")
	if m.Name == "" {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Package name is required."))
		return nil
	}

	m.Version = textPrompt("Version", "0.1.0")
	m.Description = textPrompt("Description", "")

	// Transport — arrow-key select
	m.Transport = selectPrompt("Transport", []string{"stdio", "http-sse"}, "stdio")

	// Runtime — arrow-key select
	runtime := selectPrompt("Runtime", []string{"node", "python", "docker"}, "node")
	m.Runtime = runtime

	m.Bin = textPrompt("Run command", defaultCommand(runtime, m.Transport))

	// Capabilities — multi-select
	m.Capabilities = multiSelectPrompt("Capabilities", []string{
		"tools", "resources", "prompts", "logging",
	}, []string{"tools"})

	// License — arrow-key select with "Other" fallback
	m.License = selectPromptWithOther("License", []string{
		"MIT", "Apache-2.0", "GPL-3.0", "BSD-3-Clause", "ISC", "Unlicense",
	}, "MIT")

	// Dependencies — freeform, repeat until empty line
	m.Dependencies = dependenciesPrompt()

	m.Homepage = textPrompt("Homepage", "")

	return m
}

// ── Text prompt ──────────────────────────────────────────────────────────────

// textPrompt prints a label and default, then reads a full line from stdin.
// If the user enters nothing (just Enter), the default is used.
func textPrompt(label, def string) string {
	if def == "" {
		fmt.Printf("%s  ", ui.Label.Render(label+":"))
	} else {
		fmt.Printf("%s  %s ", ui.Label.Render(label+":"), ui.Muted.Render("["+def+"]"))
	}
	line, _ := stdinReader.ReadString('\n')
	input := strings.TrimSpace(line)
	if input == "" {
		return def
	}
	return input
}

// ── Single-select prompt (arrow keys) ────────────────────────────────────────

// selectModel is a minimal bubbletea program for arrow-key selection.
type selectModel struct {
	choices  []string
	cursor   int
	label    string
	selected string
	quitting bool
}

func (m selectModel) Init() tea.Cmd { return nil }

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		case "enter":
			m.selected = m.choices[m.cursor]
			m.quitting = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m selectModel) View() string {
	if m.quitting {
		return ""
	}
	var b strings.Builder
	b.WriteString(ui.Label.Render(m.label))
	b.WriteString("\n\n")
	for i, choice := range m.choices {
		cursor := "  "
		if i == m.cursor {
			cursor = ui.PackageName.Render("▸ ")
		}
		b.WriteString(fmt.Sprintf("  %s%s\n", cursor, choice))
	}
	b.WriteString("\n")
	b.WriteString(ui.Muted.Render("  ↑/↓ to navigate · Enter to select · Ctrl+C to cancel"))
	b.WriteString("\n")
	return b.String()
}

// selectPrompt shows an arrow-key selectable list and returns the chosen option.
func selectPrompt(label string, options []string, def string) string {
	// Find default index
	defIdx := 0
	for i, o := range options {
		if o == def {
			defIdx = i
			break
		}
	}

	p := tea.NewProgram(selectModel{
		choices: options,
		cursor:  defIdx,
		label:   label,
	}, tea.WithoutCatchPanics())

	finalModel, err := p.Run()
	if err != nil {
		// Fallback to text prompt if TUI fails (e.g. non-interactive terminal)
		fmt.Fprintf(os.Stderr, "  %s\n", ui.Muted.Render("(falling back to text input)"))
		return textPrompt(label, def)
	}

	m, ok := finalModel.(selectModel)
	if !ok || m.selected == "" {
		return def
	}

	fmt.Printf("%s  %s\n", ui.Label.Render(label+":"), ui.PackageName.Render(m.selected))
	return m.selected
}

// selectPromptWithOther shows a selectable list plus an "Other" option.
// If the user picks "Other", they get a text prompt to type a custom value.
func selectPromptWithOther(label string, options []string, def string) string {
	allOptions := append(options, "Other (type your own)")
	choice := selectPrompt(label, allOptions, def)

	if choice == "Other (type your own)" {
		return textPrompt("  "+label+" (custom)", def)
	}
	return choice
}

// ── Multi-select prompt (checkboxes) ─────────────────────────────────────────

// multiSelectModel is a bubbletea program for multi-select with checkboxes.
type multiSelectModel struct {
	choices   []string
	selected  map[int]bool
	cursor    int
	label     string
	quitting  bool
	result    []string
}

func (m multiSelectModel) Init() tea.Cmd { return nil }

func (m multiSelectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		case " ", "enter":
			// Space toggles selection, Enter confirms
			if msg.String() == " " {
				m.selected[m.cursor] = !m.selected[m.cursor]
			} else {
				// Enter — collect selected
				for i, ch := range m.choices {
					if m.selected[i] {
						m.result = append(m.result, ch)
					}
				}
				m.quitting = true
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m multiSelectModel) View() string {
	if m.quitting {
		return ""
	}
	var b strings.Builder
	b.WriteString(ui.Label.Render(m.label))
	b.WriteString("\n\n")
	for i, choice := range m.choices {
		cursor := "  "
		if i == m.cursor {
			cursor = ui.PackageName.Render("▸ ")
		}
		check := "☐"
		if m.selected[i] {
			check = ui.Success.Render("☑")
		}
		b.WriteString(fmt.Sprintf("  %s%s  %s\n", cursor, check, choice))
	}
	b.WriteString("\n")
	b.WriteString(ui.Muted.Render("  ↑/↓ navigate · Space toggle · Enter confirm · Ctrl+C cancel"))
	b.WriteString("\n")
	return b.String()
}

// multiSelectPrompt shows a checkbox list and returns the selected options.
func multiSelectPrompt(label string, options []string, defaults []string) []string {
	selected := make(map[int]bool)
	defSet := make(map[string]bool)
	for _, d := range defaults {
		defSet[d] = true
	}
	for i, o := range options {
		if defSet[o] {
			selected[i] = true
		}
	}

	p := tea.NewProgram(multiSelectModel{
		choices:  options,
		selected: selected,
		label:    label,
	}, tea.WithoutCatchPanics())

	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  %s\n", ui.Muted.Render("(falling back to text input)"))
		fallback := textPrompt(label+" (comma-separated)", strings.Join(defaults, ","))
		return splitCSV(fallback)
	}

	m, ok := finalModel.(multiSelectModel)
	if !ok || len(m.result) == 0 {
		return defaults
	}

	fmt.Printf("%s  %s\n", ui.Label.Render(label+":"), ui.PackageName.Render(strings.Join(m.result, ", ")))
	return m.result
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// dependenciesPrompt interactively collects dependency entries until the user
// submits an empty line. Each line is parsed by parseDependencyInput.
func dependenciesPrompt() []manifest.Dependency {
	fmt.Printf("%s\n", ui.Label.Render("Dependencies (empty line to finish):"))
	var deps []manifest.Dependency
	for {
		fmt.Print(ui.Muted.Render("  dep> "))
		line, _ := stdinReader.ReadString('\n')
		input := strings.TrimSpace(line)
		if input == "" {
			break
		}
		name, version, err := parseDependencyInput(input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s %s\n", ui.Error.Render("✗"), err.Error())
			continue
		}
		deps = append(deps, manifest.Dependency{Name: name, Version: version})
		fmt.Printf("  %s %s@%s\n", ui.Success.Render("✓ added"), name, version)
	}
	return deps
}

// parseDependencyInput parses a single dependency entry typed by the user.
// Supported formats:
//
//	name            → version "*"  (any)
//	name@latest     → version "latest"
//	name>=0.1.0     → version ">=0.1.0"
//	name=0.1.0      → version "=0.1.0"
//	name<=0.1.0     → version "<=0.1.0"
//	name^1.0.0      → version "^1.0.0"
//
// The name must be non-empty and contain only alphanumerics, dots, hyphens,
// underscores, or forward slashes (scope names).
func parseDependencyInput(input string) (name, version string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", fmt.Errorf("empty input")
	}

	// Handle @latest first — but be careful: scoped names like @scope/pkg
	// can start with '@'. We treat '@' as a separator only when it appears
	// after at least one name character. name@latest → latest.
	if idx := strings.Index(input, "@"); idx > 0 {
		name = input[:idx]
		version = strings.TrimSpace(input[idx+1:])
		if version == "" {
			return "", "", fmt.Errorf("missing version after @")
		}
		if version != "latest" {
			// Unknown @version form: treat the whole thing as a version string
			// (preserves whatever the user typed after the '@')
		}
		return validateDepName(name, version)
	}

	// Semver constraint operators: >=, <=, =, ^ (and > / <)
	for _, op := range []string{">=", "<=", "==", ">", "<", "=", "^", "~"} {
		if idx := strings.Index(input, op); idx > 0 {
			name = strings.TrimSpace(input[:idx])
			rawVersion := strings.TrimSpace(input[idx:])
			// Normalize "==X" → "=X" per our supported forms
			if strings.HasPrefix(rawVersion, "==") {
				rawVersion = rawVersion[1:]
			}
			// Reject a bare operator with no actual version (e.g. "lodash=")
			rest := strings.TrimLeft(rawVersion, "><=^~")
			if strings.TrimSpace(rest) == "" {
				return "", "", fmt.Errorf("missing version after %q", op)
			}
			return validateDepName(name, rawVersion)
		}
	}

	// Bare name → any version
	name = input
	version = "*"
	return validateDepName(name, version)
}

// validateDepName checks that the dependency name is non-empty and contains
// only allowed characters before returning the (name, version, nil) tuple.
func validateDepName(name, version string) (string, string, error) {
	if name == "" {
		return "", "", fmt.Errorf("dependency name is required")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '-', r == '_', r == '/', r == '@':
			// allowed
		default:
			return "", "", fmt.Errorf("invalid character %q in dependency name", r)
		}
	}
	return name, version, nil
}

// defaultCommand returns a sensible default command for the runtime.
func defaultCommand(runtime, transport string) string {
	switch runtime {
	case "node":
		return "node server.js"
	case "python":
		return "python server.py"
	case "docker":
		return "docker run -i my-mcp-server"
	}
	return "node server.js"
}

// splitCSV splits a comma-separated string into trimmed fields.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// writeManifest marshals and writes pharos.json.
func writeManifest(m *manifest.Manifest) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to encode manifest:"), err)
		return
	}
	if err := os.WriteFile("pharos.json", data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to write pharos.json:"), err)
		return
	}
	fmt.Printf("%s  %s\n", ui.Success.Render("✓ Created:"), "pharos.json")
	fmt.Printf("%s  %s@%s\n", ui.Muted.Render("Package:"), m.Name, m.Version)
}

// writeGitignore creates a basic .gitignore if one doesn't exist.
func writeGitignore() {
	if _, err := os.Stat(".gitignore"); err == nil {
		return // already exists
	}
	content := `# Pharos
.pharos/
dist/
*.tgz

# Node
node_modules/

# Python
__pycache__/
*.pyc
.venv/

# Environment
.env
`
	if err := os.WriteFile(".gitignore", []byte(content), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, ui.Muted.Render("(could not create .gitignore)"), err)
		return
	}
	fmt.Printf("%s  %s\n", ui.Success.Render("✓ Created:"), ".gitignore")
}

func init() {
	initCmd.Flags().BoolVarP(&initYes, "yes", "y", false, "accept all defaults non-interactively")
	rootCmd.AddCommand(initCmd)
}
