package cmd

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check the PHAROS registry health",
	Run: func(cmd *cobra.Command, args []string) {
		_, client := loadConfig()
		start := time.Now()
		h, err := client.Health()
		latency := time.Since(start)
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), ui.Error.Render("Registry unhealthy:"), err)
			return
		}
		if JSONRequested() {
			out := map[string]any{
				"status":  h.Status,
				"version": h.Version,
				"latency": latency.String(),
			}
			data, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(data))
			return
		}
		fmt.Printf("%s  %s\n", ui.Label.Render("Status:"), ui.Success.Render(h.Status))
		fmt.Printf("%s  %s\n", ui.Label.Render("Version:"), h.Version)
		fmt.Printf("%s  %s\n", ui.Label.Render("Latency:"), latency.String())
	},
}

func init() {
	healthCmd.Flags().BoolVar(&jsonFlag, "json", false, "output as JSON")
	rootCmd.AddCommand(healthCmd)
}
