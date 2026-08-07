package cmd

import (
	"testing"
)

// =============================================================
// Fix 2 + Fix 3: install transport routing + --no-dep-config flag
// =============================================================
//
// Fix 2 (transport routing): install.go routes installation based on
// transport type — stdio packages get downloaded/extracted, http/sse
// packages with a bin field also get downloaded, and pure-remote
// http/sse packages (endpoint only) skip the tarball download.
//
// Fix 3 (--no-dep-config flag): install.go registers a --no-dep-config
// flag that, when set, skips writing MCP client configs for dependencies.
// This is used when the user wants the primary package configured but
// not its transitive deps.
//
// Both fixes are inline in runInstall() (a cobra Run handler). These
// tests verify the FLAG REGISTRATION that gates the fix behavior, since
// the routing logic itself is not extractable without modifying impl.

// TestInstallCmdExists verifies that installCmd is initialized.
func TestInstallCmdExists(t *testing.T) {
	if installCmd == nil {
		t.Fatal("installCmd is nil — command not initialized")
	}
	if installCmd.Run == nil {
		t.Fatal("installCmd.Run is nil — Run handler not set")
	}
}

// TestInstallCmdNoDepConfigFlag verifies that the --no-dep-config flag
// (Fix 3) is registered on the install command and defaults to false.
func TestInstallCmdNoDepConfigFlag(t *testing.T) {
	f := installCmd.Flags().Lookup("no-dep-config")
	if f == nil {
		t.Fatal("install command missing --no-dep-config flag")
	}
	if f.DefValue != "false" {
		t.Errorf("install --no-dep-config default = %s, want false", f.DefValue)
	}
	// Verify it's a bool flag
	if f.Value.Type() != "bool" {
		t.Errorf("install --no-dep-config type = %s, want bool", f.Value.Type())
	}
}

// TestInstallCmdNoDepConfigFlagSetTrue verifies that --no-dep-config
// can be set to true via the flag API, simulating `pharos install --no-dep-config`.
func TestInstallCmdNoDepConfigFlagSetTrue(t *testing.T) {
	// Save and restore the flag value
	original := installSkipDepConfig
	defer func() { installSkipDepConfig = original }()

	if err := installCmd.Flags().Set("no-dep-config", "true"); err != nil {
		t.Fatalf("failed to set --no-dep-config=true: %v", err)
	}
	if !installSkipDepConfig {
		t.Error("after setting --no-dep-config=true, installSkipDepConfig should be true")
	}
}

// TestInstallCmdNoDepConfigFlagSetFalse verifies that --no-dep-config
// can be explicitly set to false.
func TestInstallCmdNoDepConfigFlagSetFalse(t *testing.T) {
	original := installSkipDepConfig
	defer func() { installSkipDepConfig = original }()

	if err := installCmd.Flags().Set("no-dep-config", "true"); err != nil {
		t.Fatal(err)
	}
	if err := installCmd.Flags().Set("no-dep-config", "false"); err != nil {
		t.Fatalf("failed to set --no-dep-config=false: %v", err)
	}
	if installSkipDepConfig {
		t.Error("after setting --no-dep-config=false, installSkipDepConfig should be false")
	}
}

// TestInstallCmdFrozenFlag verifies that the pre-existing --frozen flag
// is registered on the install command and defaults to false.
func TestInstallCmdFrozenFlag(t *testing.T) {
	f := installCmd.Flags().Lookup("frozen")
	if f == nil {
		t.Fatal("install command missing --frozen flag")
	}
	if f.DefValue != "false" {
		t.Errorf("install --frozen default = %s, want false", f.DefValue)
	}
	if f.Value.Type() != "bool" {
		t.Errorf("install --frozen type = %s, want bool", f.Value.Type())
	}
}

// TestInstallCmdVersionFlag verifies the pre-existing --version flag.
// This flag interacts with transport routing: --version takes precedence
// over name@version syntax for version resolution.
func TestInstallCmdVersionFlag(t *testing.T) {
	f := installCmd.Flags().Lookup("version")
	if f == nil {
		t.Fatal("install command missing --version flag")
	}
	if f.DefValue != "" {
		t.Errorf("install --version default = %s, want empty string", f.DefValue)
	}
}

// TestInstallCmdGlobalFlag verifies the pre-existing --global flag.
func TestInstallCmdGlobalFlag(t *testing.T) {
	f := installCmd.Flags().Lookup("global")
	if f == nil {
		t.Fatal("install command missing --global flag")
	}
	if f.DefValue != "false" {
		t.Errorf("install --global default = %s, want false", f.DefValue)
	}
}

// TestInstallCmdClientFlag verifies the pre-existing --client flag.
func TestInstallCmdClientFlag(t *testing.T) {
	f := installCmd.Flags().Lookup("client")
	if f == nil {
		t.Fatal("install command missing --client flag")
	}
	if f.DefValue != "" {
		t.Errorf("install --client default = %s, want empty string", f.DefValue)
	}
}

// TestInstallCmdSelectClientsFlag verifies the pre-existing --select-clients flag.
func TestInstallCmdSelectClientsFlag(t *testing.T) {
	f := installCmd.Flags().Lookup("select-clients")
	if f == nil {
		t.Fatal("install command missing --select-clients flag")
	}
	if f.DefValue != "false" {
		t.Errorf("install --select-clients default = %s, want false", f.DefValue)
	}
}

// TestInstallCmdUseAndShort verifies the command's Use and Short fields.
func TestInstallCmdUseAndShort(t *testing.T) {
	if installCmd.Use != "install <name>[@version]" {
		t.Errorf("installCmd.Use = %q, want %q", installCmd.Use, "install <name>[@version]")
	}
	wantShort := "Download and install an MCP server package"
	if installCmd.Short != wantShort {
		t.Errorf("installCmd.Short = %q, want %q", installCmd.Short, wantShort)
	}
}

// TestInstallCmdExactArgs verifies install requires exactly 1 argument.
func TestInstallCmdExactArgs(t *testing.T) {
	if err := installCmd.Args(installCmd, []string{"server-name"}); err != nil {
		t.Errorf("installCmd.Args with 1 arg: unexpected error: %v", err)
	}
	if err := installCmd.Args(installCmd, []string{}); err == nil {
		t.Error("installCmd.Args with 0 args: expected error, got nil")
	}
	if err := installCmd.Args(installCmd, []string{"a", "b"}); err == nil {
		t.Error("installCmd.Args with 2 args: expected error, got nil")
	}
}
