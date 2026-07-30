package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var (
	purgeVersion string
	purgeAll     bool
	purgeYes     bool
)

var purgeCmd = &cobra.Command{
	Use:   "purge <name>",
	Short: "Permanently remove a package version from the registry",
	Long: `Permanently remove a package version from the registry.

Purged versions are soft-deleted — they become invisible everywhere: search,
direct lookup, and the owner's profile. This is irreversible.

Use 'pharos unpublish' instead if you just want to hide a version temporarily.

Requires --version <v> or --all.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		if !purgeAll && purgeVersion == "" {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Error:"), "must specify --version <v> or --all")
			return
		}

		_, client := loadConfig()

		versions := []string{purgeVersion}
		if purgeAll {
			pkg, err := client.GetPackage(name)
			if err != nil {
				fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to fetch package:"), err)
				return
			}
			versions = pkg.VersionStrings()
			if len(versions) == 0 {
				fmt.Println(ui.Muted.Render("No versions found."))
				return
			}
		}

		// Confirm — purge is destructive
		if !purgeYes {
			fmt.Printf("%s This will PERMANENTLY DELETE %s versions: %s\n",
				ui.Error.Render("⚠"),
				ui.PackageName.Render(name),
				strings.Join(versions, ", "))
			fmt.Printf("%s This cannot be undone. Type 'purge' to confirm: ", ui.Error.Render("⚠"))
			var confirm string
			fmt.Scanln(&confirm)
			if strings.ToLower(strings.TrimSpace(confirm)) != "purge" {
				fmt.Println(ui.Muted.Render("Cancelled."))
				return
			}
		}

		// Delete each version
		failed := 0
		for _, v := range versions {
			if err := client.SetVersionStatus(name, v, "deleted"); err != nil {
				fmt.Fprintf(os.Stderr, "%s Failed to purge %s@%s: %v\n", ui.Error.Render("✗"), name, v, err)
				failed++
				continue
			}
			fmt.Printf("%s Purged %s@%s\n", ui.Error.Render("✓"), ui.PackageName.Render(name), v)
		}

		if failed > 0 {
			fmt.Fprintf(os.Stderr, "\n%s %d version(s) failed.\n", ui.Error.Render("⚠"), failed)
			os.Exit(1)
		}

		fmt.Printf("\n%s Permanently removed. This action cannot be undone.\n",
			ui.Muted.Render("ℹ"))
	},
}

func init() {
	purgeCmd.Flags().StringVar(&purgeVersion, "version", "", "version to purge (e.g. 0.2.0)")
	purgeCmd.Flags().BoolVar(&purgeAll, "all", false, "purge ALL versions")
	purgeCmd.Flags().BoolVar(&purgeYes, "yes", false, "skip confirmation prompt")
	rootCmd.AddCommand(purgeCmd)
}
