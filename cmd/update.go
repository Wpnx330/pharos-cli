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
var updateJSON bool

// updateEntry is one server row in the update JSON report.
type updateEntry struct {
	Name   string `json:"name"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Action string `json:"action"` // "updated" | "up_to_date" | "update_available" | "not_found" | "failed"
}

// updateReport is the JSON shape of `update --json`.
type updateReport struct {
	DryRun           bool          `json:"dry_run"`
	Updated          int           `json:"updated"`
	UpToDate         int           `json:"up_to_date"`
	NotFound         int           `json:"not_found"`
	UpdatesAvailable int           `json:"updates_available"`
	Servers          []updateEntry `json:"servers"`
}

var updateCmd = &cobra.Command{
	Use:   "update [name]",
	Short: "Check for and apply updates to installed MCP servers",
	Long: ui.Label.Render("pharos update") + ` checks all servers in pharos.lock for newer versions.
With a name argument, updates only that server.

Use --dry-run to see what would change without modifying anything.`,
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

		var updatesAvailable, upToDate, notFound, updated int
		report := &updateReport{DryRun: updateDryRun, Servers: []updateEntry{}}

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
				report.Servers = append(report.Servers, updateEntry{Name: name, From: entry.Version, Action: "not_found"})
				if !JSONRequested() {
					fmt.Printf("  %s  %s — %s\n", ui.Muted.Render("?"), name, ui.Muted.Render("not found in registry"))
				}
				continue
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
				report.Servers = append(report.Servers, updateEntry{Name: name, From: entry.Version, To: latest, Action: "up_to_date"})
				if !JSONRequested() {
					fmt.Printf("  %s  %s@%s %s\n", ui.Success.Render("✓"), name, entry.Version, ui.Muted.Render("(up to date)"))
				}
				continue
			}

			updatesAvailable++
			if updateDryRun {
				report.UpdatesAvailable++
				report.Servers = append(report.Servers, updateEntry{Name: name, From: entry.Version, To: latest, Action: "update_available"})
				if !JSONRequested() {
					fmt.Printf("  %s  %s: %s → %s\n", ui.Label.Render("→"), name, entry.Version, latest)
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

			lf.Set(name, lockfile.ServerEntry{
				Version:     latest,
				Integrity:   integrity,
				Transport:   transport,
				Resolved:    entry.Resolved,
				InstalledAt: entry.InstalledAt,
			})
			updated++
			updatedNames = append(updatedNames, name)
			singleLatest = latest
			report.Updated++
			report.Servers = append(report.Servers, updateEntry{Name: name, From: entry.Version, To: latest, Action: "updated"})
		}

		if updateDryRun {
			if JSONRequested() {
				return printUpdateJSON(report)
			}
			fmt.Printf("\n%s  %d update(s) available (dry run)\n", ui.Label.Render("Summary:"), updatesAvailable)
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

func init() {
	updateCmd.Flags().BoolVar(&updateDryRun, "dry-run", false, "show what would change without applying updates")
	updateCmd.Flags().BoolVar(&updateJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(updateCmd)
}
