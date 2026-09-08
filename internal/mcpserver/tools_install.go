package mcpserver

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/canonical"
	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/install"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/resolver"
	"github.com/Wpnx330/pharos-cli/internal/semver"
)

// installIdleTimeoutDefault matches `pharos install`'s flag default: the
// canonical-config idle timeout (minutes) recorded for MCP-installed
// servers. There is no flag surface on serve; parity with the CLI is the
// contract.
const installIdleTimeoutDefault = 60

// installDoc is the install tool's success payload.
type installDoc struct {
	Name             string   `json:"name"`
	Version          string   `json:"version"`
	Transport        string   `json:"transport"`
	Kind             string   `json:"kind"`
	AlreadyInstalled bool     `json:"already_installed"`
	ClientsWritten   []string `json:"clients_written"`
	Dependencies     []string `json:"dependencies,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`
	Lockfile         string   `json:"lockfile"`
}

// runInstall is the opt-in install tool. Flag contract: the tool exists
// only when `pharos serve --allow-install` is set. Without the flag the
// tool is omitted from tools/list, and a direct tools/call answers an
// isError result that names the flag (clients may have cached tools from
// a differently-configured server — the answer must stay honest).
//
// When enabled it delegates to the same data-layer pipeline as
// `pharos install` (internal/install + internal/canonical + lockfile +
// resolver): semver resolution, kind classification, PHAROS_REMOTE_ONLY
// enforcement, integrity-verified tarball install, canonical config,
// detected-MCP-client config writes, provenance-tracked lockfile update,
// and recursive dependency resolution. No receipts, no profile attach,
// no daemon start — those are CLI-session concerns.
func (s *Server) runInstall(args json.RawMessage) (any, error) {
	if !s.cfg.AllowInstall {
		return nil, fmt.Errorf("the install tool is disabled on this server: restart `pharos serve` with --allow-install to enable installs")
	}
	var a struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(a.Name)
	if name == "" {
		return nil, badParams("install requires a package name")
	}
	client, err := s.registryClient()
	if err != nil {
		return nil, err
	}
	lockPath, err := lockfile.DefaultPath()
	if err != nil {
		return nil, fmt.Errorf("resolve lockfile path: %w", err)
	}
	storeDir, err := install.DefaultStoreDir()
	if err != nil {
		return nil, fmt.Errorf("resolve store directory: %w", err)
	}

	// Resolve the version the same way `pharos install` does.
	pkg, err := client.GetPackage(name)
	if err != nil {
		return nil, fmt.Errorf("fetch package info failed: %w", err)
	}
	available := pkg.VersionStrings()
	if len(available) == 0 {
		return nil, fmt.Errorf("package %q has no published versions", name)
	}
	resolved, err := semver.Resolve(strings.TrimSpace(a.Version), available, pkg.DistTags)
	if err != nil {
		return nil, fmt.Errorf("version resolution failed: %w", err)
	}
	vd := pkg.FindVersion(resolved)
	if vd == nil {
		return nil, fmt.Errorf("resolved version %s not found in packument", resolved)
	}
	manifest := vd.Manifest

	kind := install.ClassifyManifest(manifest)
	if kind == install.KindNone {
		return nil, fmt.Errorf("package %s@%s is not installable: no endpoint, command, bin, or runtime+package", name, resolved)
	}
	if install.RemoteOnlyRejected(kind, install.EnvRemoteOnly()) {
		return nil, fmt.Errorf("PHAROS_REMOTE_ONLY=true refuses kind %s local install of %s@%s", kind, name, resolved)
	}

	transport := strings.ToLower(strings.TrimSpace(manifest.Transport))
	if transport == "" {
		transport = "stdio"
	}
	resolvedURL := client.TarballURL(name, resolved)
	if kind == install.KindRemoteHTTP {
		resolvedURL = manifest.Endpoint
	}

	mgr := install.NewManager(storeDir)
	warnings := []string{}
	already := mgr.IsInstalled(name, resolved)
	result := &install.InstallResult{
		Name:      name,
		Version:   resolved,
		Transport: transport,
		Kind:      kind,
		Endpoint:  manifest.Endpoint,
	}
	if !already {
		result, err = mgr.InstallByKind(install.InstallOptions{
			Name:              name,
			Version:           resolved,
			TarballURL:        client.TarballURL(name, resolved),
			ExpectedIntegrity: manifest.Integrity,
			Manifest:          manifest,
		})
		if err != nil {
			return nil, fmt.Errorf("install failed: %w", err)
		}
		if result.Transport != "" {
			transport = result.Transport
		}
	}

	// Canonical config (~/.pharos/mcp.json) — same shape as the CLI.
	serverCfg := install.BuildServerConfig(manifest, storeDir)
	canonSrv := canonical.Server{
		Transport:   transport,
		Enabled:     true,
		IdleTimeout: installIdleTimeoutDefault,
		Package: canonical.PackageInfo{
			Name:      name,
			Version:   resolved,
			Integrity: manifest.Integrity,
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
		canonSrv.Cwd = filepath.Join(storeDir, name, resolved)
	}
	if err := canonical.AddServer(name, canonSrv); err != nil {
		warnings = append(warnings, "canonical config write failed: "+err.Error())
	}

	// MCP client configs — auto mode, same as `pharos install` without
	// --client (every detected client).
	clientCfg := install.BuildClientConfig(manifest, storeDir)
	updated, skipped, err := install.WriteClientConfigs(name, clientCfg, nil)
	if err != nil {
		warnings = append(warnings, "client config write failed: "+err.Error())
	}
	clientsWritten := clientIDsOf(updated)
	for _, sk := range skipped {
		warnings = append(warnings, fmt.Sprintf("client %s skipped: %s", sk.Client.Name, sk.Reason))
	}

	// Lockfile update with provenance. InstalledVia is honest about the
	// surface: these installs did not come from the interactive CLI.
	origin := &lockfile.OriginInfo{
		Kind:         lockfile.OriginKindRegistry,
		Ref:          name + "@" + resolved,
		InstalledVia: "pharos serve",
	}
	if err := install.UpdateLockfile(lockPath, result, resolvedURL, clientsWritten, origin); err != nil {
		warnings = append(warnings, "lockfile update failed: "+err.Error())
	}

	// Install-event telemetry for remote transports (best-effort, same
	// policy as the CLI: stdio packages are counted server-side).
	if transport != "stdio" {
		_ = client.ReportInstallEvent(name, resolved)
	}

	deps := s.installDependencies(client, mgr, storeDir, lockPath, manifest, name, warnings)

	return installDoc{
		Name:             name,
		Version:          resolved,
		Transport:        transport,
		Kind:             kind.String(),
		AlreadyInstalled: already,
		ClientsWritten:   clientsWritten,
		Dependencies:     deps,
		Warnings:         warnings,
		Lockfile:         lockPath,
	}, nil
}

// installDependencies mirrors the CLI's dependency stage: resolve declared
// deps, install missing ones, write their client configs and lockfile
// rows. Every per-dep failure degrades to a warning — the primary
// package is installed either way — exactly like `pharos install`.
func (s *Server) installDependencies(client *api.Client, mgr *install.Manager, storeDir, lockPath string, manifest api.Manifest, name string, warnings []string) []string {
	if len(manifest.Dependencies) == 0 {
		return nil
	}
	r := resolver.New(client)
	depResult, err := r.ResolveAll(manifest.Dependencies)
	if err != nil {
		warnings = append(warnings, "dependency resolution failed: "+err.Error())
		return nil
	}
	for _, c := range depResult.Conflicts {
		warnings = append(warnings, fmt.Sprintf("version conflict %s: %s vs %s → using %s", c.Name, c.Existing, c.Requested, c.Resolution))
	}
	for _, cyc := range depResult.Circular {
		warnings = append(warnings, "circular dependency skipped: "+cyc)
	}

	depNames := make([]string, 0, len(depResult.Flat))
	for depName := range depResult.Flat {
		depNames = append(depNames, depName)
	}
	sort.Strings(depNames) // deterministic order

	deps := make([]string, 0, len(depNames))
	for _, depName := range depNames {
		depVersion := depResult.Flat[depName]
		if depName == name {
			continue
		}
		depPkg, err := client.GetPackage(depName)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("dependency %s: fetch failed: %v", depName, err))
			continue
		}
		depVD := depPkg.FindVersion(depVersion)
		if depVD == nil {
			warnings = append(warnings, fmt.Sprintf("dependency %s@%s not found in registry", depName, depVersion))
			continue
		}
		depKind := install.ClassifyManifest(depVD.Manifest)
		if depKind == install.KindNone {
			warnings = append(warnings, fmt.Sprintf("dependency %s@%s is not installable (no endpoint, command, bin, or runtime+package)", depName, depVersion))
			continue
		}
		if install.RemoteOnlyRejected(depKind, install.EnvRemoteOnly()) {
			warnings = append(warnings, fmt.Sprintf("dependency %s@%s skipped under PHAROS_REMOTE_ONLY", depName, depVersion))
			continue
		}
		depTransport := strings.ToLower(strings.TrimSpace(depVD.Manifest.Transport))
		if depTransport == "" {
			depTransport = "stdio"
		}
		depURL := client.TarballURL(depName, depVersion)
		if depKind == install.KindRemoteHTTP {
			depURL = depVD.Manifest.Endpoint
		}
		if !mgr.IsInstalled(depName, depVersion) {
			depRes, err := mgr.InstallByKind(install.InstallOptions{
				Name:              depName,
				Version:           depVersion,
				TarballURL:        client.TarballURL(depName, depVersion),
				ExpectedIntegrity: depVD.Manifest.Integrity,
				Manifest:          depVD.Manifest,
			})
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("dependency %s@%s install failed: %v", depName, depVersion, err))
				continue
			}
			if depRes != nil && depRes.Transport != "" {
				depTransport = depRes.Transport
			}
		}
		depCfg := install.BuildClientConfig(depVD.Manifest, storeDir)
		depUpdated, depSkipped, err := install.WriteClientConfigs(depName, depCfg, nil)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("dependency %s client config write failed: %v", depName, err))
		}
		for _, sk := range depSkipped {
			warnings = append(warnings, fmt.Sprintf("dependency %s client %s skipped: %s", depName, sk.Client.Name, sk.Reason))
		}
		depOrigin := &lockfile.OriginInfo{
			Kind:         lockfile.OriginKindRegistry,
			Ref:          depName + "@" + depVersion,
			InstalledVia: "pharos serve",
		}
		if err := install.UpdateLockfile(lockPath, &install.InstallResult{
			Name:      depName,
			Version:   depVersion,
			Transport: depTransport,
			Kind:      depKind,
		}, depURL, clientIDsOf(depUpdated), depOrigin); err != nil {
			warnings = append(warnings, fmt.Sprintf("dependency %s lockfile update failed: %v", depName, err))
		}
		deps = append(deps, depName+"@"+depVersion)
	}
	return deps
}

// clientIDsOf extracts the deduped client IDs from a config-write result
// (the lockfile Clients record).
func clientIDsOf(updated []clientconfig.Client) []string {
	if len(updated) == 0 {
		return []string{}
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
