package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/install"
	"github.com/Wpnx330/pharos-cli/internal/manifest"
	"github.com/Wpnx330/pharos-cli/internal/runtime"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

// startManifest holds the manifest and working directory for a started server.
type startManifest struct {
	manifest *manifest.Manifest
	workDir  string
}

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

		// Load the manifest. For stdio installs, pkg.Location points to the
		// extracted tarball directory and pharos.json lives there. For http/sse
		// installs, Location is empty — there's no local tarball — so we fetch
		// the manifest from the registry instead.
		sm, err := loadStartManifest(pkg)
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot load manifest:"), err)
			return
		}
		m := sm.manifest

		// stdio servers communicate over stdin/stdout — they're meant to be
		// spawned by MCP clients (Cursor, Claude Desktop, etc.) as child
		// processes with piped I/O. Starting one in background mode makes no
		// sense: the process would have no stdin to read from and would exit
		// immediately. Only allow --foreground (useful for debugging) or
		// refuse with an informative message.
		transport := strings.ToLower(strings.TrimSpace(m.Transport))
		if transport == "" {
			transport = "stdio"
		}
		if transport == "stdio" && !startForeground {
			fmt.Printf("%s %s is a stdio server — it launches automatically when an\n",
				ui.Label.Render("ℹ"), ui.PackageName.Render(name))
			fmt.Printf("%s MCP client (Cursor, Claude Desktop, etc.) connects. No need to start it manually.\n",
				ui.Muted.Render(" "))
			fmt.Printf("%s To run in foreground for debugging: pharos start %s --foreground\n",
				ui.Muted.Render(" "), name)
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
			WorkDir:    sm.workDir,
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
		logDir := filepath.Dir(sm.workDir)
		if logDir == "." {
			logDir = filepath.Join(os.Getenv("HOME"), ".pharos", "store")
		}
		logFile := filepath.Join(logDir, name+".log")
		fmt.Printf("%s Logs: %s\n", ui.Muted.Render(" "), logFile)
	},
}

// loadStartManifest resolves the manifest for an installed package.
// For stdio installs (pkg.Location non-empty), it reads pharos.json from
// the extracted tarball directory. For http/sse installs (pkg.Location
// empty), it fetches the manifest from the registry.
func loadStartManifest(pkg *install.InstalledPackage) (*startManifest, error) {
	// stdio install: read local pharos.json
	if pkg.Location != "" {
		manifestPath := filepath.Join(pkg.Location, "pharos.json")
		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			return nil, fmt.Errorf("read manifest: %w", err)
		}
		m, err := manifest.Parse(manifestData)
		if err != nil {
			return nil, fmt.Errorf("parse manifest: %w", err)
		}
		return &startManifest{manifest: m, workDir: pkg.Location}, nil
	}

	// http/sse install: fetch manifest from registry
	_, client := loadConfig()
	vd, err := client.GetVersionManifest(pkg.Name, pkg.Version)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest from registry: %w", err)
	}

	// Convert api.Manifest to manifest.Manifest
	m := &manifest.Manifest{
		Name:         vd.Manifest.Name,
		Version:      vd.Manifest.Version,
		Description:  vd.Manifest.Description,
		Transport:    vd.Manifest.Transport,
		Runtime:      vd.Manifest.Runtime,
		License:      vd.Manifest.License,
		Bin:          vd.Manifest.Bin,
		Capabilities: vd.Manifest.Capabilities,
	}

	// For http/sse servers with a command/bin, use a temp work dir since
	// there's no local tarball. For pure remote servers (endpoint only, no
	// command), workDir is empty — the server runs elsewhere.
	workDir := ""
	if m.RunCommand() != "" {
		// Server has a local command — use the store directory as workDir
		home, _ := os.UserHomeDir()
		workDir = filepath.Join(home, ".pharos", "store", pkg.Name, pkg.Version)
	}

	return &startManifest{manifest: m, workDir: workDir}, nil
}

func init() {
	startCmd.Flags().BoolVar(&startForeground, "foreground", false, "run in foreground (block terminal, Ctrl+C to stop)")
	startCmd.Flags().StringArrayVar(&startEnv, "env", nil, "environment variable KEY=VALUE (repeatable)")
	startCmd.Flags().IntVar(&startPort, "port", 0, "override declared port (http-sse only)")
	rootCmd.AddCommand(startCmd)
}
