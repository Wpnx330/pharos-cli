package cmd

import (
	"fmt"
	"os"
	"strings"
)

// ── PHAROS agent automation contract ─────────────────────────────────────────
//
// Every PHAROS command honors three environment variables so agents (and CI)
// can drive the CLI without a terminal:
//
//	PHAROS_NON_INTERACTIVE=1  never launch an interactive prompt or TUI;
//	                          every command has a non-interactive path.
//	PHAROS_ASSUME_YES=1       assume "yes" for confirmation prompts, including
//	                          destructive ones (purge, unpublish, init).
//	PHAROS_JSON=1             machine-parsable JSON on stdout for every
//	                          command that has a --json flag; equivalent to
//	                          passing --json.
//
// Value parsing is deliberately liberal: unset or empty means false,
// "1"/"true"/"yes" (case-insensitive, whitespace-trimmed) means true, and
// anything else ("0", "false", "no", or garbage) means false — never an error.
// The full generated reference lives in docs/llm.txt (`pharos llmtxt`).

// envTruthy reports whether the named environment variable is set to a
// truthy value: "1", "true", or "yes" (case-insensitive, trimmed).
// Unset, empty, "0", "false", "no", and any unrecognized value are false.
func envTruthy(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// NonInteractive reports whether PHAROS_NON_INTERACTIVE is truthy. When set,
// commands must never launch an interactive prompt (bubbletea TUIs or stdin
// reads); instead they either take a default, or fail with an error from
// RequireNonInteractive naming the flag that fixes it.
func NonInteractive() bool {
	return envTruthy("PHAROS_NON_INTERACTIVE")
}

// AssumeYes reports whether PHAROS_ASSUME_YES is truthy. When set, yes/no
// confirmation prompts are answered "yes" without reading stdin.
func AssumeYes() bool {
	return envTruthy("PHAROS_ASSUME_YES")
}

// JSONRequested reports whether machine-parsable JSON output was requested:
// either PHAROS_JSON=1 in the environment, or the running command's own
// --json flag was set.
//
// The flag side is detected via the package-level flag variables each
// command binds its --json flag to (jsonFlag, listJSON, doctorJSON,
// auditJSON, and the per-command vars below). Only one command executes per
// process, so the union of those variables is exactly "the command's own
// --json flag".
func JSONRequested() bool {
	if envTruthy("PHAROS_JSON") {
		return true
	}
	return jsonFlag || listJSON || doctorJSON || auditJSON ||
		versionJSON || daemonStatusJSON || configJSON ||
		importJSON || republishJSON || updateJSON || profileJSON ||
		tryJSON
}

// NonInteractiveError is the typed error returned when a command reaches an
// interactive path while PHAROS_NON_INTERACTIVE is set. Its message always
// names the flag (or env var) that resolves the situation.
type NonInteractiveError struct {
	Command string
	Fix     string
}

// Error implements the error interface with the contract's guidance format:
// "<command> requires <fix> in non-interactive mode".
func (e *NonInteractiveError) Error() string {
	return fmt.Sprintf("%s requires %s in non-interactive mode", e.Command, e.Fix)
}

// RequireNonInteractive builds the guidance error for an interactive path hit
// under PHAROS_NON_INTERACTIVE=1. fix must name the flag or env var that
// resolves it, e.g. "--yes or PHAROS_ASSUME_YES=1".
func RequireNonInteractive(command, fix string) error {
	return &NonInteractiveError{Command: command, Fix: fix}
}
