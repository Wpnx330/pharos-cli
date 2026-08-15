package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/canonical"
	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/install"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/resolver"
	"github.com/Wpnx330/pharos-cli/internal/runtime"
	"github.com/Wpnx330/pharos-cli/internal/semver"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var (
	installVersion       string
	installGlobal        bool
	installClient        string
	installSelectClients bool
	installFrozen        bool
	installSkipDepConfig bool
	installIdleTimeout   int
)

var installCmd = &cobra.Command{
	Use:     "install <name>[@version]",
	Aliases: []string{"i"},
	Short:   "Download and install an MCP server package",
	Long: ui.Label.Render("pharos install") + ` — transport-aware installation of MCP server packages.

Resolves semver ranges (^, ~, x, latest), downloads and verifies tarballs
for stdio servers, writes config entries to detected MCP clients, and
updates pharos.lock.

Examples:
  pharos install @scope/server-git          # latest
  pharos install mcp-git-server@^1.0.0      # caret range
  pharos install mcp-git-server@~1.2.0      # tilde range
  pharos install mcp-git-server --version 1.2.0
  pharos install mcp-git-server --client cursor
  pharos install mcp-git-server --client cursor,claude-desktop  # multi-select
  pharos install mcp-git-server --select-clients  # interactive picker
  pharos install mcp-git-server --idle-timeout 30  # auto-unload after 30min idle
  pharos install mcp-git-server --idle-timeout 0   # never unload (always on)
  pharos install --frozen                   # install from lockfile only`,
	Args: cobra.ExactArgs(1),
	Run:  runInstall,
}

func init() {
	installCmd.Flags().StringVarP(&installVersion, "version", "v", "", "version or range to install (e.g. 1.2.0, ^1.0.0, latest)")
	installCmd.Flags().BoolVarP(&installGlobal, "global", "g", false, "install system-wide")
	installCmd.Flags().StringVarP(&installClient, "client", "c", "", "write config only to these clients (comma-separated: cursor,claude-desktop,generic)")
	installCmd.Flags().BoolVar(&installSelectClients, "select-clients", false, "interactively pick which MCP clients to configure")
	installCmd.Flags().BoolVar(&installFrozen, "frozen", false, "install strictly from lockfile; refuse if missing or mismatched")
	installCmd.Flags().BoolVar(&installSkipDepConfig, "no-dep-config", false, "don't write MCP client configs for dependencies")
	installCmd.Flags().IntVar(&installIdleTimeout, "idle-timeout", 60, "idle timeout in minutes before auto-unloading (0 = never unload, always on)")
	rootCmd.AddCommand(installCmd)
}

func runInstall(cmd *cobra.Command, args []string) {
	_, client := loadConfig()
	input := args[0]

	// Parse name@version syntax.
	name, versionSpec := parseNameVersion(input)
	if installVersion != "" {
		versionSpec = installVersion // --version flag takes precedence
	}

	// Determine lockfile path.
	lockPath, err := lockfile.DefaultPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot determine lockfile path:"), err)
		return
	}

	// --frozen mode: resolve from lockfile only.
	if installFrozen {
		installFromLockfile(name, versionSpec, lockPath, resolveClientSelection())
		return
	}

	fmt.Printf("%s  %s\n", ui.Label.Render("Fetching package info..."), name)
	pkg, err := client.GetPackage(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to fetch package:"), err)
		return
	}

	// Resolve version via semver.
	available := pkg.VersionStrings()
	if len(available) == 0 {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Package has no published versions."))
		return
	}

	resolvedVersion, err := semver.Resolve(versionSpec, available, pkg.DistTags)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Version resolution failed:"), err)
		return
	}

	// Find the version detail (manifest).
	vd := pkg.FindVersion(resolvedVersion)
	if vd == nil {
		fmt.Fprintf(os.Stderr, "%s  %s\n", ui.Error.Render("Internal error:"), "resolved version not found in packument")
		return
	}

	manifest := vd.Manifest
	transport := strings.ToLower(strings.TrimSpace(manifest.Transport))
	if transport == "" {
		transport = "stdio"
	}

	// Pre-install check: warn if the runtime executable is missing.
	// We extract the binary name from the manifest's bin/command field
	// and check if it's on PATH. This is advisory — we proceed anyway
	// since the user might be installing on a different machine than
	// the one they'll run on.
	if missing := checkRuntimeRequirement(manifest, transport); missing != "" {
		fmt.Fprintf(os.Stderr, "%s  %s\n", ui.Error.Render("Warning:"), missing)
	}

	// Determine store directory.
	storeDir, err := install.DefaultStoreDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot determine store directory:"), err)
		return
	}
	if installGlobal {
		storeDir = "/usr/local/pharos/store"
	}

	mgr := install.NewManager(storeDir)

	var result *install.InstallResult
	var resolvedURL string

	if transport == "stdio" {
		// Download, verify, extract.
		resolvedURL = client.TarballURL(name, resolvedVersion)
		fmt.Printf("%s  %s@%s\n", ui.Label.Render("Downloading..."), name, resolvedVersion)

		if mgr.IsInstalled(name, resolvedVersion) {
			fmt.Printf("%s  %s\n", ui.Muted.Render("Already installed:"), fmt.Sprintf("%s@%s", name, resolvedVersion))
		} else {
			result, err = mgr.InstallStdio(name, resolvedVersion, resolvedURL, manifest.Integrity)
			if err != nil {
				fmt.Fprintln(os.Stderr, ui.Error.Render("Install failed:"), err)
				return
			}
		}
	} else {
		// http/sse: check if the package has a local command (bin/command field).
		// If so, download the tarball so the server files are present for `pharos start`.
		// If not (pure remote, endpoint-only), skip the download.
		if manifest.Bin != "" {
			resolvedURL = client.TarballURL(name, resolvedVersion)
			fmt.Printf("%s  %s@%s (%s)\n", ui.Label.Render("Downloading..."), name, resolvedVersion, transport)
			if mgr.IsInstalled(name, resolvedVersion) {
				fmt.Printf("%s  %s\n", ui.Muted.Render("Already installed:"), fmt.Sprintf("%s@%s", name, resolvedVersion))
			} else {
				result, err = mgr.InstallStdio(name, resolvedVersion, resolvedURL, manifest.Integrity)
				if err != nil {
					fmt.Fprintln(os.Stderr, ui.Error.Render("Install failed:"), err)
					return
				}
				// Override transport in the metadata to reflect the actual transport
				result.Transport = transport
				// Also fix the installed package metadata
				mgr.UpdateTransport(name, resolvedVersion, transport)
			}
		} else {
			fmt.Printf("%s  %s@%s (%s)\n", ui.Label.Render("Installing remote server..."), name, resolvedVersion, transport)
			result, err = mgr.InstallHTTP(name, resolvedVersion)
			if err != nil {
				fmt.Fprintln(os.Stderr, ui.Error.Render("Install failed:"), err)
				return
			}
			resolvedURL = manifest.Endpoint
		}
	}

	// If result is nil because already installed, construct it.
	if result == nil {
		result = &install.InstallResult{
			Name:      name,
			Version:   resolvedVersion,
			Transport: transport,
		}
	}

	// Build client config.
	serverCfg := install.BuildServerConfig(manifest, storeDir)

	// Resolve which clients to write to.
	clientIDs := resolveClientSelection()

	// Write to the canonical Pharos config first (~/.pharos/mcp.json).
	// This is the single source of truth; client configs are synced from it.
	canonSrv := canonical.Server{
		Transport:   transport,
		Enabled:     true,
		IdleTimeout: installIdleTimeout,
		Package: canonical.PackageInfo{
			Name:      name,
			Version:   resolvedVersion,
			Integrity: manifest.Integrity,
			Source:    "pharos",
		},
	}
	// Populate command/args/env/url/cwd from the server config
	if serverCfg.URL != "" {
		canonSrv.URL = serverCfg.URL
	} else {
		canonSrv.Command = serverCfg.Command
		canonSrv.Args = serverCfg.Args
	}
	if len(serverCfg.Env) > 0 {
		canonSrv.Env = serverCfg.Env
	}
	if storeDir != "" {
		canonSrv.Cwd = filepath.Join(storeDir, name, resolvedVersion)
	}
	if err := canonical.AddServer(name, canonSrv); err != nil {
		fmt.Fprintf(os.Stderr, "%s  %v\n", ui.Error.Render("Warning: failed to write canonical config:"), err)
	}

	// Write to selected MCP clients.
	fmt.Printf("%s\n", ui.Label.Render("Writing MCP client configs..."))
	updated, err := install.WriteClientConfigs(name, serverCfg, clientIDs)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Config write failed:"), err)
		// Don't return — still update lockfile.
	}
	for _, c := range updated {
		fmt.Printf("  %s  %s\n", ui.Success.Render("✓"), c.Name)
	}

	// Update lockfile.
	if err := install.UpdateLockfile(lockPath, result, resolvedURL); err != nil {
		fmt.Fprintf(os.Stderr, "%s  %s\n", ui.Error.Render("Lockfile update failed:"), err)
	} else {
		fmt.Printf("%s  %s\n", ui.Muted.Render("Lockfile updated:"), lockPath)
	}

	// Report install telemetry for http/sse packages. stdio packages are
	// counted server-side via the tarball redirect increment, so we only
	// send an explicit install-event for remote transports. This is
	// best-effort: failures are silently ignored.
	if transport != "stdio" {
		_ = client.ReportInstallEvent(name, resolvedVersion)
	}

	// --- Dependency resolution ---
	// After the primary package is installed, check if it declares
	// dependencies. If so, resolve and install them recursively.
	if len(manifest.Dependencies) > 0 {
		fmt.Printf("\n%s\n", ui.Label.Render("Resolving dependencies..."))
		r := resolver.New(client)
		depResult, err := r.ResolveAll(manifest.Dependencies)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s  %v\n", ui.Error.Render("Dependency resolution failed:"), err)
		} else {
			// Report conflicts and circular deps as warnings.
			for _, c := range depResult.Conflicts {
				fmt.Fprintf(os.Stderr, "%s  %s: %s vs %s → %s (higher)\n",
					ui.Error.Render("Warning: version conflict"),
					c.Name, c.Existing, c.Requested, c.Resolution)
			}
			for _, cyc := range depResult.Circular {
				fmt.Fprintf(os.Stderr, "%s  circular dependency detected: %s (skipped)\n",
					ui.Error.Render("Warning:"), cyc)
			}

			// Install each resolved dependency that isn't already installed.
			installed := 0
			skipped := 0
			for depName, depVersion := range depResult.Flat {
				// Skip the primary package itself.
				if depName == name {
					continue
				}

				// Fetch the dependency's manifest — needed for both install
				// and client config writing (even if already installed).
				depPkg, err := client.GetPackage(depName)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s  failed to fetch %s: %v\n", ui.Error.Render("Warning:"), depName, err)
					continue
				}
				depVD := depPkg.FindVersion(depVersion)
				if depVD == nil {
					fmt.Fprintf(os.Stderr, "%s  %s@%s not found in registry\n", ui.Error.Render("Warning:"), depName, depVersion)
					continue
				}
				depTransport := strings.ToLower(strings.TrimSpace(depVD.Manifest.Transport))
				if depTransport == "" {
					depTransport = "stdio"
				}
				depURL := client.TarballURL(depName, depVersion)

				if mgr.IsInstalled(depName, depVersion) {
					fmt.Printf("  %s  %s\n", ui.Muted.Render("Already installed:"), fmt.Sprintf("%s@%s", depName, depVersion))
					skipped++
					// Even if already installed, write client configs so the
					// dependency is accessible to MCP clients. Skip if
					// --no-dep-config was passed.
					if !installSkipDepConfig {
						depCfg := install.BuildServerConfig(depVD.Manifest, storeDir)
						_, _ = install.WriteClientConfigs(depName, depCfg, clientIDs)
					}
					continue
				}

				// Transport-aware install: http/sse dependencies don't need a
				// tarball download — they're remote servers. stdio deps get
				// downloaded and extracted locally.
				var depInstallResult *install.InstallResult
				if depTransport == "http" || depTransport == "http-sse" || depTransport == "sse" {
					depInstallResult, err = mgr.InstallHTTP(depName, depVersion)
				} else {
					depInstallResult, err = mgr.InstallStdio(depName, depVersion, depURL, depVD.Manifest.Integrity)
				}
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s  failed to install %s@%s: %v\n", ui.Error.Render("Warning:"), depName, depVersion, err)
					continue
				}
				_ = depInstallResult
				// Write client config for the dependency (unless --no-dep-config).
				if !installSkipDepConfig {
					depCfg := install.BuildServerConfig(depVD.Manifest, storeDir)
					_, _ = install.WriteClientConfigs(depName, depCfg, clientIDs)
				}
				// Update lockfile for the dependency.
				_ = install.UpdateLockfile(lockPath, &install.InstallResult{
					Name: depName, Version: depVersion, Transport: depTransport,
				}, depURL)
				fmt.Printf("  %s  %s@%s\n", ui.Success.Render("✓"), depName, depVersion)
				installed++
			}
			if installed > 0 || skipped > 0 {
				fmt.Printf("%s  %d installed, %d already present\n",
					ui.Muted.Render("Dependencies:"), installed, skipped)
			}
		}
	}

	// Success summary.
	fmt.Printf("\n%s  %s\n", ui.Success.Render("✓ Installed:"), fmt.Sprintf("%s@%s (%s)", name, resolvedVersion, transport))
	fmt.Printf("%s    %s\n", ui.Muted.Render("Usage:"), fmt.Sprintf("pharos info %s", name))

	// Auto-start daemon for HTTP/SSE servers
	if transport == "http-sse" || transport == "http" || transport == "streamable-http" {
		ensureDaemonRunning()
	}
}

// installFromLockfile installs strictly from the lockfile.
func installFromLockfile(name, versionSpec, lockPath string, clientIDs []string) {
	lf, err := lockfile.Load(lockPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot read lockfile:"), err)
		return
	}
	if !lf.Has(name) {
		fmt.Fprintf(os.Stderr, "%s  %s not found in lockfile\n", ui.Error.Render("Frozen install failed:"), name)
		return
	}
	entry, _ := lf.Get(name)

	if versionSpec != "" && versionSpec != "latest" && versionSpec != entry.Version {
		fmt.Fprintf(os.Stderr, "%s  lockfile has %s@%s but you requested %s\n",
			ui.Error.Render("Frozen install failed:"), name, entry.Version, versionSpec)
		return
	}

	_, client := loadConfig()

	// Fetch the manifest for the locked version to build client config.
	vd, err := client.GetVersionManifest(name, entry.Version)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to fetch manifest for locked version:"), err)
		return
	}

	storeDir, _ := install.DefaultStoreDir()
	mgr := install.NewManager(storeDir)

	if entry.Transport == "stdio" {
		if mgr.IsInstalled(name, entry.Version) {
			fmt.Printf("%s  %s\n", ui.Muted.Render("Already installed:"), fmt.Sprintf("%s@%s", name, entry.Version))
		} else {
			fmt.Printf("%s  %s@%s (from lockfile)\n", ui.Label.Render("Downloading..."), name, entry.Version)
			_, err := mgr.InstallStdio(name, entry.Version, entry.Resolved, entry.Integrity)
			if err != nil {
				fmt.Fprintln(os.Stderr, ui.Error.Render("Install failed:"), err)
				return
			}
		}
	}

	// Build client config.
	serverCfg := install.BuildServerConfig(vd.Manifest, storeDir)

	// Write to canonical config.
	transport := strings.ToLower(strings.TrimSpace(vd.Manifest.Transport))
	if transport == "" {
		transport = "stdio"
	}
	canonSrv := canonical.Server{
		Transport:   transport,
		Enabled:     true,
		IdleTimeout: installIdleTimeout,
		Package: canonical.PackageInfo{
			Name:      name,
			Version:   entry.Version,
			Integrity: entry.Integrity,
			Source:    "pharos",
		},
	}
	if serverCfg.URL != "" {
		canonSrv.URL = serverCfg.URL
	} else {
		canonSrv.Command = serverCfg.Command
		canonSrv.Args = serverCfg.Args
	}
	if len(serverCfg.Env) > 0 {
		canonSrv.Env = serverCfg.Env
	}
	if storeDir != "" {
		canonSrv.Cwd = filepath.Join(storeDir, name, entry.Version)
	}
	if err := canonical.AddServer(name, canonSrv); err != nil {
		fmt.Fprintf(os.Stderr, "%s  %v\n", ui.Error.Render("Warning: failed to write canonical config:"), err)
	}

	// Write client config.
	updated, err := install.WriteClientConfigs(name, serverCfg, clientIDs)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Config write failed:"), err)
	} else {
		for _, c := range updated {
			fmt.Printf("  %s  %s\n", ui.Success.Render("✓"), c.Name)
		}
	}

	fmt.Printf("\n%s  %s\n", ui.Success.Render("✓ Installed (frozen):"), fmt.Sprintf("%s@%s", name, entry.Version))

	// Auto-start daemon for HTTP/SSE servers
	if transport == "http-sse" || transport == "http" || transport == "streamable-http" {
		ensureDaemonRunning()
	}
}

// parseNameVersion splits "name@version" into parts. Names with scoped
// syntax (@scope/pkg) are handled correctly — only the last @ is the
// version separator.
func parseNameVersion(input string) (name, version string) {
	// Scoped package: @scope/name@version
	if strings.HasPrefix(input, "@") {
		// Find the second @ (after the scope).
		rest := input[1:]
		if idx := strings.Index(rest, "@"); idx >= 0 {
			return input[:1+idx], rest[idx+1:]
		}
		return input, ""
	}
	if idx := strings.Index(input, "@"); idx >= 0 {
		return input[:idx], input[idx+1:]
	}
	return input, ""
}

// checkRuntimeRequirement checks if the executable needed to run the
// package is on PATH. Returns a warning message if missing, empty string
// if all good. For pure remote http/sse packages (no bin/command), no
// check is needed.
func checkRuntimeRequirement(m api.Manifest, transport string) string {
	// For pure remote servers (http/sse with endpoint only, no bin), no
	// local runtime is needed.
	if transport != "stdio" && m.Bin == "" {
		return ""
	}

	// Extract the executable from bin field.
	cmdStr := m.Bin
	if cmdStr == "" {
		return ""
	}
	parts := strings.Fields(cmdStr)
	if len(parts) == 0 {
		return ""
	}
	exe := parts[0]

	if _, err := exec.LookPath(exe); err != nil {
		hint := runtime.ExecutableHint(exe)
		if hint != "" {
			return fmt.Sprintf("this package requires %q which is not on $PATH: %s", exe, hint)
		}
		return fmt.Sprintf("this package requires %q which is not on $PATH", exe)
	}
	return ""
}

// resolveClientSelection determines which MCP clients to write configs to.
// Three modes:
//   - auto: no --client and no --select-clients → write to all detected clients
//   - explicit: --client cursor,claude-desktop → write only to those (must be detected)
//   - interactive: --select-clients → show a checkbox picker of all known clients
//     (detected + undetected), let the user pick, then write to selected ones
//
// Returns a list of ClientID strings to target. Empty list means "auto" (all detected).
func resolveClientSelection() []string {
	// Mode 1: explicit comma-separated list
	if installClient != "" {
		return strings.Split(installClient, ",")
	}

	// Mode 2: interactive picker
	if installSelectClients {
		// Build the list of ALL known clients with detection status.
		candidates := clientconfig.CandidatePaths()
		// Also include custom clients
		allClients := clientconfig.Detect()
		// Build display labels: "Name (detected)" or "Name (not detected)"
		detectedIDs := make(map[string]bool)
		for _, c := range allClients {
			detectedIDs[string(c.ID)] = true
		}

		var labels []string
		labelToID := make(map[string]string)
		var defaults []string

		for _, c := range candidates {
			label := c.Name
			if detectedIDs[string(c.ID)] {
				label += " (detected)"
				defaults = append(defaults, label)
			} else {
				label += " (not detected)"
			}
			labels = append(labels, label)
			labelToID[label] = string(c.ID)
		}
		// Add custom clients to the picker
		for _, c := range allClients {
			if c.Custom {
				label := c.Name + " (detected)"
				if _, exists := labelToID[label]; !exists {
					labels = append(labels, label)
					labelToID[label] = string(c.ID)
					defaults = append(defaults, label)
				}
			}
		}

		selected := multiSelectPrompt("Select MCP clients to configure", labels, defaults)
		var ids []string
		for _, label := range selected {
			ids = append(ids, labelToID[label])
		}
		return ids
	}

	// Mode 3: auto — empty list signals "all detected"
	return nil
}
