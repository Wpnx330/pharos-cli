// W2.2 — `pharos profile` (SPEC A1, Context Profiles, opt-in).
//
// A profile is a named context: a set of installed servers plus the MCP
// client IDs that context drives. Profiles are an ORCHESTRATION layer
// over installs — pharos.lock and ~/.pharos/mcp.json stay untouched by
// profile creation. `pharos profile use` is the retention hook: it
// reconciles the profile's mapped clients to contain exactly the
// profile's target set (base + inherited + own servers), plan-first with
// interactive confirmation (see profile_use.go).
//
// State: ~/.pharos/profiles.json via internal/profiles. Every subcommand
// honors the W1.1 contract: --json / PHAROS_JSON=1 (pure stdout JSON,
// never a prompt), PHAROS_ASSUME_YES=1 (confirms), PHAROS_NON_INTERACTIVE
// (guidance instead of prompts). Exit codes: 0 success/applied; 1
// nothing-done (declined); 2 usage/validation errors.
package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/canonical"
	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/config"
	"github.com/Wpnx330/pharos-cli/internal/profiles"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

// profileJSON is the --json flag bound by every profile subcommand (only
// one command executes per process, so a single shared variable is the
// running command's own flag — see JSONRequested in envcontract.go).
var profileJSON bool

// profileStdin is the reader for interactive confirmations. Tests swap
// it to simulate stdin.
var profileStdin io.Reader = os.Stdin

var profileCmd = &cobra.Command{
	Use:   "profile <create|add|remove|ls|use|rm|run>",
	Short: "Manage context profiles — named server sets mapped to clients",
	Long: ui.Label.Render("pharos profile") + ` — context profiles (opt-in).

A profile maps a set of installed servers to MCP clients: 'work' with
--client cursor means "this is my Cursor context". Profiles are an
orchestration layer over installs — pharos.lock and ~/.pharos/mcp.json
are untouched until you run 'pharos profile use', which reconciles the
mapped clients to contain exactly the profile's servers (plan first,
apply on confirmation; --dry-run previews, --yes skips the prompt).

Every profile implicitly inherits the 'base' profile so common servers
are listed once.

Examples:
  pharos profile create work --client cursor
  pharos profile create personal --client claude-desktop --inherit base
  pharos profile add work Context7 github   # already-installed servers
  pharos profile remove work github
  pharos profile ls --json                  # agent-visible context map
  pharos profile use work                   # plan, then confirm
  pharos profile use work --dry-run         # plan only, writes nothing
  pharos profile use work --yes             # apply without prompting
  pharos profile rm work                    # servers stay installed
  pharos profile run work                   # daemon: load profile set, idle others`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

// loadProfilesState loads profiles.json; on any error prints to stderr
// and exits 2 (usage/validation).
func loadProfilesState() *profiles.State {
	st, err := profiles.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot read profiles state:"), err)
		os.Exit(2)
	}
	return st
}

// validateClientIDs checks a comma-separated --client list against the
// known built-in and custom clients. Returns an error (exit 2 at the
// call site) on unknown IDs so tests can assert validation directly.
func validateClientIDs(spec string) ([]string, error) {
	ids := splitClientList(spec)
	if len(ids) == 0 {
		return nil, fmt.Errorf("--client requires at least one client ID (e.g. --client cursor)")
	}
	known := knownClientIDs()
	for _, id := range ids {
		if !known[id] {
			return nil, fmt.Errorf("client %q is not a known client — valid: %s", id, formatKnownClients(known))
		}
	}
	return ids, nil
}

// splitClientList splits a comma-separated list, trimming whitespace
// and dropping empties (input order preserved for validation messages;
// the state normalizes/sorts on save).
func splitClientList(spec string) []string {
	var out []string
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// knownClientIDs returns the set of valid client IDs: built-ins plus
// custom clients registered via `pharos config add-client`.
func knownClientIDs() map[string]bool {
	known := make(map[string]bool, len(clientconfig.AllClients))
	for _, id := range clientconfig.AllClients {
		known[string(id)] = true
	}
	if cfg, err := config.Load(); err == nil {
		for _, cc := range cfg.CustomClients {
			known[cc.ID] = true
		}
	}
	return known
}

// formatKnownClients renders the sorted valid-client list for errors.
func formatKnownClients(known map[string]bool) string {
	ids := make([]string, 0, len(known))
	for id := range known {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return strings.Join(ids, ", ")
}

// emitProfileJSON prints one profile command's JSON document as the only
// stdout output.
func emitProfileJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// ── profile create ──────────────────────────────────────────────────────

var (
	profileCreateClient  string
	profileCreateInherit string
)

var profileCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a context profile and map it to clients",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		created, code, err := createProfile(args[0], profileCreateInherit, profileCreateClient)
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot create profile:"), err)
			os.Exit(code)
		}

		type createdOut struct {
			Created  string   `json:"created"`
			Inherits []string `json:"inherits"`
			Servers  []string `json:"servers"`
			Clients  []string `json:"clients"`
			State    string   `json:"state"`
		}
		out := createdOut{
			Created:  args[0],
			Inherits: created.Inherits,
			Servers:  created.Servers,
			Clients:  created.Clients,
		}
		if out.State, _ = profiles.FilePath(); out.State == "" {
			out.State = "~/.pharos/profiles.json"
		}
		if JSONRequested() {
			return emitProfileJSON(out)
		}
		inherited := "base (implicit)"
		if len(created.Inherits) == 1 {
			inherited = "base (implicit) + " + created.Inherits[0]
		}
		fmt.Printf("%s  Created profile %q\n", ui.Success.Render("✓"), args[0])
		if len(created.Clients) > 0 {
			fmt.Printf("%s    clients: %s\n", ui.Muted.Render("·"), strings.Join(created.Clients, ", "))
		}
		fmt.Printf("%s    inherits: %s\n", ui.Muted.Render("·"), inherited)
		fmt.Printf("%s  %s\n", ui.Muted.Render("Next:"),
			fmt.Sprintf("pharos profile add %s <server...> — then 'pharos profile use %s' to apply", args[0], args[0]))
		return nil
	},
}

// createProfile is the testable core of `pharos profile create` (the
// adopt pattern: logic returns the exit code; the cobra wrapper exits).
// clientSpec is the raw comma-separated --client value ("" = none).
func createProfile(name, parent, clientSpec string) (profiles.Profile, int, error) {
	var clients []string
	if clientSpec != "" {
		ids, err := validateClientIDs(clientSpec)
		if err != nil {
			return profiles.Profile{}, 2, err
		}
		clients = ids
	}
	st, err := profiles.Load()
	if err != nil {
		return profiles.Profile{}, 2, err
	}
	if err := st.Create(name, parent, clients); err != nil {
		return profiles.Profile{}, 2, err
	}
	if err := st.Save(); err != nil {
		return profiles.Profile{}, 2, err
	}
	return st.Profiles[name], 0, nil
}

// ── profile add ─────────────────────────────────────────────────────────

var profileAddCmd = &cobra.Command{
	Use:   "add <profile> <server...>",
	Short: "Attach already-installed servers to a profile",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		added, all, code, err := addProfileServers(args[0], args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot update profile:"), err)
			os.Exit(code)
		}

		type addedOut struct {
			Profile string   `json:"profile"`
			Added   []string `json:"added"`
			Servers []string `json:"servers"`
		}
		out := addedOut{Profile: args[0], Added: added, Servers: all}
		if JSONRequested() {
			return emitProfileJSON(out)
		}
		for _, srv := range added {
			fmt.Printf("%s  %s → %s\n", ui.Success.Render("✓"), srv, args[0])
		}
		fmt.Printf("%s  %d server(s) in %s. Apply with 'pharos profile use %s'.\n",
			ui.Muted.Render("·"), len(all), args[0], args[0])
		return nil
	},
}

// addProfileServers is the testable core of `pharos profile add`.
// Servers must already be installed (present in the canonical config).
func addProfileServers(name string, servers []string) (added, all []string, code int, err error) {
	st, err := profiles.Load()
	if err != nil {
		return nil, nil, 2, err
	}
	if !st.HasProfile(name) {
		return nil, nil, 2, fmt.Errorf("profile %q does not exist", name)
	}
	canon, err := canonical.Load()
	if err != nil {
		return nil, nil, 2, fmt.Errorf("read canonical config: %w", err)
	}
	var missing []string
	for _, srv := range servers {
		if _, ok := canon.Servers[srv]; !ok {
			missing = append(missing, srv)
		}
	}
	if len(missing) > 0 {
		return nil, nil, 2, fmt.Errorf("not installed (no canonical config): %s — run 'pharos install <name>' first", strings.Join(missing, ", "))
	}
	if err := st.AddServers(name, servers...); err != nil {
		return nil, nil, 2, err
	}
	if err := st.Save(); err != nil {
		return nil, nil, 2, err
	}
	return dedupSorted(servers), st.Profiles[name].Servers, 0, nil
}

// ── profile remove ──────────────────────────────────────────────────────

var profileRemoveCmd = &cobra.Command{
	Use:   "remove <profile> <server...>",
	Short: "Detach servers from a profile (they stay installed)",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		removed, all, code, err := removeProfileServers(args[0], args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot update profile:"), err)
			os.Exit(code)
		}

		type removedOut struct {
			Profile string   `json:"profile"`
			Removed []string `json:"removed"`
			Servers []string `json:"servers"`
		}
		out := removedOut{Profile: args[0], Removed: removed, Servers: all}
		if JSONRequested() {
			return emitProfileJSON(out)
		}
		for _, srv := range removed {
			fmt.Printf("%s  %s detached from %s (still installed)\n", ui.Success.Render("✓"), srv, args[0])
		}
		for _, srv := range dedupSorted(args[1:]) {
			if !containsString(removed, srv) {
				fmt.Printf("%s  %s was not in %s\n", ui.Muted.Render("—"), srv, args[0])
			}
		}
		return nil
	},
}

// removeProfileServers is the testable core of `pharos profile remove`.
// Servers not in the profile are skipped (reported, never an error).
func removeProfileServers(name string, servers []string) (removed, all []string, code int, err error) {
	st, err := profiles.Load()
	if err != nil {
		return nil, nil, 2, err
	}
	if !st.HasProfile(name) {
		return nil, nil, 2, fmt.Errorf("profile %q does not exist", name)
	}
	gone, err := st.RemoveServers(name, servers...)
	if err != nil {
		return nil, nil, 2, err
	}
	if err := st.Save(); err != nil {
		return nil, nil, 2, err
	}
	return gone, st.Profiles[name].Servers, 0, nil
}

// ── profile ls ──────────────────────────────────────────────────────────

type profileLsEntry struct {
	Inherits  []string `json:"inherits"`
	Servers   []string `json:"servers"`
	Clients   []string `json:"clients"`
	TargetSet []string `json:"target_set"`
}

type profileLsReport struct {
	Version  int                       `json:"version"`
	State    string                    `json:"state"`
	Profiles map[string]profileLsEntry `json:"profiles"`
}

var profileLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List context profiles and their client mappings",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		st := loadProfilesState()

		report := profileLsReport{
			Version:  profiles.SchemaVersion,
			Profiles: make(map[string]profileLsEntry, len(st.Profiles)),
		}
		if report.State, _ = profiles.FilePath(); report.State == "" {
			report.State = "~/.pharos/profiles.json"
		}

		names := make([]string, 0, len(st.Profiles))
		for name := range st.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			target, err := st.TargetSet(name)
			if err != nil {
				target = nil
			}
			p := st.Profiles[name]
			report.Profiles[name] = profileLsEntry{
				Inherits:  p.Inherits,
				Servers:   p.Servers,
				Clients:   p.Clients,
				TargetSet: target,
			}
		}

		if JSONRequested() {
			return emitProfileJSON(report)
		}

		if len(names) == 1 && names[0] == profiles.BaseProfile {
			fmt.Println(ui.Muted.Render("No profiles yet (only the implicit 'base')."))
			fmt.Printf("%s  %s\n", ui.Muted.Render("Try:"), "pharos profile create work --client cursor")
			return nil
		}

		var rows [][]string
		for _, name := range names {
			p := st.Profiles[name]
			inherited := "base (implicit)"
			if len(p.Inherits) == 1 {
				inherited = p.Inherits[0]
			}
			rows = append(rows, []string{
				name,
				strings.Join(p.Clients, ", "),
				inherited,
				fmt.Sprintf("%d", len(p.Servers)),
			})
		}
		fmt.Print(ui.RenderTable([]ui.TableColumn{
			{Title: "PROFILE"},
			{Title: "CLIENTS"},
			{Title: "INHERITS"},
			{Title: "SERVERS"},
		}, rows))
		fmt.Printf("%s  %s\n", ui.Muted.Render("Apply:"), "pharos profile use <name>")
		return nil
	},
}

// ── profile rm ──────────────────────────────────────────────────────────

var profileRmYes bool

var profileRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Delete a profile (servers stay installed)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		code, err := deleteProfile(args[0], profileRmYes || AssumeYes())
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot delete profile:"), err)
			os.Exit(code)
		}
		if code != 0 {
			os.Exit(code)
		}

		type deletedOut struct {
			Deleted     string `json:"deleted"`
			ServersKept bool   `json:"servers_kept"`
		}
		out := deletedOut{Deleted: args[0], ServersKept: true}
		if JSONRequested() {
			return emitProfileJSON(out)
		}
		fmt.Printf("%s  Deleted profile %q (servers stay installed; remove with 'pharos remove <name>').\n",
			ui.Success.Render("✓"), args[0])
		return nil
	},
}

// profileRmDeclined is the minimal JSON document `profile rm` emits when
// JSON mode does nothing (W1.1: JSON stdout is always exactly one
// document — the decline path must never be stderr-only).
type profileRmDeclined struct {
	Deleted bool   `json:"deleted"`
	Reason  string `json:"reason"`
}

// deleteProfile is the testable core of `pharos profile rm`. yes skips
// the interactive confirm (PHAROS_ASSUME_YES=1 counts as yes, per the
// W1.1 contract); a declined prompt returns (1, nil) — nothing done.
// JSON mode never prompts: an unconfirmed run declines immediately and
// emits the {deleted: false, reason} document so stdout stays a single
// machine-parsable doc. Errors are validation/state failures (code 2).
func deleteProfile(name string, yes bool) (int, error) {
	st, err := profiles.Load()
	if err != nil {
		return 2, err
	}
	if !st.HasProfile(name) {
		return 2, fmt.Errorf("profile %q does not exist", name)
	}
	if !yes && !AssumeYes() {
		if JSONRequested() {
			// W1.1: JSON stdout is a single document — no prompt, no
			// prompt text on stdout. Decline unless the run was confirmed
			// via --yes / PHAROS_ASSUME_YES=1.
			_ = emitProfileJSON(profileRmDeclined{
				Reason: "not confirmed — re-run with --yes or PHAROS_ASSUME_YES=1 to delete",
			})
			return 1, nil
		}
		if NonInteractive() {
			fmt.Fprintln(os.Stderr, RequireNonInteractive("profile rm", "--yes or PHAROS_ASSUME_YES=1").Error())
			return 1, nil
		}
		fmt.Printf("Delete profile %q? Servers stay installed. [y/N] ", name)
		answer, _ := readProfileLine()
		if !isProfileYes(answer) {
			fmt.Println(ui.Muted.Render("Aborted — profile kept."))
			return 1, nil
		}
	}
	if err := st.Delete(name); err != nil {
		return 2, err
	}
	if err := st.Save(); err != nil {
		return 2, err
	}
	return 0, nil
}

// readProfileLine reads one line from profileStdin.
func readProfileLine() (string, error) {
	var b [1]byte
	var sb strings.Builder
	for {
		n, err := profileStdin.Read(b[:])
		if n > 0 {
			if b[0] == '\n' {
				return sb.String(), nil
			}
			sb.WriteByte(b[0])
		}
		if err != nil {
			return sb.String(), err
		}
	}
}

// isProfileYes parses a [y/N] confirmation answer.
func isProfileYes(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	}
	return false
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// dedupSorted trims, dedups, and sorts an argument list for reporting.
func dedupSorted(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, s := range items {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func init() {
	rootCmd.AddCommand(profileCmd)
	profileCmd.AddCommand(profileCreateCmd, profileAddCmd, profileRemoveCmd,
		profileLsCmd, profileRmCmd, profileUseCmd, profileRunCmd)

	profileCreateCmd.Flags().StringVarP(&profileCreateClient, "client", "c", "",
		"map the profile to these clients (comma-separated, e.g. cursor,claude-desktop)")
	profileCreateCmd.Flags().StringVar(&profileCreateInherit, "inherit", "",
		"inherit an existing profile's servers in addition to base")
	profileCreateCmd.Flags().BoolVar(&profileJSON, "json", false, "machine-parsable JSON on stdout")

	profileAddCmd.Flags().BoolVar(&profileJSON, "json", false, "machine-parsable JSON on stdout")
	profileRemoveCmd.Flags().BoolVar(&profileJSON, "json", false, "machine-parsable JSON on stdout")
	profileLsCmd.Flags().BoolVar(&profileJSON, "json", false, "machine-parsable JSON on stdout")
	profileRmCmd.Flags().BoolVar(&profileJSON, "json", false, "machine-parsable JSON on stdout")
	profileRmCmd.Flags().BoolVar(&profileRmYes, "yes", false, "delete without confirmation prompt")
	profileUseCmd.Flags().BoolVar(&profileJSON, "json", false, "machine-parsable plan JSON on stdout (never prompts)")
	profileUseCmd.Flags().BoolVar(&profileUseYes, "yes", false, "apply the plan without confirmation prompt")
	profileUseCmd.Flags().BoolVar(&profileUseDryRun, "dry-run", false, "print the plan only — write nothing")
	profileUseCmd.Flags().BoolVar(&profileUseStrict, "strict", false, "refuse to apply while unprofiled servers would be removed")
	profileRunCmd.Flags().BoolVar(&profileJSON, "json", false, "machine-parsable JSON on stdout")
}
