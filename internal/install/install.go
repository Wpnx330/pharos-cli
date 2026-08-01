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
	"strings"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
)

// InstalledPackage records metadata about a locally installed package.
type InstalledPackage struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Transport string `json:"transport"`
	Installed string `json:"installed"`
	Location  string `json:"location"`
	Integrity string `json:"integrity"`
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
	}, nil
}

// InstallHTTP installs an http/sse transport package (no download needed).
func (m *Manager) InstallHTTP(name, version string) (*InstallResult, error) {
	pkg := &InstalledPackage{
		Name:      name,
		Version:   version,
		Transport: "http",
		Installed: time.Now().UTC().Format(time.RFC3339),
		Location:  "",
	}
	if err := m.saveMetadata(name, version, pkg); err != nil {
		return nil, err
	}
	return &InstallResult{
		Name:      name,
		Version:   version,
		Transport: "http",
	}, nil
}

// BuildServerConfig constructs a clientconfig.ServerConfig from the
// manifest, tailored to the transport type.
func BuildServerConfig(manifest api.Manifest, storeDir string) clientconfig.ServerConfig {
	transport := normalizeTransport(manifest.Transport)
	if transport == "http" || transport == "sse" {
		return clientconfig.ServerConfig{
			URL:  manifest.Endpoint,
			Type: transport,
		}
	}

	// stdio: build command/args based on runtime hint.
	cfg := clientconfig.ServerConfig{
		Env: manifest.Env,
	}
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
	default:
		// Fallback: assume npx-style.
		cfg.Command = "npx"
		cfg.Args = append([]string{"-y", pkg}, manifest.Args...)
	}
	return cfg
}

func normalizeTransport(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if t == "sse" || t == "https" {
		return "sse"
	}
	if t == "http" {
		return "http"
	}
	return "stdio"
}

// WriteClientConfigs detects installed MCP clients and writes the server
// config entry to each. If clientID is non-empty, only that client is
// written. Returns the list of clients that were updated.
func WriteClientConfigs(name string, serverCfg clientconfig.ServerConfig, clientID string) ([]clientconfig.Client, error) {
	var targets []clientconfig.Client

	if clientID != "" {
		c := clientconfig.DetectByID(clientconfig.ClientID(clientID))
		if c == nil {
			return nil, fmt.Errorf("client %q not detected on this system", clientID)
		}
		targets = append(targets, *c)
	} else {
		targets = clientconfig.Detect()
		if len(targets) == 0 {
			return nil, fmt.Errorf("no MCP clients detected; use --client to specify one")
		}
	}

	var updated []clientconfig.Client
	for _, c := range targets {
		if err := clientconfig.MergeServer(c, name, serverCfg); err != nil {
			return updated, fmt.Errorf("failed to write config for %s: %w", c.Name, err)
		}
		updated = append(updated, c)
	}
	return updated, nil
}

// UpdateLockfile writes/updates the lockfile with the install result.
func UpdateLockfile(lockPath string, result *InstallResult, resolvedURL string) error {
	lf, err := lockfile.Load(lockPath)
	if err != nil {
		return err
	}
	lf.Set(result.Name, lockfile.ServerEntry{
		Version:     result.Version,
		Integrity:   result.Integrity,
		Transport:   result.Transport,
		Resolved:    resolvedURL,
		InstalledAt: time.Now().UTC(),
	})
	return lf.Save(lockPath)
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
func (m *Manager) List() ([]InstalledPackage, error) {
	entries, err := os.ReadDir(m.StoreDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var pkgs []InstalledPackage
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Each package dir contains version subdirs.
		pkgName := entry.Name()
		versions, err := os.ReadDir(m.packagePath(pkgName))
		if err != nil {
			continue
		}
		for _, vd := range versions {
			if !vd.IsDir() {
				continue
			}
			metaPath := m.metadataPath(pkgName, vd.Name())
			data, err := os.ReadFile(metaPath)
			if err != nil {
				continue
			}
			var pkg InstalledPackage
			if err := json.Unmarshal(data, &pkg); err != nil {
				continue
			}
			pkgs = append(pkgs, pkg)
		}
	}
	return pkgs, nil
}
