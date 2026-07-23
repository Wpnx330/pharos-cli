// Package install handles downloading and extracting package tarballs
// into the local packages directory (~/.pharos/packages/).
package install

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// InstalledPackage records metadata about a locally installed package.
type InstalledPackage struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Installed string `json:"installed"`
	Location  string `json:"location"`
}

// Manager handles package installation and listing.
type Manager struct {
	PackagesDir string
	HTTPClient  *http.Client
}

// NewManager creates a Manager that installs packages into the given
// directory.
func NewManager(packagesDir string) *Manager {
	return &Manager{
		PackagesDir: packagesDir,
		HTTPClient:  &http.Client{Timeout: 60 * time.Second},
	}
}

// IsInstalled returns true if a package with the given name is already
// installed locally.
func (m *Manager) IsInstalled(name string) bool {
	metadata := m.metadataPath(name)
	_, err := os.Stat(metadata)
	return err == nil
}

// DownloadTarball downloads a tarball from the given URL and returns
// the bytes.
func (m *Manager) DownloadTarball(url string) ([]byte, error) {
	resp, err := m.HTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
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

// Install downloads and extracts a package, recording metadata.
func (m *Manager) Install(name, version, tarballURL string) error {
	if m.IsInstalled(name) {
		return fmt.Errorf("package %s is already installed", name)
	}
	data, err := m.DownloadTarball(tarballURL)
	if err != nil {
		return err
	}
	dest := m.packagePath(name)
	if err := Extract(data, dest); err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	pkg := InstalledPackage{
		Name:      name,
		Version:   version,
		Installed: time.Now().UTC().Format(time.RFC3339),
		Location:  dest,
	}
	return m.saveMetadata(name, &pkg)
}

// metadataPath returns the path to a package's install metadata file.
func (m *Manager) metadataPath(name string) string {
	return filepath.Join(m.PackagesDir, name, ".pharos-installed.json")
}

// packagePath returns the installation directory for a package.
func (m *Manager) packagePath(name string) string {
	return filepath.Join(m.PackagesDir, name)
}

// saveMetadata writes the installed package metadata to disk.
func (m *Manager) saveMetadata(name string, pkg *InstalledPackage) error {
	path := m.metadataPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// List returns all locally installed packages.
func (m *Manager) List() ([]InstalledPackage, error) {
	entries, err := os.ReadDir(m.PackagesDir)
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
		metaPath := m.metadataPath(entry.Name())
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
	return pkgs, nil
}
