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
	Args: cobra.MinimumNArgs(1),
	Run:  runInstall,
}

func init() {
	installCmd.Flags().StringVarP(&installVersion, "version", "v", "", "version or range to install (e.g. 1.2.0, ^1.0.0, latest)")
	installCmd.Flags().BoolVarP(&installGlobal, "global", "g", false, "install system-wide")
	installCmd.Flags().StringVarP(&installClient, "client", "c", "", "write config only to these clients (comma-separated: cursor,claude-desktop,claude-code,generic)")
	installCmd.Flags().BoolVar(&installSelectClients, "select-clients", false, "interactively pick which MCP clients to configure")
	installCmd.Flags().BoolVar(&installFrozen, "frozen", false, "install strictly from lockfile; refuse if missing or mismatched")
	installCmd.Flags().BoolVar(&installSkipDepConfig, "no-dep-config", false, "don't write MCP client configs for dependencies")
	installCmd.Flags().IntVar(&installIdleTimeout, "idle-timeout", 60, "idle timeout in minutes before auto-unloading (0 = never unload, always on)")
	rootCmd.AddCommand(installCmd)
}

func runInstall(cmd *cobra.Command, args []string) {
	_, client := loadConfig()
	input := joinInfoName(args)

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

	progressf("%s  %s\n", ui.Label.Render("Fetching package info..."), name)
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
	kind := classifyInstallManifest(manifest)
	if kind == install.KindNone {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Package is not installable:"), "no endpoint, command, bin, or runtime+package")
		return
	}
	if install.RemoteOnlyRejected(kind, install.EnvRemoteOnly()) {
		fmt.Fprintf(os.Stderr, "%s  PHAROS_REMOTE_ONLY=true refuses kind %s local install of %s@%s (kind 1 remote HTTP only)\n",
			ui.Error.Render("Install rejected:"), kind, name, resolvedVersion)
		return
	}

	transport := strings.ToLower(strings.TrimSpace(manifest.Transport))
	if transport == "" {
		transport = "stdio"
	}

	// Pre-install check: warn if the runtime executable is missing.
	// We extract the binary name from the manifest's bin/command field
	// and check if it's on PATH. This is advisory — we proceed anyway
	// since the user might be installing on a different machine than
	// the one they'll run on. Kind 1 is a remote bookmark — no local runtime.
	if kind != install.KindRemoteHTTP {
		if missing := checkRuntimeRequirement(manifest, transport); missing != "" {
			fmt.Fprintf(os.Stderr, "%s  %s\n", ui.Error.Render("Warning:"), missing)
		}
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

	switch kind {
	case install.KindRemoteHTTP:
		resolvedURL = manifest.Endpoint
		progressf("%s  %s@%s (%s, kind 1 remote)\n", ui.Label.Render("Registering remote server..."), name, resolvedVersion, transport)
	case install.KindLocalHTTP:
		resolvedURL = client.TarballURL(name, resolvedVersion)
		progressf("%s  %s@%s (%s, kind 2 local HTTP)\n", ui.Label.Render("Installing..."), name, resolvedVersion, transport)
	default:
		resolvedURL = client.TarballURL(name, resolvedVersion)
		progressf("%s  %s@%s (%s, kind 3 stdio)\n", ui.Label.Render("Installing..."), name, resolvedVersion, transport)
	}

	if mgr.IsInstalled(name, resolvedVersion) {
		progressf("%s  %s\n", ui.Muted.Render("Already installed:"), fmt.Sprintf("%s@%s", name, resolvedVersion))
	} else {
		result, err = mgr.InstallByKind(install.InstallOptions{
			Name:              name,
			Version:           resolvedVersion,
			TarballURL:        client.TarballURL(name, resolvedVersion),
			ExpectedIntegrity: manifest.Integrity,
			Manifest:          manifest,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Install failed:"), err)
			return
		}
	}

	// If result is nil because already installed, construct it.
	if result == nil {
		result = &install.InstallResult{
			Name:      name,
			Version:   resolvedVersion,
			Transport: transport,
			Kind:      kind,
			Endpoint:  manifest.Endpoint,
		}
	}

	// Launch/canonical config (kind 2 keeps command/args). Client writes
	// use a separate overlay so kind 2 clients get a localhost URL.
	serverCfg := install.BuildServerConfig(manifest, storeDir)
	clientCfg := install.BuildClientConfig(manifest, storeDir)

	// Resolve which clients to write to.
	clientIDs := resolveClientSelection()

	// W1.2 receipt: snapshot every known client config, the canonical
	// config, and the lockfile before any writes, so before-hashes are
	// exact whichever files the run ends up touching.
	rcpt := newReceiptBuilder("install", name, resolvedVersion)
	rcpt.snapshotAllClients(name)
	rcpt.noteLock(lockPath)
	if canonPath, cerr := canonical.FilePath(); cerr == nil {
		rcpt.noteCanonical(canonPath)
	}

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
	} else {
		rcpt.touchCanonical()
	}

	// Write to selected MCP clients.
	progressf("%s\n", ui.Label.Render("Writing MCP client configs..."))
	updated, skipped, err := install.WriteClientConfigs(name, clientCfg, clientIDs)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Config write failed:"), err)
		rcpt.addError("config write failed: %v", err)
		// Don't return — still update lockfile.
	}
	printClientConfigResults(updated, skipped)
	for _, c := range updated {
		rcpt.touch(c.Path, c.Name, "")
		if rcpt.serverWasPresent(c.Path, name) {
			rcpt.server(c.Name, name, "replaced")
		} else {
			rcpt.server(c.Name, name, "added")
		}
	}

	// Update lockfile, recording which clients actually received the
	// config (drift detection keys MISSING findings off this record).
	writtenClients := writtenClientIDs(updated)
	if err := install.UpdateLockfile(lockPath, result, resolvedURL, writtenClients); err != nil {
		fmt.Fprintf(os.Stderr, "%s  %s\n", ui.Error.Render("Lockfile update failed:"), err)
		rcpt.addError("lockfile update failed: %v", err)
	} else {
		rcpt.touchLock()
		progressf("%s  %s\n", ui.Muted.Render("Lockfile updated:"), lockPath)
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
		progressf("\n%s\n", ui.Label.Render("Resolving dependencies..."))
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
				depKind := classifyInstallManifest(depVD.Manifest)
				if depKind == install.KindNone {
					fmt.Fprintf(os.Stderr, "%s  %s@%s is not installable (no endpoint/command/bin/runtime+package)\n", ui.Error.Render("Warning:"), depName, depVersion)
					continue
				}
				if install.RemoteOnlyRejected(depKind, install.EnvRemoteOnly()) {
					fmt.Fprintf(os.Stderr, "%s  skipping local dependency %s@%s under PHAROS_REMOTE_ONLY\n", ui.Error.Render("Warning:"), depName, depVersion)
					continue
				}
				depURL := client.TarballURL(depName, depVersion)
				if depKind == install.KindRemoteHTTP {
					depURL = depVD.Manifest.Endpoint
				}

				if mgr.IsInstalled(depName, depVersion) {
					progressf("  %s  %s\n", ui.Muted.Render("Already installed:"), fmt.Sprintf("%s@%s", depName, depVersion))
					skipped++
					// Even if already installed, write client configs so the
					// dependency is accessible to MCP clients. Skip if
					// --no-dep-config was passed.
					if !installSkipDepConfig {
						depCfg := install.BuildClientConfig(depVD.Manifest, storeDir)
						writeDepClientConfigs(rcpt, depName, depCfg, clientIDs)
					}
					continue
				}

				depInstallResult, err := mgr.InstallByKind(install.InstallOptions{
					Name:              depName,
					Version:           depVersion,
					TarballURL:        client.TarballURL(depName, depVersion),
					ExpectedIntegrity: depVD.Manifest.Integrity,
					Manifest:          depVD.Manifest,
				})
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s  failed to install %s@%s: %v\n", ui.Error.Render("Warning:"), depName, depVersion, err)
					continue
				}
				if depInstallResult != nil && depInstallResult.Transport != "" {
					depTransport = depInstallResult.Transport
				}
				// Write client config for the dependency (unless
				// --no-dep-config), then update the lockfile for it,
				// recording the clients that actually received the config.
				var depUpdated []clientconfig.Client
				if !installSkipDepConfig {
					depCfg := install.BuildClientConfig(depVD.Manifest, storeDir)
					depUpdated = writeDepClientConfigs(rcpt, depName, depCfg, clientIDs)
				}
				if err := install.UpdateLockfile(lockPath, &install.InstallResult{
					Name: depName, Version: depVersion, Transport: depTransport, Kind: depKind,
				}, depURL, writtenClientIDs(depUpdated)); err != nil {
					rcpt.addError("dependency %s lockfile update failed: %v", depName, err)
				} else {
					rcpt.touchLock()
				}
				progressf("  %s  %s@%s\n", ui.Success.Render("✓"), depName, depVersion)
				installed++
			}
			if installed > 0 || skipped > 0 {
				progressf("%s  %d installed, %d already present\n",
					ui.Muted.Render("Dependencies:"), installed, skipped)
			}
		}
	}

	// Success summary.
	progressf("\n%s  %s\n", ui.Success.Render("✓ Installed:"), fmt.Sprintf("%s@%s (%s, kind %s)", name, resolvedVersion, transport, kind))
	progressf("%s    %s\n", ui.Muted.Render("Usage:"), fmt.Sprintf("pharos info %s", name))

	// W1.2: deterministic receipt of everything this install changed.
	rcpt.emit()

	// Auto-start daemon only for kind 2 (we host the HTTP process locally).
	// Kind 1 is a publisher URL bookmark — do not pretend we run it.
	if kind == install.KindLocalHTTP {
		ensureDaemonRunning(name)
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

	kind := classifyInstallManifest(vd.Manifest)
	if kind == install.KindNone {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Frozen install failed:"), "locked package is not installable")
		return
	}
	if install.RemoteOnlyRejected(kind, install.EnvRemoteOnly()) {
		fmt.Fprintf(os.Stderr, "%s  PHAROS_REMOTE_ONLY refuses kind %s\n", ui.Error.Render("Frozen install failed:"), kind)
		return
	}

	if mgr.IsInstalled(name, entry.Version) {
		progressf("%s  %s\n", ui.Muted.Render("Already installed:"), fmt.Sprintf("%s@%s", name, entry.Version))
	} else {
		progressf("%s  %s@%s (from lockfile, kind %s)\n", ui.Label.Render("Installing..."), name, entry.Version, kind)
		tarballURL := entry.Resolved
		if kind != install.KindRemoteHTTP && tarballURL == "" {
			tarballURL = client.TarballURL(name, entry.Version)
		}
		_, err := mgr.InstallByKind(install.InstallOptions{
			Name:              name,
			Version:           entry.Version,
			TarballURL:        tarballURL,
			ExpectedIntegrity: entry.Integrity,
			Manifest:          vd.Manifest,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Install failed:"), err)
			return
		}
	}

	// Launch/canonical config vs client overlay (kind 2 localhost URL).
	serverCfg := install.BuildServerConfig(vd.Manifest, storeDir)
	clientCfg := install.BuildClientConfig(vd.Manifest, storeDir)

	// Write to canonical config.
	transport := strings.ToLower(strings.TrimSpace(vd.Manifest.Transport))
	if transport == "" {
		transport = "stdio"
	}

	// W1.2 receipt: snapshot every known client config and the canonical
	// config before any writes. Frozen installs write client configs but
	// never the lockfile, so no lockfile row is recorded.
	rcpt := newReceiptBuilder("install", name, entry.Version)
	rcpt.snapshotAllClients(name)
	if canonPath, cerr := canonical.FilePath(); cerr == nil {
		rcpt.noteCanonical(canonPath)
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
	} else {
		rcpt.touchCanonical()
	}

	// Write client config.
	updated, skipped, err := install.WriteClientConfigs(name, clientCfg, clientIDs)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Config write failed:"), err)
		rcpt.addError("config write failed: %v", err)
	}
	printClientConfigResults(updated, skipped)
	for _, c := range updated {
		rcpt.touch(c.Path, c.Name, "")
		if rcpt.serverWasPresent(c.Path, name) {
			rcpt.server(c.Name, name, "replaced")
		} else {
			rcpt.server(c.Name, name, "added")
		}
	}

	progressf("\n%s  %s\n", ui.Success.Render("✓ Installed (frozen):"), fmt.Sprintf("%s@%s (kind %s)", name, entry.Version, kind))

	// W1.2: deterministic receipt of everything this frozen install changed.
	rcpt.emit()

	if kind == install.KindLocalHTTP {
		ensureDaemonRunning(name)
	}
}

// classifyInstallManifest is the CLI entry to the shared F1–F7 classifier.
func classifyInstallManifest(m api.Manifest) install.Kind {
	return install.ClassifyManifest(m)
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

// printClientConfigResults prints a success check only for clients that
// were actually written. Skips are never shown as ✓. Progress goes to
// stderr under JSON mode to keep stdout a pure receipt document.
func printClientConfigResults(updated []clientconfig.Client, skipped []clientconfig.SkippedClient) {
	for _, c := range updated {
		progressf("  %s  %s\n", ui.Success.Render("✓"), c.Name)
	}
	for _, s := range skipped {
		progressf("  %s  %s  skipped: %s\n", ui.Muted.Render("—"), s.Client.Name, s.Reason)
	}
}

// writeDepClientConfigs writes one dependency's MCP client configs and
// records every write on the receipt. The dep's server-entry presence is
// snapshotted just before the write (the dep-level equivalent of the
// primary's pre-run snapshotAllClients), so the action is "added" when
// this run introduces the dep and "replaced" when the config already
// referenced it. Dep write failures are recorded on the receipt (status
// "partial") and do not abort the remaining dependencies. It returns the
// clients the dep config was actually written to (for the lockfile's
// Clients record).
func writeDepClientConfigs(rcpt *receiptBuilder, depName string, depCfg clientconfig.ServerConfig, clientIDs []string) []clientconfig.Client {
	rcpt.snapshotAllClients(depName)
	updated, skipped, err := install.WriteClientConfigs(depName, depCfg, clientIDs)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Config write failed:"), err)
		rcpt.addError("dependency %s config write failed: %v", depName, err)
	}
	printClientConfigResults(updated, skipped)
	for _, c := range updated {
		rcpt.touch(c.Path, c.Name, "")
		if rcpt.serverWasPresent(c.Path, depName) {
			rcpt.server(c.Name, depName, "replaced")
		} else {
			rcpt.server(c.Name, depName, "added")
		}
	}
	return updated
}

// writtenClientIDs extracts the deduped client IDs from the clients a
// config write actually updated (the lockfile Clients record).
func writtenClientIDs(updated []clientconfig.Client) []string {
	if len(updated) == 0 {
		return nil
	}
	ids := make([]string, 0, len(updated))
	seen := make(map[string]bool, len(updated))
	for _, c := range updated {
		id := string(c.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
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
