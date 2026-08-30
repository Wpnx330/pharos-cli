package cmd

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/install"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var updateDryRun bool

var updateCmd = &cobra.Command{
	Use:   "update [name]",
	Short: "Check for and apply updates to installed MCP servers",
	Long: ui.Label.Render("pharos update") + ` checks all servers in pharos.lock for newer versions.
With a name argument, updates only that server.

Use --dry-run to see what would change without modifying anything.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		_, client := loadConfig()

		lockPath, err := lockfile.DefaultPath()
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot determine lockfile path:"), err)
			return
		}

		lf, err := lockfile.Load(lockPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to load lockfile:"), err)
			return
		}

		if len(lf.Servers) == 0 {
			fmt.Fprintln(os.Stderr, ui.Error.Render("No servers in lockfile."), ui.Muted.Render("Run `pharos import` or `pharos install` first."))
			return
		}

		target := ""
		if len(args) == 1 {
			target = args[0]
			if !lf.Has(target) {
				fmt.Fprintln(os.Stderr, ui.Error.Render("Server not in lockfile:"), target)
				return
			}
		}

		var updatesAvailable, upToDate, notFound, updated int

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
				fmt.Printf("  %s  %s — %s\n", ui.Muted.Render("?"), name, ui.Muted.Render("not found in registry"))
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
				fmt.Printf("  %s  %s@%s %s\n", ui.Success.Render("✓"), name, entry.Version, ui.Muted.Render("(up to date)"))
				continue
			}

			updatesAvailable++
			if updateDryRun {
				fmt.Printf("  %s  %s: %s → %s\n", ui.Label.Render("→"), name, entry.Version, latest)
				continue
			}

			// Perform the update: land the new artifact (K3/K2), rewrite every
			// affected client config (issue #20), then bump the lockfile.
			fmt.Printf("  %s  %s: %s → %s\n", ui.Label.Render("Updating"), name, entry.Version, latest)

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
				fmt.Printf("  %s  %s@%s — %s\n", ui.Error.Render("✗"), name, latest, ui.Muted.Render("manifest unavailable, skipping"))
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
						continue
					}
				}
			}

			// Rewrite affected client configs with the NEW server config
			// (same write path as install: clientconfig.MergeServer).
			clientCfg := install.BuildClientConfig(vd.Manifest, storeDir)
			upd, uerrs := rewriteClientsForUpdate(name, clientCfg, clientconfig.Detect())
			printUpdateConfigResults(upd, uerrs)

			lf.Set(name, lockfile.ServerEntry{
				Version:     latest,
				Integrity:   integrity,
				Transport:   transport,
				Resolved:    entry.Resolved,
				InstalledAt: entry.InstalledAt,
			})
			updated++
		}

		if updateDryRun {
			fmt.Printf("\n%s  %d update(s) available (dry run)\n", ui.Label.Render("Summary:"), updatesAvailable)
			return
		}

		if updated > 0 {
			if err := lf.Save(lockPath); err != nil {
				fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to save lockfile:"), err)
				return
			}
		}

		fmt.Printf("\n%s  %d updated, %d up to date, %d not found\n",
			ui.Success.Render("✓ Done."),
			updated,
			upToDate,
			notFound)
	},
}

func init() {
	updateCmd.Flags().BoolVar(&updateDryRun, "dry-run", false, "show what would change without applying updates")
	rootCmd.AddCommand(updateCmd)
}
