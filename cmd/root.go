// Package cmd implements all PHAROS CLI commands using cobra.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/config"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

// Version is the CLI version, set at build time via ldflags.
var Version = "1.1.0"

// jsonFlag is the global --json flag.
var jsonFlag bool

var rootCmd = &cobra.Command{
	Use:   "pharos",
	Short: "PHAROS — MCP server package registry CLI",
	Long: ui.Label.Render("PHAROS") + " — a CLI for searching, installing, and publishing\nMCP server packages on the PHAROS registry.",
	SilenceUsage: true,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// loadConfig loads the config from disk and returns a config + API client.
func loadConfig() (*config.Config, *api.Client) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Error loading config:"), err)
		os.Exit(1)
	}
	return cfg, api.New(cfg.Registry, cfg.Token)
}
