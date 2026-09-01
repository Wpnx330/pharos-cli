package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var (
	unpublishVersion string
	unpublishAll     bool
	unpublishYes     bool
)

var unpublishCmd = &cobra.Command{
	Use:   "unpublish <name>",
	Short: "Hide a package version from search and direct lookup",
	Long: `Hide a package version from the registry.

Unpublished versions are invisible to search and direct lookup (pharos info),
but remain visible on the owner's profile page. The package is NOT deleted —
use 'pharos publish' to re-activate, or 'pharos purge' to permanently remove.

Requires --version <v> or --all.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		if !unpublishAll && unpublishVersion == "" {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Error:"), "must specify --version <v> or --all")
			return nil
		}

		_, client := loadConfig()

		versions := []string{unpublishVersion}
		if unpublishAll {
			// Fetch all versions for the package
			pkg, err := client.GetPackage(name)
			if err != nil {
				fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to fetch package:"), err)
				return nil
			}
			versions = pkg.VersionStrings()
			if len(versions) == 0 {
				fmt.Println(ui.Muted.Render("No versions found."))
				return nil
			}
		}

		// Confirm — hiding versions is reversible but still user-visible,
		// so under PHAROS_NON_INTERACTIVE without an explicit yes we abort
		// with guidance instead of acting unasked.
		if !unpublishYes && !AssumeYes() {
			if NonInteractive() {
				return RequireNonInteractive("unpublish", "--yes or PHAROS_ASSUME_YES=1")
			}
			fmt.Printf("%s This will hide %s versions: %s\n",
				ui.Label.Render("⚠"),
				ui.PackageName.Render(name),
				strings.Join(versions, ", "))
			fmt.Printf("%s Type 'yes' to confirm: ", ui.Label.Render("?"))
			var confirm string
			fmt.Scanln(&confirm)
			if strings.ToLower(strings.TrimSpace(confirm)) != "yes" {
				fmt.Println(ui.Muted.Render("Cancelled."))
				return nil
			}
		}

		// Unpublish each version
		failed := 0
		for _, v := range versions {
			if err := client.SetVersionStatus(name, v, "unpublished"); err != nil {
				fmt.Fprintf(os.Stderr, "%s Failed to unpublish %s@%s: %v\n", ui.Error.Render("✗"), name, v, err)
				failed++
				continue
			}
			fmt.Printf("%s Unpublished %s@%s\n", ui.Success.Render("✓"), ui.PackageName.Render(name), v)
		}

		if failed > 0 {
			fmt.Fprintf(os.Stderr, "\n%s %d version(s) failed.\n", ui.Error.Render("⚠"), failed)
			os.Exit(1)
		}

		fmt.Printf("\n%s Package hidden from search. Use 'pharos republish' to re-activate.\n",
			ui.Muted.Render("ℹ"))
		return nil
	},
}

func init() {
	unpublishCmd.Flags().StringVar(&unpublishVersion, "version", "", "version to unpublish (e.g. 0.2.0)")
	unpublishCmd.Flags().BoolVar(&unpublishAll, "all", false, "unpublish ALL versions")
	unpublishCmd.Flags().BoolVar(&unpublishYes, "yes", false, "skip confirmation prompt")
	rootCmd.AddCommand(unpublishCmd)
}
