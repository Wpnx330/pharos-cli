package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/install"
	"github.com/Wpnx330/pharos-cli/internal/manifest"
	"github.com/Wpnx330/pharos-cli/internal/runtime"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var (
	startForeground bool
	startEnv        []string
	startPort       int
)

var startCmd = &cobra.Command{
	Use:   "start <name>",
	Short: "Start a locally installed MCP server",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		// Find the installed package
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Error:"), err)
			return
		}
		storeDir := filepath.Join(home, ".pharos", "store")
		mgr := install.NewManager(storeDir)

		pkgs, err := mgr.List()
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to list packages:"), err)
			return
		}

		var pkg *install.InstalledPackage
		for i := range pkgs {
			if pkgs[i].Name == name {
				pkg = &pkgs[i]
				break
			}
		}
		if pkg == nil {
			fmt.Fprintf(os.Stderr, "%s Package %q is not installed. Use 'pharos install %s' first.\n", ui.Error.Render("Error:"), name, name)
			return
		}

		// Read the manifest from the install location
		manifestPath := filepath.Join(pkg.Location, "pharos.json")
		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot read manifest:"), err)
			return
		}
		m, err := manifest.Parse(manifestData)
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot parse manifest:"), err)
			return
		}

		// Determine the command to run
		runCmd := m.RunCommand()
		if runCmd == "" {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Manifest has no 'command' or 'bin' field — cannot determine how to start the server."))
			return
		}

		// Determine the port
		port := startPort
		if port == 0 && (m.Transport == "http-sse" || m.Transport == "http") {
			// Try to extract port from the command string (e.g. "python server.py --port 8765")
			port = runtime.ExtractPort(runCmd)
		}

		// Start the server
		result, err := runtime.Start(runtime.StartOptions{
			Name:       name,
			Command:    runCmd,
			WorkDir:    pkg.Location,
			Env:        startEnv,
			Port:       port,
			Foreground: startForeground,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to start:"), err)
			return
		}

		if startForeground {
			return // foreground mode blocks until the server exits
		}

		// For http-sse servers, probe the port to confirm startup
		if port > 0 {
			fmt.Printf("%s Starting %s on port %d...\n", ui.Label.Render("▸"), ui.PackageName.Render(name), port)
			// Give the server a moment to bind
			if runtime.IsRunning(result.PID) {
				fmt.Printf("%s Started %s (PID %d) on port %d\n", ui.Success.Render("✓"), ui.PackageName.Render(name), result.PID, port)
			} else {
				fmt.Fprintln(os.Stderr, ui.Error.Render("Server process exited immediately. Check the log file."))
			}
		} else {
			fmt.Printf("%s Started %s (PID %d)\n", ui.Success.Render("✓"), ui.PackageName.Render(name), result.PID)
		}

		// Show log file location
		logFile := filepath.Join(filepath.Dir(pkg.Location), name+".log")
		fmt.Printf("%s Logs: %s\n", ui.Muted.Render(" "), logFile)
	},
}

func init() {
	startCmd.Flags().BoolVar(&startForeground, "foreground", false, "run in foreground (block terminal, Ctrl+C to stop)")
	startCmd.Flags().StringArrayVar(&startEnv, "env", nil, "environment variable KEY=VALUE (repeatable)")
	startCmd.Flags().IntVar(&startPort, "port", 0, "override declared port (http-sse only)")
	rootCmd.AddCommand(startCmd)
}
