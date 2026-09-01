//go:generate go run github.com/Wpnx330/pharos-cli llmtxt

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Wpnx330/pharos-cli/internal/ui"
)

// llmtxtDoc is the default output path for `pharos llmtxt`, relative to the
// working directory (the repo root when regenerating docs/llm.txt).
const llmtxtDoc = "docs/llm.txt"

// llmNote carries the per-command agent-contract annotations that cannot be
// derived from the Cobra tree: the output shape, the env-var behavior, and
// the non-interactive path. Keyed by full command path (e.g. "pharos daemon
// status").
type llmNote struct {
	output string // output shape (JSON fields when --json exists, else plain)
	env    string // env contract notes
	ni     string // non-interactive path
}

// llmNotes is the contract metadata for every real command. Commands not
// listed here fall back to generic defaults.
var llmNotes = map[string]llmNote{
	"pharos": {
		output: "help text listing subcommands",
		env:    "all three contract vars are inherited by every subcommand",
		ni:     "no prompts; safe by default",
	},
	"pharos audit": {
		output: "JSON: {total_servers, scanned, vulnerable_servers, entries: [{server, version, advisories, error}], has_vulnerabilities}; exit code 1 when vulnerabilities are found. Plain: per-server report",
		ni:     "no prompts; requires registry access",
	},
	"pharos config": {
		output: "JSON get: {key, value} (value is \"\" when unset); JSON set: {key, value, saved: true}. Plain: \"key: value\" / \"✓ key = value\"",
		env:    "PHAROS_JSON=1 or --json",
		ni:     "no prompts",
	},
	"pharos config add-client": {
		output: "single confirmation line; JSON N/A (single-line output)",
		ni:     "no prompts; --path is required",
	},
	"pharos config remove-client": {
		output: "single confirmation line; exit 1 when the client is not registered; JSON N/A (single-line output)",
		ni:     "no prompts",
	},
	"pharos config list-clients": {
		output: "fixed-format list (BUILT-IN + CUSTOM sections); JSON N/A (single list output)",
		ni:     "no prompts",
	},
	"pharos daemon": {
		output: "help text listing daemon subcommands",
		ni:     "no prompts",
	},
	"pharos daemon start": {
		output: "startup confirmation lines (PID, log path); exits 1 if already running; JSON N/A",
		ni:     "no prompts; backgrounds itself by default",
	},
	"pharos daemon stop": {
		output: "single confirmation line; exit 1 when the daemon is not running; JSON N/A (single-line output)",
		ni:     "no prompts",
	},
	"pharos daemon status": {
		output: "JSON: {running, pid, port, started_at, servers: [{name, state, port, memory, last_activity, idle_timeout}]} (servers is [] when none). Plain: status line + managed-server table",
		env:    "PHAROS_JSON=1 or --json",
		ni:     "no prompts; exits 1 when the daemon state cannot be read",
	},
	"pharos daemon log": {
		output: "raw daemon log lines; JSON N/A (raw passthrough)",
		ni:     "no prompts",
	},
	"pharos daemon restart": {
		output: "stop + start confirmation lines; JSON N/A",
		ni:     "no prompts",
	},
	"pharos daemon autostart": {
		output: "single status line, or enable/disable confirmation with --on/--off; JSON N/A (single-line output)",
		ni:     "no prompts; --on/--off run the platform service manager",
	},
	"pharos doctor": {
		output: "JSON: {checks: [{name, status, detail}], failures, healthy}. Plain: per-check pass/fail list",
		env:    "PHAROS_JSON=1 or --json",
		ni:     "no prompts; the registry connectivity check requires network access",
	},
	"pharos health": {
		output: "JSON: {status, version, latency}. Plain: three labeled lines",
		env:    "PHAROS_JSON=1 or --json",
		ni:     "no prompts; requires registry access",
	},
	"pharos import": {
		output: "JSON: {lockfile, resolved, unresolved, servers: [{name, version, status}]}. Plain: per-server report + summary line",
		env:    "PHAROS_JSON=1 or --json",
		ni:     "no prompts; requires registry access to resolve servers",
	},
	"pharos info": {
		output: "JSON: full registry package detail (name, dist_tags, versions[] with manifest). Plain: labeled detail sections",
		env:    "PHAROS_JSON=1 or --json",
		ni:     "no prompts; requires registry access",
	},
	"pharos init": {
		output: "writes pharos.json (and .gitignore if absent) + confirmation lines; JSON N/A",
		env:    "PHAROS_NON_INTERACTIVE aborts with guidance unless the fix below is set; PHAROS_ASSUME_YES=1 accepts all defaults",
		ni:     "--yes, or PHAROS_ASSUME_YES=1 (writes the default manifest: my-mcp-server@0.1.0, stdio, node)",
	},
	"pharos install": {
		output: "progress lines + per-client config write results; JSON N/A",
		env:    "PHAROS_NON_INTERACTIVE makes the --select-clients picker return the detected defaults instead of launching its TUI",
		ni:     "no prompts by default (writes all detected clients); use --client to pin targets",
	},
	"pharos list": {
		output: "JSON: array of {name, version, transport, kind, status, endpoint, port, size, memory, uptime, idle, lastActivity} ([] when none installed). Plain: table",
		env:    "PHAROS_JSON=1 or --json",
		ni:     "no prompts",
	},
	"pharos lock": {
		output: "resolution tree + pharos.lock written; JSON N/A",
		ni:     "no prompts; requires registry access when dependencies are declared",
	},
	"pharos login": {
		output: "browser-flow URL + success line; token stored in ~/.pharos/credentials.json; JSON N/A",
		env:    "PHAROS_NON_INTERACTIVE aborts the browser flow with guidance instead of blocking on the OAuth callback",
		ni:     "--manual (token on stdin), or skip login entirely with 'pharos config token <token>'",
	},
	"pharos oauth configure": {
		output: "configuration confirmation lines; JSON N/A",
		ni:     "no prompts; --auth-url and --client-id are required",
	},
	"pharos package": {
		output: "packaging confirmation lines (tarball path + size); JSON N/A",
		ni:     "no prompts",
	},
	"pharos publish": {
		output: "4-phase progress lines (manifest, package, upload, publish) + confirmation; JSON N/A",
		ni:     "no prompts; use --dry-run to validate offline; requires --token or a stored token",
	},
	"pharos purge": {
		output: "per-version purge confirmations + final note; JSON N/A",
		env:    "PHAROS_ASSUME_YES=1 skips the destructive confirmation; PHAROS_NON_INTERACTIVE without it aborts with guidance (destructive commands are never guessed)",
		ni:     "--yes, or PHAROS_ASSUME_YES=1",
	},
	"pharos remove": {
		output: "per-target removal confirmations; exit 1 when the server is unknown or dependency-protected; JSON N/A",
		ni:     "no prompts; use --force to override dependency protection",
	},
	"pharos republish": {
		output: "JSON: {name, version, status: \"active\"}. Plain: single confirmation line",
		env:    "PHAROS_JSON=1 or --json",
		ni:     "no prompts; --version is required",
	},
	"pharos search": {
		output: "JSON: registry search response {results: [...], nextCursor, total}. Plain: results table + hint footer",
		env:    "PHAROS_JSON=1 or --json",
		ni:     "no prompts; requires registry access",
	},
	"pharos start": {
		output: "started confirmation lines (PID, port, log path); informational refusals for stdio/remote servers; JSON N/A",
		ni:     "no prompts",
	},
	"pharos stop": {
		output: "single stop confirmation per server; JSON N/A (single-line output)",
		ni:     "no prompts",
	},
	"pharos unpublish": {
		output: "per-version confirmations + final note; JSON N/A",
		env:    "PHAROS_ASSUME_YES=1 skips the confirmation; PHAROS_NON_INTERACTIVE without it aborts with guidance",
		ni:     "--yes, or PHAROS_ASSUME_YES=1",
	},
	"pharos update": {
		output: "JSON: {dry_run, updated, up_to_date, not_found, updates_available, servers: [{name, from, to, action}]} where action is one of updated, up_to_date, update_available, not_found, failed. Plain: per-server lines + summary",
		env:    "PHAROS_JSON=1 or --json",
		ni:     "no prompts; requires registry access",
	},
	"pharos version": {
		output: "JSON: {name, version}. Plain: \"pharos version X.Y.Z\"",
		env:    "PHAROS_JSON=1 or --json",
		ni:     "no prompts",
	},
	"pharos whoami": {
		output: "JSON: the authenticated user object (sub, namespace, scope, github_id, email, avatar_url, namespaces). Plain: labeled user lines; exit 1 when not logged in",
		env:    "PHAROS_JSON=1 or --json",
		ni:     "no prompts; requires credentials + registry access",
	},
	"pharos llmtxt": {
		output: "writes the generated reference file + one confirmation line; JSON N/A",
		ni:     "no prompts",
	},
}

var llmtxtCmd = &cobra.Command{
	Use:   "llmtxt [output-path]",
	Short: "Generate docs/llm.txt — machine-readable command reference for agents",
	Long: `Generate docs/llm.txt, the complete PHAROS command reference written for
AI agents and other tooling.

The file is generated from the Cobra command tree: per command it emits the
name, one-line description, usage line, flags (name/type/default/description),
the environment-variable contract (PHAROS_NON_INTERACTIVE, PHAROS_ASSUME_YES,
PHAROS_JSON), the output shape, and the non-interactive path.

Defaults to docs/llm.txt; pass a path to write elsewhere. Regenerate with
'go generate ./...' whenever commands or flags change.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := llmtxtDoc
		if len(args) == 1 {
			path = args[0]
		}
		if dir := filepath.Dir(path); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create output directory: %w", err)
			}
		}
		if err := os.WriteFile(path, []byte(GenerateLLMTxt()), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		fmt.Printf("%s  %s\n", ui.Success.Render("✓ Generated:"), path)
		return nil
	},
}

// GenerateLLMTxt renders the full docs/llm.txt content from the Cobra
// command tree. Output is deterministic: commands in alphabetical tree
// order, flags in pflag's lexical order.
func GenerateLLMTxt() string {
	// Materialize the auto-added commands and flags so generation never
	// depends on whether an Execute() happened first (cobra adds the help
	// command, the completion command, and the help/version flags lazily).
	rootCmd.InitDefaultHelpCmd()
	rootCmd.InitDefaultCompletionCmd()

	var b strings.Builder
	b.WriteString(llmtxtPreamble)

	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		if c.Hidden {
			return
		}
		c.InitDefaultHelpFlag()
		if c == rootCmd && c.Version != "" {
			c.InitDefaultVersionFlag()
		}
		writeLLMCommand(&b, c, path)

		subs := c.Commands()
		sort.Slice(subs, func(i, j int) bool { return subs[i].Name() < subs[j].Name() })
		for _, sub := range subs {
			walk(sub, path+" "+sub.Name())
		}
	}
	walk(rootCmd, "pharos")
	return b.String()
}

// llmtxtPreamble is the env-contract header emitted at the top of
// docs/llm.txt.
const llmtxtPreamble = `# PHAROS CLI — agent command reference (docs/llm.txt)
# Generated by 'pharos llmtxt' from the Cobra command tree. Do not edit by
# hand; regenerate with 'go generate ./...' (or 'pharos llmtxt').
#
# Environment contract (honored by every command):
#   PHAROS_NON_INTERACTIVE=1  never launch an interactive prompt or TUI;
#                             every command has a non-interactive path.
#   PHAROS_ASSUME_YES=1       answer "yes" to confirmation prompts, including
#                             destructive ones (init, purge, unpublish).
#   PHAROS_JSON=1             emit machine-parsable JSON on stdout for every
#                             command that has a --json flag (same output as
#                             passing --json).
# Values: unset/empty = false; "1"/"true"/"yes" (case-insensitive) = true;
# anything else ("0", "false", "no", garbage) = false — liberal, never an
# error.
# Guarantees:
#   1. Every command completes without a terminal given its required args.
#   2. Commands with --json emit valid JSON on stdout.
#   3. If a command would need an interactive answer in non-interactive mode,
#      it exits 1 with an error naming the flag that fixes it (e.g. "init
#      requires --yes or PHAROS_ASSUME_YES=1 in non-interactive mode").
# Human-facing docs: README.md. Generated: this file.

`

// writeLLMCommand appends one command section in the stable llm.txt format.
func writeLLMCommand(b *strings.Builder, c *cobra.Command, path string) {
	note, noted := llmNotes[path]
	if !noted {
		note = llmNote{
			output: "human-readable text; JSON N/A",
			ni:     "no prompts",
		}
	}

	fmt.Fprintf(b, "# COMMAND: %s\n", path)
	fmt.Fprintf(b, "SHORT: %s\n", c.Short)
	fmt.Fprintf(b, "USAGE: %s\n", c.UseLine())
	if len(c.Aliases) > 0 {
		fmt.Fprintf(b, "ALIASES: %s\n", strings.Join(c.Aliases, ", "))
	}

	flags := c.NonInheritedFlags()
	var flagLines []string
	flags.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		flagLines = append(flagLines, fmt.Sprintf("  --%s <%s> (default: %s) %s",
			f.Name, f.Value.Type(), f.DefValue, f.Usage))
	})
	if len(flagLines) > 0 {
		b.WriteString("FLAGS:\n")
		for _, l := range flagLines {
			b.WriteString(l)
			b.WriteString("\n")
		}
	} else {
		b.WriteString("FLAGS: none\n")
	}

	subs := c.Commands()
	var subNames []string
	for _, sub := range subs {
		if !sub.Hidden {
			subNames = append(subNames, sub.Name())
		}
	}
	if len(subNames) > 0 {
		sort.Strings(subNames)
		fmt.Fprintf(b, "SUBCOMMANDS: %s\n", strings.Join(subNames, ", "))
	}

	fmt.Fprintf(b, "OUTPUT: %s\n", note.output)
	if note.env != "" {
		fmt.Fprintf(b, "ENV: %s\n", note.env)
	}
	fmt.Fprintf(b, "NON-INTERACTIVE: %s\n", note.ni)
	b.WriteString("\n")
}

func init() {
	rootCmd.AddCommand(llmtxtCmd)
}
