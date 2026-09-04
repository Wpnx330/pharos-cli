package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/canonical"
	"github.com/Wpnx330/pharos-cli/internal/mcpclient"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var (
	tryJSON    bool
	tryInspect bool
	tryTimeout time.Duration
)

var tryCmd = &cobra.Command{
	Use:   "try <name>",
	Short: "Probe a server's live capabilities without wiring it into any client",
	Long: ui.Label.Render("pharos try") + ` — the 5-second capability check (try before you wire).

Resolves <name> from ~/.pharos/mcp.json, spawns the stdio server once,
runs the MCP initialize handshake, and prints the server's real
tools/resources/prompts. Nothing is written to any client config.

Examples:
  pharos try echo-server            # capability summary
  pharos try echo-server --json     # machine-parsable report
  pharos try echo-server --timeout 30s
  pharos try echo-server --inspect  # launch MCP Inspector (needs npx)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := runTry(args)
		if err == nil {
			return nil
		}
		te, ok := err.(*tryError)
		if !ok || te.Code != 2 {
			return err // runtime failure — cobra prints it; pharos exits 1
		}
		fmt.Fprintln(os.Stderr, ui.Error.Render("✗"), te.Message)
		if te.Hint != "" {
			fmt.Fprintf(os.Stderr, "  %s  %s\n", ui.Muted.Render("Hint:"), te.Hint)
		}
		os.Exit(2) // usage/validation — mirrors profile/config exit codes
		return nil
	},
}

// tryError carries an exit code (1 = runtime failure, 2 = usage/validation)
// plus an optional hint line, so tests can assert behavior without os.Exit.
type tryError struct {
	Code    int
	Message string
	Hint    string
}

func (e *tryError) Error() string { return e.Message }

// tryJSONOut is the `pharos try --json` document. Errors and stderr_tail
// appear only when probing failed; inspect_command only with --inspect --json.
type tryJSONOut struct {
	Server     string          `json:"server"`
	Caps       *mcpclient.Caps `json:"caps,omitempty"`
	Errors     []string        `json:"errors,omitempty"`
	StderrTail []string        `json:"stderr_tail,omitempty"`
	InspectCmd string          `json:"inspect_command,omitempty"`
}

// runTry is the testable core of `pharos try`. JSON output goes to stdout
// (single document); progress and human detail go to stderr in JSON mode
// and to their natural streams otherwise.
func runTry(args []string) error {
	name := args[0]

	srv, err := canonical.GetServer(name)
	if err != nil {
		return &tryError{Code: 1, Message: fmt.Sprintf("read canonical config: %v", err)}
	}
	if srv == nil {
		return &tryError{
			Code:    2,
			Message: fmt.Sprintf("server %q not found in ~/.pharos/mcp.json", name),
			Hint:    fmt.Sprintf("install it first: pharos install %s (probe-only --sandbox installs are planned)", name),
		}
	}
	if !strings.EqualFold(srv.Transport, "stdio") || strings.TrimSpace(srv.Command) == "" {
		return &tryError{
			Code: 1,
			Message: fmt.Sprintf(
				"server %q is not a stdio server (transport %q, command %q) — pharos try probes stdio servers only",
				name, srv.Transport, srv.Command),
		}
	}

	command := append([]string{srv.Command}, srv.Args...)

	if tryInspect {
		return runTryInspect(name, srv, command)
	}

	tryProgressf("Probing %s…\n", name)

	caps, perr := mcpclient.Probe(context.Background(), command, srv.Env, srv.Cwd, tryTimeout)
	if perr != nil {
		if JSONRequested() {
			out := &tryJSONOut{Server: name, Errors: []string{perr.Error()}}
			var pe *mcpclient.ProbeError
			if errors.As(perr, &pe) {
				out.StderrTail = pe.StderrTail
			}
			printTryJSON(out)
		} else {
			// The one-line error is printed by cobra on return; here we add
			// the part agents and humans actually need: the server's own
			// stderr.
			var pe *mcpclient.ProbeError
			if errors.As(perr, &pe) && len(pe.StderrTail) > 0 {
				fmt.Fprintln(os.Stderr, ui.Muted.Render("  server stderr (last lines):"))
				for _, line := range pe.StderrTail {
					fmt.Fprintf(os.Stderr, "    %s\n", line)
				}
			}
		}
		return &tryError{Code: 1, Message: perr.Error()}
	}

	if JSONRequested() {
		printTryJSON(&tryJSONOut{Server: name, Caps: caps})
		return nil
	}
	printTrySummary(name, caps)
	return nil
}

// runTryInspect wires MCP Inspector (via npx) to the server: print the
// exact command, then — in text mode with npx on PATH — run it in the
// foreground. JSON mode only reports the command (spawning an interactive
// tool would break the no-promises JSON contract).
func runTryInspect(name string, srv *canonical.Server, command []string) error {
	args := append([]string{"-y", "@modelcontextprotocol/inspector"}, command...)
	shown := quotedCommand("npx", args)
	if JSONRequested() {
		printTryJSON(&tryJSONOut{Server: name, InspectCmd: shown})
		return nil
	}
	fmt.Printf("%s  %s\n", ui.Label.Render("Inspector command:"), shown)

	exe, err := exec.LookPath("npx")
	if err != nil {
		return &tryError{
			Code:    1,
			Message: "npx not found in $PATH — install Node.js/npm to run MCP Inspector (https://nodejs.org)",
		}
	}

	fmt.Printf("%s  launching MCP Inspector (foreground)…\n", ui.Muted.Render("·"))
	icmd := exec.Command(exe, args...)
	icmd.Stdin, icmd.Stdout, icmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	icmd.Dir = srv.Cwd
	icmd.Env = append(os.Environ(), envPairs(srv.Env)...)
	if err := icmd.Run(); err != nil {
		return &tryError{Code: 1, Message: fmt.Sprintf("MCP Inspector exited: %v", err)}
	}
	return nil
}

// printTrySummary renders the human capability report: header, tools
// table (descriptions truncated to 72 columns, newline-collapsed), then
// resource/prompt counts with names only when the set is small.
func printTrySummary(name string, caps *mcpclient.Caps) {
	display := caps.ServerInfo.Name
	if display == "" {
		display = name
	}
	fmt.Printf("%s  %s", ui.Success.Render("✓"), ui.PackageName.Render(display))
	if caps.ServerInfo.Version != "" {
		fmt.Printf(" %s", ui.Muted.Render("v"+caps.ServerInfo.Version))
	}
	fmt.Printf(" %s %s\n", ui.Muted.Render("—"), ui.Muted.Render("MCP "+caps.ProtocolVersion))

	if len(caps.Tools) == 0 {
		fmt.Printf("\n  %s\n", ui.Muted.Render("TOOLS: none"))
	} else {
		fmt.Printf("\n  %s\n", ui.Label.Render(fmt.Sprintf("TOOLS (%d)", len(caps.Tools))))
		for _, t := range caps.Tools {
			fmt.Printf("    %-24s %s\n",
				truncateCols(t.Name, 24),
				truncateCols(oneLine(t.Description), 72))
		}
	}
	printNameList("RESOURCES", caps.Resources)
	printNameList("PROMPTS", caps.Prompts)
}

func printNameList(label string, names []string) {
	switch {
	case len(names) == 0:
		fmt.Printf("  %s\n", ui.Muted.Render(label+": none"))
	case len(names) <= 10:
		fmt.Printf("  %s  %s\n", ui.Label.Render(fmt.Sprintf("%s (%d)", label, len(names))),
			ui.Muted.Render(strings.Join(names, ", ")))
	default:
		fmt.Printf("  %s\n", ui.Label.Render(fmt.Sprintf("%s (%d)", label, len(names))))
	}
}

// printTryJSON emits the single-document JSON report to stdout.
func printTryJSON(out *tryJSONOut) {
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return
	}
	fmt.Println(string(data))
}

// tryProgressf sends progress lines to stderr in JSON mode (stdout must
// stay a single JSON document) and to stdout otherwise.
func tryProgressf(format string, a ...any) {
	if JSONRequested() {
		fmt.Fprintf(os.Stderr, format, a...)
		return
	}
	fmt.Printf(format, a...)
}

// oneLine collapses a multi-line description to a single spaced line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncateCols cuts a string to n columns (runes, not bytes) with an
// ellipsis — safe for CJK and emoji descriptions.
func truncateCols(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// quotedCommand renders a command line, quoting arguments that contain
// whitespace so the printed command is copy-pasteable.
func quotedCommand(exe string, args []string) string {
	parts := append([]string{exe}, args...)
	for i, p := range parts {
		if strings.ContainsAny(p, " \t") {
			parts[i] = strconv.Quote(p)
		}
	}
	return strings.Join(parts, " ")
}

func envPairs(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

func init() {
	tryCmd.Flags().BoolVar(&tryJSON, "json", false, "output as JSON")
	tryCmd.Flags().BoolVar(&tryInspect, "inspect", false, "launch MCP Inspector (npx) against the server instead of probing")
	tryCmd.Flags().DurationVar(&tryTimeout, "timeout", 10*time.Second, "total probe budget (each request is also capped at 10s)")
	rootCmd.AddCommand(tryCmd)
}
