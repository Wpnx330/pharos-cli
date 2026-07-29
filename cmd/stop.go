package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/runtime"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var (
	stopForce   bool
	stopAll     bool
	stopTimeout int
)

var stopCmd = &cobra.Command{
	Use:   "stop <name>",
	Short: "Stop a running MCP server",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if stopAll {
			stopped, err := runtime.StopAll(stopForce, stopTimeout)
			if err != nil {
				fmt.Fprintln(os.Stderr, ui.Error.Render("Error stopping servers:"), err)
				return
			}
			if len(stopped) == 0 {
				fmt.Println(ui.Muted.Render("No running servers to stop."))
				return
			}
			for _, name := range stopped {
				fmt.Printf("%s Stopped %s\n", ui.Success.Render("✓"), ui.PackageName.Render(name))
			}
			return
		}

		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Error:"), "specify a server name or use --all")
			return
		}

		name := args[0]
		err := runtime.Stop(runtime.StopOptions{
			Name:    name,
			Force:   stopForce,
			Timeout: stopTimeout,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Error:"), err)
			return
		}
		fmt.Printf("%s Stopped %s\n", ui.Success.Render("✓"), ui.PackageName.Render(name))
	},
}

func init() {
	stopCmd.Flags().BoolVar(&stopForce, "force", false, "send SIGKILL if SIGTERM doesn't stop within timeout")
	stopCmd.Flags().BoolVar(&stopAll, "all", false, "stop all running Pharos-managed servers")
	stopCmd.Flags().IntVar(&stopTimeout, "timeout", 5, "seconds to wait for graceful shutdown")
	rootCmd.AddCommand(stopCmd)
}
