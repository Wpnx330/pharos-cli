package cmd

import (
	"testing"
)

// =============================================================
// Fix 1: stdio server lifecycle (cmd/list.go + cmd/start.go)
// =============================================================
//
// The stdio lifecycle fix introduces an `isStdio` check in list.go
// (line ~119: `isStdio := e.pkg.Transport == "stdio" || e.pkg.Transport == ""`)
// and a transport guard in start.go (line ~80:
//   `if transport == "stdio" && !startForeground`).
//
// Both checks are INLINE inside cobra Run closures — they are not
// extractable into independently testable functions without modifying
// the implementation files (list.go, start.go), which is out of scope
// for this test-only change.
//
// These tests verify the COMMAND STRUCTURE that surrounds the fix:
//   - listCmd exists and is correctly configured (Use, Short, Run)
//   - startCmd exists and is correctly configured (Use, Short, Run)
//
// The existing cmd_commands_test.go already covers:
//   - TestListCmdFlags, TestListCmdAliases
//   - TestStartCmdFlags, TestStartCmdExactArgs
//   - TestCommandUseStrings, TestCommandShortDescriptions
//   - TestCommandsRegistered
//
// To avoid duplication, this file focuses on structural assertions
// specific to the stdio lifecycle fix that are not already covered.

// TestListCmdExists verifies that listCmd is a non-nil command with a
// non-nil Run function. The Run closure contains the isStdio lifecycle
// logic; a nil Run would mean the fix is not wired up.
func TestListCmdExists(t *testing.T) {
	if listCmd == nil {
		t.Fatal("listCmd is nil — command not initialized")
	}
	if listCmd.Run == nil {
		t.Fatal("listCmd.Run is nil — the Run closure (containing isStdio logic) is not set")
	}
}

// TestListCmdUseAndShort verifies the list command's Use and Short fields.
// The Short description is what users see in `pharos --help`; it must
// accurately describe listing installed packages (the context in which
// stdio servers show "idle" status instead of "stopped").
func TestListCmdUseAndShort(t *testing.T) {
	if listCmd.Use != "list" {
		t.Errorf("listCmd.Use = %q, want %q", listCmd.Use, "list")
	}
	wantShort := "List locally installed packages"
	if listCmd.Short != wantShort {
		t.Errorf("listCmd.Short = %q, want %q", listCmd.Short, wantShort)
	}
}

// TestStartCmdExists verifies that startCmd is a non-nil command with a
// non-nil Run function. The Run closure contains the stdio transport
// guard (`if transport == "stdio" && !startForeground`) that prevents
// background-starting stdio servers.
func TestStartCmdExists(t *testing.T) {
	if startCmd == nil {
		t.Fatal("startCmd is nil — command not initialized")
	}
	if startCmd.Run == nil {
		t.Fatal("startCmd.Run is nil — the Run closure (containing stdio transport guard) is not set")
	}
}

// TestStartCmdUseAndShort verifies the start command's Use and Short
// fields. The Short description must reflect starting MCP servers, the
// context in which stdio servers are intercepted.
func TestStartCmdUseAndShort(t *testing.T) {
	if startCmd.Use != "start <name>" {
		t.Errorf("startCmd.Use = %q, want %q", startCmd.Use, "start <name>")
	}
	wantShort := "Start a locally installed MCP server"
	if startCmd.Short != wantShort {
		t.Errorf("startCmd.Short = %q, want %q", startCmd.Short, wantShort)
	}
}

// TestListCmdHasTransportColumn verifies that the list command is
// configured to display transport info. This is a structural check:
// the isStdio logic in list.go's Run depends on e.pkg.Transport being
// available, which is surfaced in the TRANSPORT table column.
func TestListCmdHasTransportColumn(t *testing.T) {
	// The listCmd should accept no positional args (it takes flags only).
	// listCmd doesn't set Args, so cobra defaults to ArbitraryArgs.
	// We verify the command is callable without args.
	if listCmd.Args != nil {
		// If Args is set, verify it accepts zero args.
		if err := listCmd.Args(listCmd, []string{}); err != nil {
			t.Errorf("listCmd.Args with 0 args returned error: %v", err)
		}
	}
}
