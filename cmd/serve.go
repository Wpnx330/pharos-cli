package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/config"
	"github.com/Wpnx330/pharos-cli/internal/mcpserver"
)

// serveAllowInstall gates the MCP install tool (default OFF).
var serveAllowInstall bool

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the Pharos MCP stdio server (JSON-RPC 2.0 on stdin/stdout)",
	Long: `pharos serve — expose Pharos as MCP tools over the Model Context Protocol
stdio transport.

Speaks newline-delimited JSON-RPC 2.0 on stdin/stdout so MCP clients
(Claude Desktop, Cursor, Hermes, ...) can wire Pharos in directly:

    search(query, limit?, transport?)   registry search
    info(name)                          package details + install hint
    list_installed()                    pharos.lock contents (read-only)
    install(name, version?)             opt-in via --allow-install

stdout carries ONLY protocol frames; diagnostics go to stderr. The server
runs until stdin closes or the process is signalled.`,
	Args: cobra.NoArgs,
	Run:  runServe,
}

func init() {
	serveCmd.Flags().BoolVar(&serveAllowInstall, "allow-install", false,
		"expose the install tool to MCP clients (installs run the same pipeline as 'pharos install': store, canonical config, detected MCP clients, pharos.lock); default OFF")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) {
	if err := serveMain(serveAllowInstall); err != nil {
		fmt.Fprintln(os.Stderr, "pharos serve:", err)
		os.Exit(1)
	}
}

// serveMain loads config, builds the MCP server, and serves protocol
// frames over stdin/stdout until stdin hits EOF. Errors before serving
// (unreadable config) return; errors during serving are reported on
// stderr by the server itself.
//
// No signal handling by design: the server is stateless, so default
// SIGINT/SIGTERM termination is a clean exit, and MCP clients stop it by
// closing stdin (which ends the read loop).
func serveMain(allowInstall bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	return mcpserver.New(mcpserver.Config{
		Version:      Version,
		AllowInstall: allowInstall,
		Client:       api.New(cfg.Registry, cfg.Token),
	}).Run(context.Background(), os.Stdin, os.Stdout, os.Stderr)
}
