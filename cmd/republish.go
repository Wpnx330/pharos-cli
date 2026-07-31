package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var republishVersion string

var republishCmd = &cobra.Command{
	Use:   "republish <name>",
	Short: "Re-activate a previously unpublished package version",
	Long: `Re-activate a previously unpublished package version.

Sets the version status back to 'active', making it visible in search and
direct lookup again. No re-upload needed — the original tarball is preserved.

Requires --version <v>.

Example:
  pharos republish test-echo-server --version 0.2.0`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		if republishVersion == "" {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Error:"), "must specify --version <v>")
			return
		}

		_, client := loadConfig()

		if err := client.SetVersionStatus(name, republishVersion, "active"); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to republish:"), err)
			return
		}

		fmt.Printf("%s Republished %s@%s — now visible in search and direct lookup.\n",
			ui.Success.Render("✓"), ui.PackageName.Render(name), republishVersion)
	},
}

func init() {
	republishCmd.Flags().StringVar(&republishVersion, "version", "", "version to republish (e.g. 0.2.0)")
	rootCmd.AddCommand(republishCmd)
}
