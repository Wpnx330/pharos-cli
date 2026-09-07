package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/install"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var updateDryRun bool
var updateCheck bool
var updateJSON bool

// updateEntry is one server row in the update JSON report.
type updateEntry struct {
	Name   string `json:"name"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Action string `json:"action"` // "updated" | "up_to_date" | "update_available" | "pinned" | "not_found" | "failed"
	// W5.1 origin extras. Origin is the lockfile provenance, omitted
	// when the entry has none (legacy entries — never null). Pinned
	// marks servers whose lockfile entry carries PinnedAt. Repo and
	// Changelog are --check-only derivations from ALREADY-fetched
	// registry data (no extra fetches), omitted when not derivable.
	Origin    *lockfile.OriginInfo `json:"origin,omitempty"`
	Pinned    bool                 `json:"pinned,omitempty"`
	Repo      string               `json:"repo,omitempty"`
	Changelog string               `json:"changelog,omitempty"`
}

// updateReport is the JSON shape of `update --json`.
type updateReport struct {
	DryRun           bool          `json:"dry_run"`
	Updated          int           `json:"updated"`
	UpToDate         int           `json:"up_to_date"`
	NotFound         int           `json:"not_found"`
	UpdatesAvailable int           `json:"updates_available"`
	Pinned           int           `json:"pinned"`
	Servers          []updateEntry `json:"servers"`
}

var updateCmd = &cobra.Command{
	Use:   "update [name]",
	Short: "Check for and apply updates to installed MCP servers",
	Long: ui.Label.Render("pharos update") + ` checks all servers in pharos.lock for newer versions.
With a name argument, updates only that server.

Use --dry-run to see what would change without modifying anything.
Use --check for the same no-apply preview plus origin-aware extras:
when a server's origin or registry metadata carries a repository URL on
a known git host (github/gitlab), the repo and a changelog link are
shown. --dry-run and --check may be combined; they select the same mode.

Use --all (or no arguments) to update every server in the lockfile.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, client := loadConfig()

		lockPath, err := lockfile.DefaultPath()
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot determine lockfile path:"), err)
			return nil
		}

		lf, err := lockfile.Load(lockPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to load lockfile:"), err)
			return nil
		}

		// W1.2 receipt: the lockfile's pre-run hash is captured now; the row
		// is only added if an update is actually applied and saved.
		rcpt := newReceiptBuilder("update", "", "")
		rcpt.noteLock(lockPath)
		var updatedNames []string
		var singleLatest string
		finalizeReceipt := func() {
			rcpt.setPackage(strings.Join(updatedNames, ","))
			if len(updatedNames) == 1 {
				rcpt.setVersion(singleLatest)
			}
		}

		if len(lf.Servers) == 0 {
			fmt.Fprintln(os.Stderr, ui.Error.Render("No servers in lockfile."), ui.Muted.Render("Run `pharos import` or `pharos install` first."))
			return nil
		}

		target := ""
		if len(args) == 1 {
			target = args[0]
			if !lf.Has(target) {
				fmt.Fprintln(os.Stderr, ui.Error.Render("Server not in lockfile:"), target)
				return nil
			}
		}

		// W5.1: --check is an alias of --dry-run with origin-aware
		// extras (repo/changelog links). Both flags may be passed
		// together — they select the same no-apply mode.
		checkMode := updateDryRun || updateCheck

		var updatesAvailable, upToDate, notFound, updated int
		report := &updateReport{DryRun: checkMode, Servers: []updateEntry{}}

		// Sort server names for deterministic output
		names := make([]string, 0, len(lf.Servers))
		for n := range lf.Servers {
			names = append(names, n)
		}
		sort.Strings(names)

		for _, name := range names {
			if target != "" && name != target {
				continue
			}

			entry, _ := lf.Get(name)
			pkg, err := client.GetPackage(name)
			if err != nil {
				notFound++
				report.NotFound++
				report.Servers = append(report.Servers, updateEntry{Name: name, From: entry.Version, Action: "not_found", Origin: entry.Origin})
				if !JSONRequested() {
					fmt.Printf("  %s  %s — %s\n", ui.Muted.Render("?"), name, ui.Muted.Render("not found in registry"))
				}
				continue
			}

			// W5.1 --check extras: repo/changelog links derived from
			// data already fetched (origin Ref, packument repo_url) —
			// never an extra network call, never fabricated for
			// unknown hosts.
			repo, changelog := "", ""
			if updateCheck {
				repo, changelog = deriveRepoLinks(entry, pkg)
			}

			latest := ""
			if pkg.DistTags != nil {
				latest = pkg.DistTags["latest"]
			}
			if latest == "" {
				latest = entry.Version
			}

			if latest == entry.Version {
				upToDate++
				report.UpToDate++
				report.Servers = append(report.Servers, updateEntry{
					Name: name, From: entry.Version, To: latest, Action: "up_to_date",
					Origin: entry.Origin, Repo: repo, Changelog: changelog,
				})
				if !JSONRequested() {
					fmt.Printf("  %s  %s@%s %s\n", ui.Success.Render("✓"), name, entry.Version, ui.Muted.Render("(up to date)"))
					printCheckOriginExtras(repo, changelog)
				}
				continue
			}

			updatesAvailable++
			if checkMode {
				report.UpdatesAvailable++
				report.Servers = append(report.Servers, updateEntry{
					Name: name, From: entry.Version, To: latest, Action: "update_available",
					Origin: entry.Origin, Repo: repo, Changelog: changelog,
				})
				if !JSONRequested() {
					fmt.Printf("  %s  %s: %s → %s\n", ui.Label.Render("→"), name, entry.Version, latest)
					printCheckOriginExtras(repo, changelog)
				}
				continue
			}

			// Perform the update: land the new artifact (K3/K2), rewrite every
			// affected client config (issue #20), then bump the lockfile.
			if !JSONRequested() {
				fmt.Printf("  %s  %s: %s → %s\n", ui.Label.Render("Updating"), name, entry.Version, latest)
			}

			vd := pkg.FindVersion(latest)
			integrity := ""
			transport := entry.Transport
			if vd != nil {
				integrity = vd.Manifest.Integrity
				transport = vd.Manifest.Transport
			}

			storeDir, serr := install.DefaultStoreDir()
			if serr != nil {
				fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot determine store directory:"), serr)
				continue
			}
			if vd == nil {
				notFound++
				report.NotFound++
				report.Servers = append(report.Servers, updateEntry{Name: name, From: entry.Version, To: latest, Action: "not_found"})
				if !JSONRequested() {
					fmt.Printf("  %s  %s@%s — %s\n", ui.Error.Render("✗"), name, latest, ui.Muted.Render("manifest unavailable, skipping"))
				}
				continue
			}

			if kind := install.ClassifyManifest(vd.Manifest); kind != install.KindRemoteHTTP {
				// Kind 2/3: land the new artifact via install's own pipeline so the
				// rewritten configs point at a real binary.
				mgr := install.NewManager(storeDir)
				if !mgr.IsInstalled(name, latest) {
					if _, ierr := mgr.InstallByKind(install.InstallOptions{
						Name:              name,
						Version:           latest,
						TarballURL:        client.TarballURL(name, latest),
						ExpectedIntegrity: vd.Manifest.Integrity,
						Manifest:          vd.Manifest,
					}); ierr != nil {
						fmt.Fprintln(os.Stderr, ui.Error.Render("Update failed:"), ierr)
						notFound++
						report.NotFound++
						report.Servers = append(report.Servers, updateEntry{Name: name, From: entry.Version, To: latest, Action: "failed"})
						continue
					}
				}
			}

			// Rewrite affected client configs with the NEW server config
			// (same write path as install: clientconfig.MergeServer). The
			// builder captures each rewritten file + a "replaced" server row.
			clientCfg := install.BuildClientConfig(vd.Manifest, storeDir)
			upd, uerrs := rewriteClientsForUpdate(name, clientCfg, clientconfig.Detect(), rcpt)
			if !JSONRequested() {
				printUpdateConfigResults(upd, uerrs)
			}

			// Rewrite the lockfile entry preserving every additive field:
			// Clients (previously wiped here — drift detection keys MISSING
			// findings off this record), Origin (W5.1), PinnedAt (W5.1).
			// For registry-origin entries the Ref's version part is bumped
			// to the newly installed version; adopted/legacy origins pass
			// through untouched (legacy nil Origin stays absent — the
			// backfill rule is behavior-only, never fabricated metadata).
			lf.Set(name, lockfile.ServerEntry{
				Version:     latest,
				Integrity:   integrity,
				Transport:   transport,
				Resolved:    entry.Resolved,
				InstalledAt: entry.InstalledAt,
				Clients:     entry.Clients,
				Origin:      originAfterUpdate(entry, name, latest),
				PinnedAt:    entry.PinnedAt,
			})
			updated++
			updatedNames = append(updatedNames, name)
			singleLatest = latest
			report.Updated++
			report.Servers = append(report.Servers, updateEntry{Name: name, From: entry.Version, To: latest, Action: "updated"})
		}

		if checkMode {
			if JSONRequested() {
				return printUpdateJSON(report)
			}
			mode := "dry run"
			if updateCheck {
				mode = "check"
			}
			fmt.Printf("\n%s  %d update(s) available (%s)\n", ui.Label.Render("Summary:"), updatesAvailable, mode)
			return nil
		}

		if updated > 0 {
			if err := lf.Save(lockPath); err != nil {
				fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to save lockfile:"), err)
				// W1.2: the configs were rewritten and .baks taken — the
				// built receipt must still be emitted (status "partial",
				// lockfile row simply absent, error recorded) instead of
				// being dropped with a bare return.
				rcpt.addError("lockfile save failed: %v", err)
				finalizeReceipt()
				rcpt.emit()
				return nil
			}
			rcpt.touchLock()
		}

		if JSONRequested() {
			if updated > 0 {
				// W1.2: when a receipt exists the receipt JSON is the only
				// stdout document; the update report stays for the
				// nothing-updated paths below.
				finalizeReceipt()
				rcpt.emit()
				return nil
			}
			return printUpdateJSON(report)
		}

		fmt.Printf("\n%s  %d updated, %d up to date, %d not found\n",
			ui.Success.Render("✓ Done."),
			updated,
			upToDate,
			notFound)
		if updated > 0 {
			finalizeReceipt()
			rcpt.emit()
		}
		return nil
	},
}

// printUpdateJSON emits the update report as JSON to stdout.
func printUpdateJSON(report *updateReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// originAfterUpdate computes the Origin an updated lockfile entry
// carries. Legacy entries (nil Origin) stay absent — treating them as
// registry installs is update BEHAVIOR, never written back as fabricated
// metadata. Registry origins are refreshed so Ref's version part tracks
// the newly installed version; any other kind (adopted) passes through
// unchanged. The input pointer is never mutated.
func originAfterUpdate(entry lockfile.ServerEntry, name, latest string) *lockfile.OriginInfo {
	if entry.Origin == nil {
		return nil
	}
	if entry.Origin.Kind != lockfile.OriginKindRegistry {
		return entry.Origin
	}
	updated := *entry.Origin
	updated.Ref = name + "@" + latest
	return &updated
}

func init() {
	updateCmd.Flags().BoolVar(&updateDryRun, "dry-run", false, "show what would change without applying updates")
	updateCmd.Flags().BoolVar(&updateCheck, "check", false, "like --dry-run, plus repo/changelog links for git-hosted origins")
	updateCmd.Flags().BoolVar(&updateJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(updateCmd)
}
