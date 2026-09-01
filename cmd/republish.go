package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var republishVersion string
var republishJSON bool

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
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		if republishVersion == "" {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Error:"), "must specify --version <v>")
			return nil
		}

		_, client := loadConfig()

		if err := client.SetVersionStatus(name, republishVersion, "active"); err != nil {
			// For deleted/purged versions, the registry returns 410 Gone.
			// We show "not found" — purge is irreversible, and the version
			// should appear as if it never existed from the user's perspective.
			fmt.Fprintln(os.Stderr, ui.Error.Render("Error:"), fmt.Sprintf("version %s@%s not found", name, republishVersion))
			return nil
		}

		if JSONRequested() {
			data, err := json.MarshalIndent(map[string]string{
				"name":    name,
				"version": republishVersion,
				"status":  "active",
			}, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("%s Republished %s@%s — now visible in search and direct lookup.\n",
			ui.Success.Render("✓"), ui.PackageName.Render(name), republishVersion)
		return nil
	},
}

func init() {
	republishCmd.Flags().StringVar(&republishVersion, "version", "", "version to republish (e.g. 0.2.0)")
	republishCmd.Flags().BoolVar(&republishJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(republishCmd)
}
