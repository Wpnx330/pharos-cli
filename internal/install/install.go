// Package install handles downloading and extracting package tarballs
// into the local Pharos store (~/.pharos/store/{name}/{version}/) with
// transport-aware installation, integrity verification, and client
// config writing.
package install

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/runtime"
)

// InstalledPackage records metadata about a locally installed package.
// Kind/Endpoint/Command/Bin/Runtime/Package are persisted so list/start/remove
// (T1b) can act without re-fetching the registry.
type InstalledPackage struct {
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	Transport string   `json:"transport"`
	Installed string   `json:"installed"`
	Location  string   `json:"location"`
	Integrity string   `json:"integrity"`
	Kind      Kind     `json:"kind,omitempty"`
	Endpoint  string   `json:"endpoint,omitempty"`
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	Bin       string   `json:"bin,omitempty"`
	Runtime   string   `json:"runtime,omitempty"`
	Package   string   `json:"package,omitempty"`
}

// Manager handles package installation, the local store, lockfile,
// and client config writing.
type Manager struct {
	StoreDir   string
	HTTPClient *http.Client
}

// NewManager creates a Manager that installs packages into the given
// store directory (typically ~/.pharos/store).
func NewManager(storeDir string) *Manager {
	return &Manager{
		StoreDir:   storeDir,
		HTTPClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// DefaultStoreDir returns ~/.pharos/store.
func DefaultStoreDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pharos", "store"), nil
}

// IsInstalled returns true if a specific name@version is already in the store.
func (m *Manager) IsInstalled(name, version string) bool {
	metadata := m.metadataPath(name, version)
	_, err := os.Stat(metadata)
	return err == nil
}

// IsAnyVersionInstalled returns true if any version of the package is installed.
func (m *Manager) IsAnyVersionInstalled(name string) bool {
	dir := m.packagePath(name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(m.packagePath(name), e.Name(), ".pharos-installed.json")); err == nil {
				return true
			}
		}
	}
	return false
}

// DownloadTarball downloads a tarball from the given URL and returns
// the bytes. It follows redirects (registry returns a presigned R2 URL).
func (m *Manager) DownloadTarball(url string) ([]byte, error) {
	resp, err := m.HTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download failed: HTTP %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(resp.Body)
}

// ComputeIntegrity computes the sha512 integrity hash of data in the
// format "sha512-<base64>".
func ComputeIntegrity(data []byte) string {
	h := sha512.Sum512(data)
	return "sha512-" + base64.StdEncoding.EncodeToString(h[:])
}

// VerifyIntegrity compares the computed hash of data against the expected
// integrity string. Returns nil if they match.
func VerifyIntegrity(data []byte, expected string) error {
	if expected == "" {
		return nil // no integrity to check (registry may not provide one yet)
	}
	actual := ComputeIntegrity(data)
	if actual != expected {
		return fmt.Errorf("integrity mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

// Extract extracts a gzip+tar byte stream into the given destination
// directory.
func Extract(data []byte, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	gzr, err := gzip.NewReader(strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("gunzip: %w", err)
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}
		// Prevent path traversal
		target := filepath.Join(dest, hdr.Name)
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) && target != filepath.Clean(dest) {
			return fmt.Errorf("path traversal detected: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			// Skip symlinks for security
		}
	}
	return nil
}

// InstallResult holds the outcome of an installation.
type InstallResult struct {
	Name      string
	Version   string
	Transport string
	Integrity string
	Location  string
	Kind      Kind
	Endpoint  string
	Command   string
	Bin       string
	Runtime   string
	Package   string
}

// InstallOptions is the kind-aware install request.
type InstallOptions struct {
	Name              string
	Version           string
	TarballURL        string
	ExpectedIntegrity string
	Manifest          api.Manifest
}

// InstallStdio downloads, verifies, and extracts a stdio transport package.
func (m *Manager) InstallStdio(name, version, tarballURL, expectedIntegrity string) (*InstallResult, error) {
	data, err := m.DownloadTarball(tarballURL)
	if err != nil {
		return nil, err
	}
	if err := VerifyIntegrity(data, expectedIntegrity); err != nil {
		return nil, fmt.Errorf("integrity verification failed: %w", err)
	}
	integrity := ComputeIntegrity(data)
	if expectedIntegrity != "" {
		integrity = expectedIntegrity
	}

	dest := m.versionPath(name, version)
	// Clean any previous install of this version.
	os.RemoveAll(dest)
	if err := Extract(data, dest); err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}

	pkg := &InstalledPackage{
		Name:      name,
		Version:   version,
		Transport: "stdio",
		Installed: time.Now().UTC().Format(time.RFC3339),
		Location:  dest,
		Integrity: integrity,
		Kind:      KindStdio,
	}
	if err := m.saveMetadata(name, version, pkg); err != nil {
		return nil, err
	}
	return &InstallResult{
		Name:      name,
		Version:   version,
		Transport: "stdio",
		Integrity: integrity,
		Location:  dest,
		Kind:      KindStdio,
	}, nil
}

// InstallHTTP installs an http/sse transport package (no download needed).
func (m *Manager) InstallHTTP(name, version string) (*InstallResult, error) {
	return m.InstallHTTPBookmark(name, version, "http", "")
}

// InstallHTTPBookmark writes a kind-1 remote bookmark. No tarball is fetched.
func (m *Manager) InstallHTTPBookmark(name, version, transport, endpoint string) (*InstallResult, error) {
	if err := ValidateInstallIdentity(name, version); err != nil {
		return nil, err
	}
	if transport == "" {
		transport = "http"
	}
	if !IsHTTPFamily(transport) {
		transport = "http"
	}
	pkg := &InstalledPackage{
		Name:      name,
		Version:   version,
		Transport: transport,
		Installed: time.Now().UTC().Format(time.RFC3339),
		Location:  "",
		Kind:      KindRemoteHTTP,
		Endpoint:  endpoint,
	}
	if err := m.saveMetadata(name, version, pkg); err != nil {
		return nil, err
	}
	return &InstallResult{
		Name:      name,
		Version:   version,
		Transport: transport,
		Kind:      KindRemoteHTTP,
		Endpoint:  endpoint,
	}, nil
}

// InstallByKind classifies the manifest and installs kind 1, 2, or 3.
// Kind 1 never fetches a tarball. Kind 2 downloads only if the tarball URL
// returns 200. Kind 3 uses InstallStdio when a tarball is present, otherwise
// persists command / runtime+package (mcp.io, no /v1/tarballs required).
func (m *Manager) InstallByKind(opts InstallOptions) (*InstallResult, error) {
	if err := ValidateInstallIdentity(opts.Name, opts.Version); err != nil {
		return nil, err
	}
	kind := ClassifyManifest(opts.Manifest)
	if kind == KindNone {
		return nil, fmt.Errorf("package %s@%s is not installable: no endpoint, command, bin, or runtime+package", opts.Name, opts.Version)
	}
	if RemoteOnlyRejected(kind, EnvRemoteOnly()) {
		return nil, fmt.Errorf("PHAROS_REMOTE_ONLY: refusing local install of %s@%s (kind %s); only kind 1 remote HTTP is allowed", opts.Name, opts.Version, kind)
	}

	switch kind {
	case KindRemoteHTTP:
		transport := opts.Manifest.Transport
		if transport == "" {
			transport = "http"
		}
		return m.InstallHTTPBookmark(opts.Name, opts.Version, transport, opts.Manifest.Endpoint)
	case KindLocalHTTP:
		return m.installLocalHTTP(opts)
	case KindStdio:
		return m.installStdioKind(opts)
	default:
		return nil, fmt.Errorf("package %s@%s is not installable", opts.Name, opts.Version)
	}
}

func (m *Manager) installLocalHTTP(opts InstallOptions) (*InstallResult, error) {
	transport := opts.Manifest.Transport
	if transport == "" || !IsHTTPFamily(transport) {
		transport = "http"
	}

	if opts.TarballURL != "" {
		data, err := m.tryDownloadTarball(opts.TarballURL)
		if err != nil {
			return nil, err
		}
		if data != nil {
			if err := VerifyIntegrity(data, opts.ExpectedIntegrity); err != nil {
				return nil, fmt.Errorf("integrity verification failed: %w", err)
			}
			integrity := ComputeIntegrity(data)
			if opts.ExpectedIntegrity != "" {
				integrity = opts.ExpectedIntegrity
			}
			dest := m.versionPath(opts.Name, opts.Version)
			os.RemoveAll(dest)
			if err := Extract(data, dest); err != nil {
				return nil, fmt.Errorf("extract: %w", err)
			}
			pkg := applyLaunchMetadata(&InstalledPackage{
				Name:      opts.Name,
				Version:   opts.Version,
				Transport: transport,
				Installed: time.Now().UTC().Format(time.RFC3339),
				Location:  dest,
				Integrity: integrity,
				Kind:      KindLocalHTTP,
			}, opts.Manifest)
			persistDerivedLaunch(pkg, opts.Manifest, m.StoreDir)
			if err := m.saveMetadata(opts.Name, opts.Version, pkg); err != nil {
				return nil, err
			}
			return resultFromPackage(pkg), nil
		}
	}

	// No tarball (missing URL or non-200). Persist launch line; do not 404-fail.
	pkg := applyLaunchMetadata(&InstalledPackage{
		Name:      opts.Name,
		Version:   opts.Version,
		Transport: transport,
		Installed: time.Now().UTC().Format(time.RFC3339),
		Kind:      KindLocalHTTP,
	}, opts.Manifest)
	persistDerivedLaunch(pkg, opts.Manifest, m.StoreDir)
	if err := m.saveMetadata(opts.Name, opts.Version, pkg); err != nil {
		return nil, err
	}
	return resultFromPackage(pkg), nil
}

func (m *Manager) installStdioKind(opts InstallOptions) (*InstallResult, error) {
	if opts.TarballURL != "" {
		data, err := m.tryDownloadTarball(opts.TarballURL)
		if err != nil {
			return nil, err
		}
		if data != nil {
			res, err := m.installStdioBytes(opts.Name, opts.Version, data, opts.ExpectedIntegrity)
			if err != nil {
				return nil, err
			}
			pkg := applyLaunchMetadata(&InstalledPackage{
				Name:      res.Name,
				Version:   res.Version,
				Transport: "stdio",
				Installed: time.Now().UTC().Format(time.RFC3339),
				Location:  res.Location,
				Integrity: res.Integrity,
				Kind:      KindStdio,
			}, opts.Manifest)
			persistDerivedLaunch(pkg, opts.Manifest, m.StoreDir)
			if err := m.saveMetadata(opts.Name, opts.Version, pkg); err != nil {
				return nil, err
			}
			return resultFromPackage(pkg), nil
		}
	}

	// Kind 3 mcp.io / npx: persist command or runtime+package. No tarball required.
	pkg := applyLaunchMetadata(&InstalledPackage{
		Name:      opts.Name,
		Version:   opts.Version,
		Transport: "stdio",
		Installed: time.Now().UTC().Format(time.RFC3339),
		Kind:      KindStdio,
	}, opts.Manifest)
	persistDerivedLaunch(pkg, opts.Manifest, m.StoreDir)
	if err := m.saveMetadata(opts.Name, opts.Version, pkg); err != nil {
		return nil, err
	}
	return resultFromPackage(pkg), nil
}

func (m *Manager) installStdioBytes(name, version string, data []byte, expectedIntegrity string) (*InstallResult, error) {
	if err := VerifyIntegrity(data, expectedIntegrity); err != nil {
		return nil, fmt.Errorf("integrity verification failed: %w", err)
	}
	integrity := ComputeIntegrity(data)
	if expectedIntegrity != "" {
		integrity = expectedIntegrity
	}
	dest := m.versionPath(name, version)
	os.RemoveAll(dest)
	if err := Extract(data, dest); err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}
	return &InstallResult{
		Name:      name,
		Version:   version,
		Transport: "stdio",
		Integrity: integrity,
		Location:  dest,
		Kind:      KindStdio,
	}, nil
}

// tryDownloadTarball returns (nil, nil) when the URL is missing or the
// registry answers with a non-200 (including 404). Network errors fail.
func (m *Manager) tryDownloadTarball(rawURL string) ([]byte, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, nil
	}
	resp, err := m.HTTPClient.Get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return io.ReadAll(resp.Body)
	}
	io.Copy(io.Discard, resp.Body)
	return nil, nil
}

func applyLaunchMetadata(pkg *InstalledPackage, m api.Manifest) *InstalledPackage {
	pkg.Endpoint = m.Endpoint
	pkg.Command = m.Command
	pkg.Args = m.Args
	pkg.Bin = m.Bin
	pkg.Runtime = m.Runtime
	pkg.Package = m.Package
	return pkg
}

func persistDerivedLaunch(pkg *InstalledPackage, m api.Manifest, storeDir string) {
	if strings.TrimSpace(pkg.Command) != "" {
		return
	}
	cfg := BuildServerConfig(m, storeDir)
	if cfg.Command == "" {
		return
	}
	pkg.Command = cfg.Command
	if len(pkg.Args) == 0 {
		pkg.Args = cfg.Args
	}
}

func resultFromPackage(pkg *InstalledPackage) *InstallResult {
	return &InstallResult{
		Name:      pkg.Name,
		Version:   pkg.Version,
		Transport: pkg.Transport,
		Integrity: pkg.Integrity,
		Location:  pkg.Location,
		Kind:      pkg.Kind,
		Endpoint:  pkg.Endpoint,
		Command:   pkg.Command,
		Bin:       pkg.Bin,
		Runtime:   pkg.Runtime,
		Package:   pkg.Package,
	}
}

// ValidateInstallIdentity rejects empty or path-traversing name/version
// before they are joined into the store path.
func ValidateInstallIdentity(name, version string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("package name is required")
	}
	if strings.TrimSpace(version) == "" {
		return fmt.Errorf("package version is required")
	}
	if !safeStoreSegment(name) {
		return fmt.Errorf("invalid package name %q", name)
	}
	if !safeStoreSegment(version) {
		return fmt.Errorf("invalid package version %q", version)
	}
	return nil
}

func safeStoreSegment(s string) bool {
	if s == "." || s == ".." {
		return false
	}
	if strings.Contains(s, "\x00") {
		return false
	}
	if strings.Contains(s, "..") {
		return false
	}
	cleaned := filepath.Clean(s)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return false
	}
	if filepath.IsAbs(cleaned) {
		return false
	}
	return true
}

// BuildServerConfig constructs a clientconfig.ServerConfig from the
// manifest, tailored to the transport type.
func BuildServerConfig(manifest api.Manifest, storeDir string) clientconfig.ServerConfig {
	transport := normalizeTransport(manifest.Transport)
	// Kind 1: publisher URL. Kind 2 has no publisher endpoint — persist launch line.
	if (transport == "http" || transport == "sse") && IsHTTPEndpoint(manifest.Endpoint) {
		return clientconfig.ServerConfig{
			URL:  strings.TrimSpace(manifest.Endpoint),
			Type: transport,
			Env:  manifest.Env,
		}
	}

	// stdio or kind-2 local HTTP: build command/args from command, bin, or runtime.
	cfg := clientconfig.ServerConfig{
		Env:  manifest.Env,
		Type: transport,
	}

	// If the manifest has an explicit Command, use it directly.
	// This handles Python servers ("python -m src.server"), custom binaries, etc.
	if manifest.Command != "" {
		parts := strings.Fields(manifest.Command)
		if len(parts) > 0 {
			cfg.Command = parts[0]
			cfg.Args = append(parts[1:], manifest.Args...)
		}
		return cfg
	}

	// If Bin is set, persist it as argv before the runtime switch.
	// A known runtime (python/npx/...) must not replace "python server.py"
	// with "python3 <package>". runtime=binary still uses the store path below.
	if strings.TrimSpace(manifest.Bin) != "" && !strings.EqualFold(strings.TrimSpace(manifest.Runtime), "binary") {
		parts := strings.Fields(manifest.Bin)
		if len(parts) > 0 {
			cfg.Command = parts[0]
			cfg.Args = append(parts[1:], manifest.Args...)
		}
		return cfg
	}

	// Fall back to runtime-based command construction.
	runtime := manifest.Runtime
	pkg := manifest.Package
	if pkg == "" {
		pkg = manifest.Name
	}

	switch runtime {
	case "npx":
		cfg.Command = "npx"
		cfg.Args = append([]string{"-y", pkg}, manifest.Args...)
	case "uvx":
		cfg.Command = "uvx"
		cfg.Args = append([]string{pkg}, manifest.Args...)
	case "docker":
		cfg.Command = "docker"
		cfg.Args = append([]string{"run", "-i", "--rm", pkg}, manifest.Args...)
	case "binary":
		// Point to the extracted binary in the store.
		binPath := manifest.Bin
		if binPath == "" {
			binPath = pkg
		}
		// If we have a store dir, point to the extracted location.
		if storeDir != "" {
			cfg.Command = filepath.Join(storeDir, manifest.Name, manifest.Version, binPath)
		} else {
			cfg.Command = binPath
		}
		cfg.Args = manifest.Args
	case "python":
		cfg.Command = "python3"
		cfg.Args = append([]string{pkg}, manifest.Args...)
	default:
		// Fallback: assume npx-style.
		cfg.Command = "npx"
		cfg.Args = append([]string{"-y", pkg}, manifest.Args...)
	}
	return cfg
}

// defaultKind2ClientPort matches list/start/daemon when bin/command has no --port.
const defaultKind2ClientPort = 8765

// BuildClientConfig constructs the MCP client entry for WriteClientConfigs only.
// Kind 1 keeps the publisher URL. Kind 2 is a localhost listen URL with no
// command/args (the daemon spawn line stays in BuildServerConfig). Kind 3 is
// command/args with no URL.
func BuildClientConfig(manifest api.Manifest, storeDir string) clientconfig.ServerConfig {
	switch ClassifyManifest(manifest) {
	case KindLocalHTTP:
		return clientconfig.ServerConfig{
			URL:  fmt.Sprintf("http://127.0.0.1:%d", kind2ListenPort(manifest)),
			Type: kind2ClientType(manifest.Transport),
			Env:  manifest.Env,
		}
	default:
		return BuildServerConfig(manifest, storeDir)
	}
}

// kind2ClientType is the MCP client transport for a kind-2 localhost URL.
// http-sse / http+sse / streamable-http are streamable HTTP (POST), not
// EventSource. Only an exact "sse" transport writes type: sse.
func kind2ClientType(transport string) string {
	if strings.EqualFold(strings.TrimSpace(transport), "sse") {
		return "sse"
	}
	return "http"
}

func kind2ListenPort(manifest api.Manifest) int {
	candidates := []string{
		manifest.Bin,
		manifest.Command,
		strings.Join(manifest.Args, " "),
	}
	for _, s := range candidates {
		if p := runtime.ExtractPort(s); p > 0 {
			return p
		}
	}
	return defaultKind2ClientPort
}

func normalizeTransport(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	switch t {
	case "sse", "https", "http-sse", "http+sse":
		return "sse"
	case "http", "streamable-http":
		return "http"
	case "stdio", "":
		return "stdio"
	default:
		if IsHTTPFamily(t) {
			return "http"
		}
		return "stdio"
	}
}

// WriteClientConfigs writes the server config to the specified MCP clients.
// If clientIDs is empty, writes to all detected clients (auto mode).
// If clientIDs is non-empty, writes every detected path with that ID
// (Linux home + Windows-via-WSL2). Clients Pharos cannot configure
// (for example Claude Desktop remotes) are returned in skipped, not
// as a write error and not as a successful update.
func WriteClientConfigs(name string, serverCfg clientconfig.ServerConfig, clientIDs []string) (updated []clientconfig.Client, skipped []clientconfig.SkippedClient, err error) {
	targets, err := resolveWriteTargets(clientIDs)
	if err != nil {
		return nil, nil, err
	}

	for _, c := range targets {
		if mergeErr := clientconfig.MergeServer(c, name, serverCfg); mergeErr != nil {
			if clientconfig.IsSkip(mergeErr) {
				skipped = append(skipped, clientconfig.SkippedClient{
					Client: c,
					Reason: mergeErr.Error(),
				})
				continue
			}
			return updated, skipped, fmt.Errorf("failed to write config for %s: %w", c.Name, mergeErr)
		}
		updated = append(updated, c)
	}
	return updated, skipped, nil
}

// resolveWriteTargets expands --client IDs to every matching home-level
// path. Auto mode (empty IDs) is every detected client.
func resolveWriteTargets(clientIDs []string) ([]clientconfig.Client, error) {
	if len(clientIDs) == 0 {
		targets := clientconfig.Detect()
		if len(targets) == 0 {
			return nil, fmt.Errorf("no MCP clients detected; use --client to specify one or --select-clients to pick")
		}
		return targets, nil
	}

	var targets []clientconfig.Client
	for _, id := range clientIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		detected := clientconfig.ClientsByID(clientconfig.ClientID(id))
		if len(detected) > 0 {
			targets = append(targets, detected...)
			continue
		}
		// Not detected. Known built-ins can still be created at the
		// native home path, except Claude Code which is file-if-present.
		var native *clientconfig.Client
		for _, c := range clientconfig.CandidatePaths() {
			if string(c.ID) != id {
				continue
			}
			if native == nil {
				cp := c
				native = &cp
			}
		}
		if native == nil {
			return nil, fmt.Errorf("client %q not recognized; use 'pharos config list-clients' to see available clients", id)
		}
		if native.ID == clientconfig.ClientClaudeCode {
			return nil, fmt.Errorf("client %q not detected (no ~/.claude.json); use 'pharos config list-clients' to see available clients", id)
		}
		targets = append(targets, *native)
	}
	return targets, nil
}

// UpdateLockfile writes/updates the lockfile with the install result.
// clientIDs are the IDs of the clients this install actually wrote the
// server's config to (possibly empty); they are merged into the entry's
// Clients set — a re-install to a different client subset extends the
// record rather than replacing it.
func UpdateLockfile(lockPath string, result *InstallResult, resolvedURL string, clientIDs []string) error {
	lf, err := lockfile.Load(lockPath)
	if err != nil {
		return err
	}
	entry := lockfile.ServerEntry{
		Version:     result.Version,
		Integrity:   result.Integrity,
		Transport:   result.Transport,
		Resolved:    resolvedURL,
		InstalledAt: time.Now().UTC(),
	}
	if prev, ok := lf.Get(result.Name); ok {
		entry.Clients = mergeClientIDs(prev.Clients, clientIDs)
	} else {
		entry.Clients = normalizeClientIDs(clientIDs)
	}
	lf.Set(result.Name, entry)
	return lf.Save(lockPath)
}

// mergeClientIDs unions the previous and new client ID sets, preserving
// the previous order and appending new IDs sorted for determinism.
func mergeClientIDs(prev, next []string) []string {
	if len(prev) == 0 {
		return normalizeClientIDs(next)
	}
	if len(next) == 0 {
		return prev
	}
	seen := make(map[string]bool, len(prev)+len(next))
	merged := make([]string, 0, len(prev)+len(next))
	for _, id := range prev {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		merged = append(merged, id)
	}
	fresh := make([]string, 0, len(next))
	for _, id := range next {
		if id != "" && !seen[id] {
			seen[id] = true
			fresh = append(fresh, id)
		}
	}
	sort.Strings(fresh)
	return append(merged, fresh...)
}

// normalizeClientIDs dedups, drops empties, and sorts a client ID set.
func normalizeClientIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

// --- path helpers ---

func (m *Manager) packagePath(name string) string {
	return filepath.Join(m.StoreDir, name)
}

func (m *Manager) versionPath(name, version string) string {
	return filepath.Join(m.StoreDir, name, version)
}

func (m *Manager) metadataPath(name, version string) string {
	return filepath.Join(m.versionPath(name, version), ".pharos-installed.json")
}

func (m *Manager) saveMetadata(name, version string, pkg *InstalledPackage) error {
	path := m.metadataPath(name, version)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// UpdateTransport updates the transport field in the installed package
// metadata. This is used when an http/sse package is installed via the
// tarball download path (InstallStdio) but needs the actual transport
// recorded correctly.
func (m *Manager) UpdateTransport(name, version, transport string) error {
	metaPath := m.metadataPath(name, version)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return err
	}
	var pkg InstalledPackage
	if err := json.Unmarshal(data, &pkg); err != nil {
		return err
	}
	pkg.Transport = transport
	return m.saveMetadata(name, version, &pkg)
}

// List returns all locally installed packages.
// It walks the store for .pharos-installed.json so scoped names
// (com.invokera/world-time) that occupy extra path segments are found.
func (m *Manager) List() ([]InstalledPackage, error) {
	if _, err := os.Stat(m.StoreDir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var pkgs []InstalledPackage
	err := filepath.WalkDir(m.StoreDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || d.Name() != ".pharos-installed.json" {
			return nil
		}
		rel, relErr := filepath.Rel(m.StoreDir, path)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		var pkg InstalledPackage
		if err := json.Unmarshal(data, &pkg); err != nil {
			return nil
		}
		// Only accept metadata that lives at store/{name}/{version}/.
		// Ignore .pharos-installed.json planted inside extracted trees.
		if pkg.Name == "" || pkg.Version == "" {
			return nil
		}
		want := filepath.Clean(m.metadataPath(pkg.Name, pkg.Version))
		if filepath.Clean(path) != want {
			return nil
		}
		pkgs = append(pkgs, pkg)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pkgs, nil
}
