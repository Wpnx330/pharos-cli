package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the PHAROS CLI version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(versionLine())
	},
}

// versionLine is the single rendering of the version, shared by the `version`
// subcommand and the root `--version` flag so the two can never drift.
func versionLine() string {
	return ui.Label.Render(fmt.Sprintf("pharos version %s", Version))
}

func init() {
	// Setting Version is what makes cobra register the root --version flag
	// (and its -v shorthand). The subcommand stays; both print the same line.
	// -v on `pharos install` is unaffected: the version flag is registered on
	// the root command only, not persistently.
	rootCmd.Version = Version
	rootCmd.SetVersionTemplate(ui.Label.Render("pharos version {{.Version}}") + "\n")

	rootCmd.AddCommand(versionCmd)
}
