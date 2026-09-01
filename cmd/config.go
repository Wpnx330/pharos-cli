package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/ui"
)

// configJSON holds the --json flag for `pharos`.
var configJSON bool

var configCmd = &cobra.Command{
	Use:   "config <key> [value]",
	Short: "Get or set configuration values",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _ := loadConfig()
		key := args[0]

		if len(args) == 1 {
			// Get
			val, err := cfg.Get(key)
			if err != nil {
				fmt.Fprintln(os.Stderr, ui.Error.Render(err.Error()))
				return nil
			}
			if JSONRequested() {
				// The raw value ("" when unset) — machines don't need the
				// human "(not set)" placeholder.
				return printConfigJSON(map[string]string{"key": key, "value": val})
			}
			if val == "" {
				val = "(not set)"
			}
			fmt.Printf("%s  %s\n", ui.Label.Render(key+":"), val)
			return nil
		}

		// Set
		value := args[1]
		if err := cfg.Set(key, value); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render(err.Error()))
			return nil
		}
		if err := cfg.Save(); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to save config:"), err)
			return nil
		}
		if JSONRequested() {
			return printConfigJSON(map[string]any{"key": key, "value": value, "saved": true})
		}
		fmt.Printf("%s  %s = %s\n", ui.Success.Render("✓"), key, value)
		return nil
	},
}

// printConfigJSON marshals v as indented JSON to stdout.
func printConfigJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func init() {
	configCmd.Flags().BoolVar(&configJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(configCmd)
}
