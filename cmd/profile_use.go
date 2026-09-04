// W2.2 — `pharos profile use` (SPEC A1): reconcile a profile's mapped
// clients to contain exactly the profile's target set (base + inherited
// + own servers).
//
// SAFE-BY-DEFAULT SEMANTICS (the hard part, per the work order):
//  1. Target set = union(base ∪ inherit-chain ∪ own servers), deduped
//     base-first — every profile implicitly inherits base.
//  2. Servers present in a mapped client but NOT in the target set are
//     CANDIDATES FOR REMOVAL. Removal is opt-in per run: the default
//     prints the plan and prompts "Apply? [y/N]"; --yes applies;
//     --dry-run prints the plan and writes nothing; --json prints the
//     plan with applied:true/false and never prompts.
//  3. Target-set servers missing from a mapped client are ADDED from
//     the canonical config (~/.pharos/mcp.json) — the same shapes
//     `pharos install` writes (install.BuildClientConfig).
//  4. Non-mapped clients are NEVER touched.
//  5. Every apply writes through clientconfig (SafeWriteConfig
//     underneath) and updates pharos.lock Clients[] records: added to
//     client X → append X; removed from X → drop X.
//  6. Removal candidates not attached to ANY profile are labeled
//     "unprofiled" and hint `pharos profile add base <server...>`.
//     --strict refuses to apply while unprofiled servers would be
//     removed (exit 1) so agents can classify them deliberately first.
//
// Exit codes: 0 = applied, already-in-sync, or dry-run; 1 = plan had
// changes but nothing was applied (declined / needs --yes / strict
// blocked); 2 = usage/validation errors.
package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/canonical"
	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/install"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/profiles"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

// profile use flags.
var (
	profileUseYes    bool
	profileUseDryRun bool
	profileUseStrict bool
)

// profileUseOptions carries explicit overrides for runProfileUse (tests
// drive the logic directly); zero values defer to the cobra flag vars
// and the W1.1 env contract.
type profileUseOptions struct {
	Yes    bool
	DryRun bool
	Strict bool
}

// profileServerAction is one server-level row in a client plan.
type profileServerAction struct {
	Server string `json:"server"`
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`
}

// profileClientPlan is the plan for one mapped client instance.
type profileClientPlan struct {
	Client    string                `json:"client"`
	Path      string                `json:"path"`
	Add       []profileServerAction `json:"add"`
	Remove    []profileServerAction `json:"remove"`
	Skipped   []profileServerAction `json:"skipped"`
	Unchanged int                   `json:"unchanged"`
}

// profileUsePlan is the full JSON document `profile use --json` prints.
type profileUsePlan struct {
	Profile string   `json:"profile"`
	Clients []string `json:"clients"`
	// TargetSet is the resolved base+inherited+own server union.
	TargetSet []string `json:"target_set"`
	DryRun    bool     `json:"dry_run"`
	Strict    bool     `json:"strict,omitempty"`
	// Applied is true when the plan was applied (or nothing needed
	// doing); false means nothing was written.
	Applied bool `json:"applied"`
	// Changes counts planned add/remove rows before a dry-run/decline,
	// and applied+failed rows after a real apply — rows that failed are
	// never zeroed out, so a failed apply cannot read as "in sync".
	Changes int `json:"changes"`
	// Failed counts rows whose add/remove errored during the apply;
	// their Error stays on the row in clients_plan.
	Failed int `json:"failed"`
	// Skipped counts target-set servers held back (not in canonical
	// config, or merge-skipped) across all mapped clients.
	Skipped         int                 `json:"skipped"`
	Blocked         string              `json:"blocked,omitempty"` // strict-unprofiled | needs-yes | declined
	Hint            string              `json:"hint,omitempty"`
	LockfileUpdated bool                `json:"lockfile_updated"`
	ClientsPlan     []profileClientPlan `json:"clients_plan"`

	// resolved holds the client instances behind ClientsPlan. It is
	// unexported, so it never appears in the JSON document.
	resolved []clientconfig.Client
}

var profileUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Reconcile a profile's mapped clients to exactly its servers",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		plan, code, err := runProfileUse(args[0], profileUseOptions{})
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot plan profile use:"), err)
			os.Exit(2)
		}
		if JSONRequested() {
			if err := emitProfileJSON(plan); err != nil {
				return err
			}
		} else {
			emitProfileUseHuman(plan)
		}
		if code != 0 {
			os.Exit(code)
		}
		return nil
	},
}

// runProfileUse is the testable core of `pharos profile use` (the adopt
// pattern: logic returns the exit code; the cobra wrapper exits). It
// reads the W1.1 contract env vars itself, so JSON / ASSUME_YES /
// NON_INTERACTIVE behave identically in-process and via the CLI.
func runProfileUse(name string, opts profileUseOptions) (*profileUsePlan, int, error) {
	st, err := profiles.Load()
	if err != nil {
		return nil, 2, err
	}
	if !st.HasProfile(name) {
		return nil, 2, fmt.Errorf("profile %q does not exist", name)
	}

	plan, err := computeProfileUsePlan(st, name)
	if err != nil {
		return nil, 2, err
	}
	plan.DryRun = opts.DryRun || profileUseDryRun
	plan.Strict = opts.Strict || profileUseStrict

	// Decide what this run does with the plan.
	switch {
	case plan.Changes == 0:
		plan.Applied = true // already in sync — success, no prompt
	case !plan.DryRun && len(plan.unprofiled()) > 0 && plan.Strict:
		// --strict gates real applies only: a dry-run is always a pure
		// preview (exit 0) — the plan rows still show the unprofiled
		// removals and the base hint, but nothing is blocked.
		plan.Blocked = "strict-unprofiled"
	case plan.DryRun:
		// Plan only; applied stays false, exit 0.
	case opts.Yes || profileUseYes || AssumeYes():
		// --yes / PHAROS_ASSUME_YES=1 applies — in JSON mode too (JSON
		// suppresses prompts, never applies).
		applyProfileUsePlan(plan)
	case JSONRequested():
		// Never prompt in JSON mode: return the plan, applied:false.
		plan.Blocked = "needs-yes"
	case NonInteractive():
		plan.Blocked = "needs-yes"
	default:
		printProfileUsePlanHuman(plan)
		fmt.Print("Apply? [y/N] ")
		answer, _ := readProfileLine()
		if !isProfileYes(answer) {
			plan.Blocked = "declined"
		} else {
			applyProfileUsePlan(plan)
		}
	}

	// Exit 1 when changes were pending but nothing was applied: declined,
	// needs-yes, strict-blocked — or every add/remove failed (Changes
	// keeps failed rows, so an all-failed apply cannot exit 0).
	code := 0
	if !plan.Applied && plan.Changes > 0 && !plan.DryRun {
		code = 1
	}
	return plan, code, nil
}

// emitProfileUseHuman renders the human outcome for the cobra wrapper.
// The exit code itself is decided by runProfileUse.
func emitProfileUseHuman(plan *profileUsePlan) {
	switch {
	case plan.Applied && plan.Changes > 0:
		if plan.Failed > 0 {
			fmt.Printf("%s  Applied %d of %d change(s) across %d mapped client(s).\n",
				ui.Success.Render("✓"), plan.Changes-plan.Failed, plan.Changes, len(plan.ClientsPlan))
		} else {
			fmt.Printf("%s  Applied %d change(s) across %d mapped client(s).\n",
				ui.Success.Render("✓"), plan.Changes, len(plan.ClientsPlan))
		}
		printProfileUseFailures(plan)
		printSkippedNote(plan)
		if plan.LockfileUpdated {
			fmt.Printf("%s  pharos.lock client records updated.\n", ui.Muted.Render("·"))
		} else {
			fmt.Printf("%s  pharos.lock needed no client-record updates.\n", ui.Muted.Render("·"))
		}
	case plan.Applied: // in sync
		printProfileUsePlanHuman(plan)
	case plan.Blocked == "declined":
		fmt.Println(ui.Muted.Render("Nothing done — plan not applied."))
	default:
		printProfileUsePlanHuman(plan)
		// An apply that ran but failed every row lands here (applied
		// false, no block): the FAILED block keeps that honest — it must
		// never read as a silent success.
		printProfileUseFailures(plan)
		if plan.Blocked == "needs-yes" {
			fmt.Fprintf(os.Stderr, "%s\n", RequireNonInteractive("profile use", "--yes or PHAROS_ASSUME_YES=1"))
		}
		if plan.Blocked == "strict-unprofiled" {
			fmt.Fprintf(os.Stderr, "%s  --strict: unprofiled server(s) would be removed; classify them first:\n    %s\n",
				ui.Error.Render("Blocked:"), plan.Hint)
		}
	}
}

// printProfileUseFailures lists the rows that failed to apply with the
// server name and the underlying error — the honesty block that keeps a
// failed apply from masquerading as "already in sync".
func printProfileUseFailures(plan *profileUsePlan) {
	if plan.Failed == 0 {
		return
	}
	fmt.Printf("%s  %d change(s) FAILED:\n", ui.Error.Render("✗"), plan.Failed)
	for _, cp := range plan.ClientsPlan {
		for _, a := range cp.Add {
			if a.Error != "" {
				fmt.Printf("    %s  add %s (%s) — %s\n", ui.Error.Render("!"), a.Server, cp.Client, a.Error)
			}
		}
		for _, r := range cp.Remove {
			if r.Error != "" {
				fmt.Printf("    %s  remove %s (%s) — %s\n", ui.Error.Render("!"), r.Server, cp.Client, r.Error)
			}
		}
	}
}

// printSkippedNote flags target-set servers held back because they have
// no canonical record — the "no silent perfection" note that keeps an
// exit-0 run from reading as fully healthy when rows were skipped.
func printSkippedNote(plan *profileUsePlan) {
	if plan.Skipped == 0 {
		return
	}
	fmt.Printf("%s  %d target server(s) missing from canonical — reinstall ('pharos install <server>') or remove them from the profile.\n",
		ui.Muted.Render("Note:"), plan.Skipped)
}

// computeProfileUsePlan builds the reconciliation plan without touching
// anything. resolved carries the client instances the plan maps to.
func computeProfileUsePlan(st *profiles.State, name string) (*profileUsePlan, error) {
	p := st.Profiles[name]
	if len(p.Clients) == 0 {
		return nil, fmt.Errorf("profile %q has no mapped clients; recreate it with --client <id>", name)
	}

	target, err := st.TargetSet(name)
	if err != nil {
		return nil, err
	}

	instances, err := install.ResolveWriteTargets(p.Clients)
	if err != nil {
		return nil, err
	}

	canon, err := canonical.Load()
	if err != nil {
		return nil, fmt.Errorf("read canonical config: %w", err)
	}

	plan := &profileUsePlan{
		Profile:     name,
		Clients:     append([]string{}, p.Clients...),
		TargetSet:   target,
		ClientsPlan: []profileClientPlan{},
		resolved:    instances,
	}

	for _, c := range instances {
		cp := profileClientPlan{
			Client:  string(c.ID),
			Path:    c.Path,
			Add:     []profileServerAction{},
			Remove:  []profileServerAction{},
			Skipped: []profileServerAction{},
		}
		present, err := clientconfig.ReadServersFormat(c.Path, c.Format)
		if err != nil {
			return nil, fmt.Errorf("read config for %s: %w", c.Name, err)
		}
		presentNames := sortedServerNames(present)
		presentSet := make(map[string]bool, len(presentNames))
		for _, srv := range presentNames {
			presentSet[srv] = true
		}

		// Adds: target-set servers missing from this client. The entry
		// shape comes from the canonical config — the same record
		// `pharos install` wrote.
		for _, srv := range target {
			if presentSet[srv] {
				cp.Unchanged++
				continue
			}
			if _, ok := canon.Servers[srv]; !ok {
				cp.Skipped = append(cp.Skipped, profileServerAction{
					Server: srv,
					Reason: "not in canonical config — run 'pharos install " + srv + "' first",
				})
				continue
			}
			cp.Add = append(cp.Add, profileServerAction{Server: srv})
		}

		// Removals: servers present but outside the target set, labeled
		// by where they are profiled (or not).
		profiledBy := st.ProfiledBy()
		for _, srv := range presentNames {
			if targetHas(target, srv) {
				continue
			}
			action := profileServerAction{Server: srv}
			if holders, ok := profiledBy[srv]; ok && len(holders) > 0 {
				action.Reason = "profiled: " + strings.Join(holders, ", ")
			} else {
				action.Reason = "unprofiled (will be removed from this client)"
			}
			cp.Remove = append(cp.Remove, action)
		}

		plan.Changes += len(cp.Add) + len(cp.Remove)
		plan.ClientsPlan = append(plan.ClientsPlan, cp)
	}

	if unprof := plan.unprofiled(); len(unprof) > 0 {
		plan.Hint = profileAddBaseHint(unprof)
	}
	for _, cp := range plan.ClientsPlan {
		plan.Skipped += len(cp.Skipped)
	}
	return plan, nil
}

// unprofiled returns the sorted removal candidates attached to no
// profile at all (the migration surface).
func (plan *profileUsePlan) unprofiled() []string {
	var out []string
	seen := make(map[string]bool)
	for _, cp := range plan.ClientsPlan {
		for _, r := range cp.Remove {
			if strings.HasPrefix(r.Reason, "unprofiled") && !seen[r.Server] {
				seen[r.Server] = true
				out = append(out, r.Server)
			}
		}
	}
	sort.Strings(out)
	return out
}

// profileAddBaseHint is the migration hint for unprofiled servers.
func profileAddBaseHint(servers []string) string {
	return "pharos profile add base " + strings.Join(servers, " ") +
		" — keeps them in every context"
}

func targetHas(target []string, srv string) bool {
	for _, t := range target {
		if t == srv {
			return true
		}
	}
	return false
}

// applyProfileUsePlan executes the plan: client config writes via
// clientconfig, then pharos.lock Clients[] reconciliation. Rows that
// fail are recorded in-place (Error field) and do not abort the run.
func applyProfileUsePlan(plan *profileUsePlan) {
	canon, err := canonical.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s  %v\n", ui.Error.Render("Apply aborted — cannot read canonical config:"), err)
		return
	}
	lockPath, err := lockfile.DefaultPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s  %v\n", ui.Error.Render("Apply aborted — cannot locate lockfile:"), err)
		return
	}
	lf, err := lockfile.Load(lockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s  %v\n", ui.Error.Render("Apply aborted — cannot read lockfile:"), err)
		return
	}

	applied := 0
	failed := 0
	for i := range plan.ClientsPlan {
		cp := &plan.ClientsPlan[i]
		c := plan.resolved[i]

		var keptAdds []profileServerAction
		for _, action := range cp.Add {
			srvCanonical, ok := canon.Servers[action.Server]
			if !ok {
				cp.Skipped = append(cp.Skipped, profileServerAction{
					Server: action.Server,
					Reason: "not in canonical config — run 'pharos install " + action.Server + "' first",
				})
				continue
			}
			cfg := canonicalToClientCfg(srvCanonical)
			if err := clientconfig.MergeServer(c, action.Server, cfg); err != nil {
				if clientconfig.IsSkip(err) {
					cp.Skipped = append(cp.Skipped, profileServerAction{Server: action.Server, Reason: err.Error()})
				} else {
					action.Error = err.Error()
					keptAdds = append(keptAdds, action)
					failed++
				}
				continue
			}
			lfRecordClientAdded(lf, action.Server, string(c.ID))
			applied++
		}
		cp.Add = keptAdds

		var keptRemoves []profileServerAction
		for _, action := range cp.Remove {
			if err := clientconfig.RemoveServer(c, action.Server); err != nil {
				action.Error = err.Error()
				keptRemoves = append(keptRemoves, action)
				failed++
				continue
			}
			lfRecordClientRemoved(lf, action.Server, string(c.ID))
			applied++
		}
		cp.Remove = keptRemoves
	}

	if err := lf.Save(lockPath); err != nil {
		fmt.Fprintf(os.Stderr, "%s  %v\n", ui.Error.Render("Warning: lockfile update failed:"), err)
	} else if applied > 0 {
		plan.LockfileUpdated = true
	}

	// Honest accounting: failed rows keep their Error and stay counted
	// (Changes = applied + failed), so an apply that failed everything
	// reports changes>0/applied:false — never a silent "in sync".
	skipped := 0
	for _, cp := range plan.ClientsPlan {
		skipped += len(cp.Skipped)
	}
	plan.Skipped = skipped
	plan.Failed = failed
	plan.Changes = applied + failed
	plan.Applied = applied > 0
}

// lfRecordClientAdded appends clientID to the lockfile entry's Clients
// record (a server now written to one more client).
func lfRecordClientAdded(lf *lockfile.Lockfile, server, clientID string) {
	entry, ok := lf.Get(server)
	if !ok {
		return // not pharos-managed; nothing to reconcile
	}
	for _, id := range entry.Clients {
		if id == clientID {
			return
		}
	}
	entry.Clients = append(entry.Clients, clientID)
	sort.Strings(entry.Clients)
	lf.Set(server, entry)
}

// lfRecordClientRemoved drops clientID from the lockfile entry's Clients
// record (a server is no longer written to that client).
func lfRecordClientRemoved(lf *lockfile.Lockfile, server, clientID string) {
	entry, ok := lf.Get(server)
	if !ok {
		return
	}
	var kept []string
	changed := false
	for _, id := range entry.Clients {
		if id == clientID {
			changed = true
			continue
		}
		kept = append(kept, id)
	}
	if changed {
		entry.Clients = kept
		lf.Set(server, entry)
	}
}

// canonicalToClientCfg re-derives the client entry shape from a
// canonical record — the identical reconstruction doctor --diff uses
// (canonical → registry-manifest shape → install.BuildClientConfig), so
// profile use never writes a shape doctor would flag as drift.
func canonicalToClientCfg(srv canonical.Server) clientconfig.ServerConfig {
	manifest := api.Manifest{
		Name:      srv.Package.Name,
		Version:   srv.Package.Version,
		Transport: srv.Transport,
		Endpoint:  srv.URL,
		Command:   srv.Command,
		Args:      srv.Args,
		Env:       srv.Env,
	}
	return install.BuildClientConfig(manifest, "")
}

// printProfileUsePlanHuman renders the plan in the W1.4 doctor-style
// indented finding format.
func printProfileUsePlanHuman(plan *profileUsePlan) {
	head := fmt.Sprintf("Profile %q → %d server(s) across %d mapped client(s)",
		plan.Profile, len(plan.TargetSet), len(plan.ClientsPlan))
	if plan.DryRun {
		fmt.Printf("%s  (%s)\n", ui.Label.Render(head), ui.Muted.Render("dry run — nothing written"))
	} else {
		fmt.Printf("%s\n", ui.Label.Render(head))
	}
	if len(plan.TargetSet) > 0 {
		fmt.Printf("%s  %s\n", ui.Muted.Render("Target set:"), strings.Join(plan.TargetSet, ", "))
	}
	for _, cp := range plan.ClientsPlan {
		fmt.Printf("  %s (%s)\n", cp.Client, cp.Path)
		for _, a := range cp.Add {
			if a.Error != "" {
				fmt.Printf("    %s  add %s — failed: %s\n", ui.Error.Render("!"), a.Server, a.Error)
				continue
			}
			fmt.Printf("    %s  add %s (from canonical config)\n", ui.Success.Render("+"), a.Server)
		}
		for _, r := range cp.Remove {
			if r.Error != "" {
				fmt.Printf("    %s  remove %s — failed: %s\n", ui.Error.Render("!"), r.Server, r.Error)
				continue
			}
			fmt.Printf("    %s  remove %s — %s\n", ui.Warning.Render("-"), r.Server, r.Reason)
		}
		for _, s := range cp.Skipped {
			fmt.Printf("    %s  skip %s — %s\n", ui.Muted.Render("—"), s.Server, s.Reason)
		}
		if cp.Unchanged > 0 {
			fmt.Printf("    %s  %d already in place\n", ui.Muted.Render("="), cp.Unchanged)
		}
	}
	if plan.Changes == 0 {
		fmt.Printf("%s  Already in sync.\n", ui.Success.Render("✓"))
	}
	printSkippedNote(plan)
	if plan.Hint != "" {
		fmt.Printf("%s  %s\n", ui.Muted.Render("Hint:"), plan.Hint)
	}
}
