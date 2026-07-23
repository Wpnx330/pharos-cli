package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var configCmd = &cobra.Command{
	Use:   "config <key> [value]",
	Short: "Get or set configuration values",
	Args:  cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		cfg, _ := loadConfig()
		key := args[0]

		if len(args) == 1 {
			// Get
			val, err := cfg.Get(key)
			if err != nil {
				fmt.Fprintln(os.Stderr, ui.Error.Render(err.Error()))
				return
			}
			if val == "" {
				val = "(not set)"
			}
			fmt.Printf("%s  %s\n", ui.Label.Render(key+":"), val)
			return
		}

		// Set
		value := args[1]
		if err := cfg.Set(key, value); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render(err.Error()))
			return
		}
		if err := cfg.Save(); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to save config:"), err)
			return
		}
		fmt.Printf("%s  %s = %s\n", ui.Success.Render("✓"), key, value)
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
}
