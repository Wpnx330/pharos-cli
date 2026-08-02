package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/install"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/runtime"
	"github.com/Wpnx330/pharos-cli/internal/semver"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var (
	installVersion string
	installGlobal  bool
	installClient  string
	installFrozen  bool
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
  pharos install --frozen                   # install from lockfile only`,
	Args: cobra.ExactArgs(1),
	Run:  runInstall,
}

func init() {
	installCmd.Flags().StringVarP(&installVersion, "version", "v", "", "version or range to install (e.g. 1.2.0, ^1.0.0, latest)")
	installCmd.Flags().BoolVarP(&installGlobal, "global", "g", false, "install system-wide")
	installCmd.Flags().StringVarP(&installClient, "client", "c", "", "write config only to this client (claude-desktop, cursor, generic)")
	installCmd.Flags().BoolVar(&installFrozen, "frozen", false, "install strictly from lockfile; refuse if missing or mismatched")
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
		installFromLockfile(name, versionSpec, lockPath, installClient)
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

	// Write to detected MCP clients.
	fmt.Printf("%s\n", ui.Label.Render("Writing MCP client configs..."))
	updated, err := install.WriteClientConfigs(name, serverCfg, installClient)
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

	// Success summary.
	fmt.Printf("\n%s  %s\n", ui.Success.Render("✓ Installed:"), fmt.Sprintf("%s@%s (%s)", name, resolvedVersion, transport))
	fmt.Printf("%s    %s\n", ui.Muted.Render("Usage:"), fmt.Sprintf("pharos info %s", name))
}

// installFromLockfile installs strictly from the lockfile.
func installFromLockfile(name, versionSpec, lockPath, clientID string) {
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

	// Write client config.
	serverCfg := install.BuildServerConfig(vd.Manifest, storeDir)
	updated, err := install.WriteClientConfigs(name, serverCfg, clientID)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Config write failed:"), err)
	} else {
		for _, c := range updated {
			fmt.Printf("  %s  %s\n", ui.Success.Render("✓"), c.Name)
		}
	}

	fmt.Printf("\n%s  %s\n", ui.Success.Render("✓ Installed (frozen):"), fmt.Sprintf("%s@%s", name, entry.Version))
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
