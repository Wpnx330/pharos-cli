package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

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
command, and capabilities. Use --yes to accept defaults non-interactively.`,
	Run: func(cmd *cobra.Command, args []string) {
		m := buildManifestInteractive()
		if m == nil {
			return
		}
		writeManifest(m)
		writeGitignore()
	},
}

// buildManifestInteractive collects manifest fields via stdin prompts.
// Returns nil if the user cancels.
func buildManifestInteractive() *manifest.Manifest {
	m := &manifest.Manifest{}

	m.Name = prompt("Package name", "my-mcp-server")
	if m.Name == "" {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Package name is required."))
		return nil
	}

	m.Version = prompt("Version", "0.1.0")
	m.Description = prompt("Description", "")

	transport := promptChoice("Transport", []string{"stdio", "http-sse"}, "stdio")
	runtime := promptChoice("Runtime", []string{"node", "python", "docker"}, "node")

	m.Transport = transport
	m.Runtime = runtime
	m.Bin = prompt("Command", defaultCommand(runtime, transport))

	capsStr := prompt("Capabilities (comma-separated)", "tools")
	m.Capabilities = splitCSV(capsStr)

	m.License = prompt("License", "MIT")
	m.Homepage = prompt("Homepage", "")

	return m
}

// stdinReader is a buffered reader over os.Stdin, initialised once.
// Using bufio lets us read full lines including spaces (fmt.Scanln
// stops at the first space, which caused multi-word descriptions to
// bleed into subsequent prompts).
var stdinReader = bufio.NewReader(os.Stdin)

// prompt prints a label and default, then reads a full line from stdin.
// If the user enters nothing (just Enter), the default is used.
func prompt(label, def string) string {
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

// promptChoice presents a list of options and validates the choice.
func promptChoice(label string, options []string, def string) string {
	optStr := strings.Join(options, "/")
	for {
		val := prompt(fmt.Sprintf("%s (%s)", label, optStr), def)
		for _, o := range options {
			if val == o {
				return val
			}
		}
		fmt.Fprintf(os.Stderr, "%s  %s\n", ui.Error.Render("Invalid choice:"), val)
	}
}

// defaultCommand returns a sensible default command for the runtime.
func defaultCommand(runtime, transport string) string {
	switch runtime {
	case "node":
		return "node server.js"
	case "python":
		return "python -m my_mcp_server"
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
