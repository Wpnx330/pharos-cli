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
		out := fmt.Sprintf("pharos version %s", Version)
		fmt.Println(ui.Label.Render(out))
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
