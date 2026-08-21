package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// execRoot runs the root command with the given args and returns everything it
// wrote, restoring the command's global state afterwards.
func execRoot(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		// The version flag is sticky: leaving it set would short-circuit any
		// later Execute in this process.
		if f := rootCmd.Flags().Lookup("version"); f != nil {
			_ = f.Value.Set("false")
			f.Changed = false
		}
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("pharos %s: %v", strings.Join(args, " "), err)
	}
	return out.String()
}

// TestRootVersionFlag covers #6: `pharos --version` used to fail with
// "unknown flag: --version" because rootCmd.Version was never set.
func TestRootVersionFlag(t *testing.T) {
	got := execRoot(t, "--version")
	want := "pharos version " + Version
	if !strings.Contains(got, want) {
		t.Errorf("pharos --version = %q, want it to contain %q", got, want)
	}
}

func TestRootVersionShorthand(t *testing.T) {
	got := execRoot(t, "-v")
	want := "pharos version " + Version
	if !strings.Contains(got, want) {
		t.Errorf("pharos -v = %q, want it to contain %q", got, want)
	}
}

// TestVersionFlagMatchesSubcommand pins the two spellings to one rendering, so
// `pharos --version` and `pharos version` cannot drift apart.
func TestVersionFlagMatchesSubcommand(t *testing.T) {
	flagOut := strings.TrimSpace(execRoot(t, "--version"))
	subOut := strings.TrimSpace(versionLine())
	if flagOut != subOut {
		t.Errorf("--version printed %q, the version subcommand prints %q", flagOut, subOut)
	}
}

// TestInstallVersionShorthandUnaffected guards the one collision risk: `install`
// already uses -v for its own --version flag. Cobra registers the root version
// flag non-persistently, so the subcommand keeps its own.
func TestInstallVersionShorthandUnaffected(t *testing.T) {
	f := installCmd.Flags().Lookup("version")
	if f == nil {
		t.Fatal("install has no --version flag")
	}
	if f.Shorthand != "v" {
		t.Errorf("install --version shorthand = %q, want %q", f.Shorthand, "v")
	}
	if f.Usage == "" || !strings.Contains(f.Usage, "install") {
		t.Errorf("install --version is no longer the install flag: %q", f.Usage)
	}
}
