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

		launch := loadPackageLaunch(storeDir, *pkg)
		if launch.Endpoint == "" && launch.Bin == "" && launch.Command == "" {
			// Registry fetch fills endpoint/bin/command for older installs
			// that only persisted transport+location.
			if sm, err := loadStartManifest(pkg); err == nil && sm.manifest != nil {
				if launch.Endpoint == "" {
					launch.Endpoint = sm.manifest.Endpoint
				}
				if launch.Bin == "" {
					launch.Bin = sm.manifest.Bin
				}
				if launch.Command == "" {
					launch.Command = sm.manifest.Command
				}
				if launch.Runtime == "" {
					launch.Runtime = sm.manifest.Runtime
				}
				if launch.Transport == "" {
					launch.Transport = sm.manifest.Transport
				}
				if launch.Location == "" {
					launch.Location = sm.workDir
				}
			}
		}

		plan := planStart(*pkg, launch, startForeground)
		switch plan.Action {
		case startActionRefuseRemote:
			fmt.Printf("%s %s\n", ui.Label.Render("ℹ"), plan.Message)
			if launch.Endpoint != "" {
				fmt.Printf("%s Endpoint: %s\n", ui.Muted.Render(" "), launch.Endpoint)
			}
			return
		case startActionRefuseStdio:
			fmt.Printf("%s %s is a stdio server — it launches automatically when an\n",
				ui.Label.Render("ℹ"), ui.PackageName.Render(name))
			fmt.Printf("%s MCP client (Cursor, Claude Desktop, etc.) connects. No need to start it manually.\n",
				ui.Muted.Render(" "))
			fmt.Printf("%s To run in foreground for debugging: pharos start %s --foreground\n",
				ui.Muted.Render(" "), name)
			return
		case startActionError:
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot start:"), plan.Message)
			return
		}

		if isRemoteLaunchURL(plan.Command) {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Refusing to execute a remote URL as a local process."))
			return
		}

		kind := inferLaunchKind(launch)
		port := resolveStartListenPort(startPort, plan.Port, kind == kindLocalHTTP)

		// Kind 2: if the listen port is already accepting (or daemon.json
		// says running), treat start as a success no-op. runtime.Start
		// would otherwise fail with "port already in use".
		if kind == kindLocalHTTP {
			daemon := loadDaemonState()
			st := runtime.ProbeStatus(name, port)
			if resolveKind2StartAction(name, launch, plan, st, daemon) == startActionAlreadyRunning {
				if port > 0 {
					fmt.Printf("%s %s is already running on port %d\n", ui.Success.Render("✓"), ui.PackageName.Render(name), port)
				} else {
					fmt.Printf("%s %s is already running\n", ui.Success.Render("✓"), ui.PackageName.Render(name))
				}
				return
			}
		}

		result, err := runtime.Start(runtime.StartOptions{
			Name:       name,
			Command:    plan.Command,
			WorkDir:    plan.WorkDir,
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

		// runtime.Start already waited for a live PID (and listen, when
		// the port is known). Only print success after a nil error.
		if port > 0 {
			fmt.Printf("%s Started %s (PID %d) on port %d\n", ui.Success.Render("✓"), ui.PackageName.Render(name), result.PID, port)
		} else {
			fmt.Printf("%s Started %s (PID %d)\n", ui.Success.Render("✓"), ui.PackageName.Render(name), result.PID)
		}

		logDir := filepath.Dir(plan.WorkDir)
		if logDir == "." || plan.WorkDir == "" {
			logDir = storeDir
		}
		logFile := filepath.Join(logDir, name+".log")
		fmt.Printf("%s Logs: %s\n", ui.Muted.Render(" "), logFile)
	},
}

// defaultLocalHTTPPort is the well-known listen port used when a kind-2
// local HTTP server has no --port / :PORT in its command. Same default
// the list agent uses for test-echo-server. Never applied to remote URLs.
const defaultLocalHTTPPort = 8765

// resolveStartListenPort chooses the port runtime.Start should wait on.
// Flag wins, then plan.Port from ExtractPort, then 8765 only for local HTTP.
func resolveStartListenPort(flagPort, planPort int, localHTTP bool) int {
	if flagPort > 0 {
		return flagPort
	}
	if planPort > 0 {
		return planPort
	}
	if localHTTP {
		return defaultLocalHTTPPort
	}
	return 0
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
