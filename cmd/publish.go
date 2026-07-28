package cmd

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/config"
	"github.com/Wpnx330/pharos-cli/internal/manifest"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var publishToken string
var publishDryRun bool

var publishCmd = &cobra.Command{
	Use:   "publish [dir]",
	Short: "Publish a package to the PHAROS registry",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		dir := "."
		if len(args) == 1 {
			dir = args[0]
		}

		// Load manifest
		m, err := manifest.Load(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot read manifest:"), err)
			return
		}
		if err := m.Validate(); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Invalid manifest:"), err)
			return
		}

		fmt.Printf("%s  %s@%s\n", ui.Label.Render("Manifest:"), ui.PackageName.Render(m.Name), m.Version)

		if publishDryRun {
			data, _ := json.MarshalIndent(m, "", "  ")
			fmt.Printf("%s\n%s\n", ui.Label.Render("Dry-run validation OK:"), string(data))
			return
		}

		// Determine auth token
		token := publishToken
		if token == "" {
			cfg, err := config.Load()
			if err != nil {
				fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot load config:"), err)
				return
			}
			token = cfg.Token
		}
		if token == "" {
			fmt.Fprintln(os.Stderr, ui.Error.Render("No auth token. Use --token or run 'pharos login'"))
			return
		}

		// Package the tarball
		tarballName := fmt.Sprintf("%s-%s.tgz", m.Name, m.Version)
		tarballPath := filepath.Join(dir, tarballName)

		fmt.Printf("%s  %s\n", ui.Label.Render("Packaging..."), tarballName)
		filesToPack := determinePackFiles(dir, m)
		if err := createTarball(tarballPath, dir, filesToPack); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Packaging failed:"), err)
			return
		}

		// Read the tarball bytes
		tarballBytes, err := os.ReadFile(tarballPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot read tarball:"), err)
			return
		}
		fmt.Printf("%s  %s\n", ui.Label.Render("Tarball size:"), formatSize(int64(len(tarballBytes))))

		cfg, _ := config.Load()
		client := api.New(cfg.Registry, token)

		// Phase 0: ensure the package exists in the registry.
		// First-time publish requires creating the package before we can
		// push a version. We use the authenticated user's namespace.
		user, err := client.GetCurrentUser()
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot determine user namespace:"), err)
			return
		}
		fmt.Printf("%s  %s\n", ui.Label.Render("Creating package..."), m.Name)
		if err := client.CreatePackage(&api.CreatePackageRequest{
			Name:        m.Name,
			Namespace:   user.Username,
			Description: m.Description,
		}); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Package creation failed:"), err)
			return
		}

		// Phase 1: create upload session
		fmt.Printf("%s\n", ui.Label.Render("Uploading..."))
		uploadResp, err := client.Upload(m.Name, m.Version, int64(len(tarballBytes)))
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Upload session failed:"), err)
			return
		}

		// Phase 2: PUT tarball bytes to presigned URL
		if uploadResp.URL != "" {
			if err := client.UploadToPresigned(uploadResp.URL, tarballBytes); err != nil {
				fmt.Fprintln(os.Stderr, ui.Error.Render("Tarball upload failed:"), err)
				return
			}
		}

		// Phase 3: publish the package metadata
		// The registry expects: version, manifest (raw JSON), blobRef, and
		// optional artifact metadata (type, size, integrity hash).
		manifestJSON, err := json.Marshal(m)
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot marshal manifest:"), err)
			return
		}

		integrity := computeSHA512(tarballBytes)

		pubReq := &api.PublishRequest{
			Version:           m.Version,
			Manifest:          manifestJSON,
			BlobRef:           uploadResp.UploadID,
			ArtifactType:      "tgz",
			ArtifactSize:      int64(len(tarballBytes)),
			ArtifactIntegrity: integrity,
		}
		fmt.Printf("%s  %s\n", ui.Label.Render("Publishing..."), m.Name)
		if err := client.Publish(m.Name, pubReq); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Publish failed:"), err)
			return
		}
		fmt.Printf("%s  %s@%s\n", ui.Success.Render("✓ Published:"), m.Name, m.Version)
	},
}

// determinePackFiles returns the list of files to include in the tarball.
// If the manifest declares a "files" field, uses that. Otherwise, packs
// the manifest + entrypoint + any .py/.js/.ts files in the directory.
func determinePackFiles(dir string, m *manifest.Manifest) []string {
	// Always include the manifest
	files := []string{"pharos.json"}

	if cmd := m.RunCommand(); cmd != "" {
		// Extract the script filename from the run command
		// e.g. "python server.py" → "server.py"
		parts := strings.Fields(cmd)
		for _, p := range parts {
			if strings.HasSuffix(p, ".py") || strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".ts") {
				files = append(files, p)
				break
			}
		}
	}

	// Also include the entrypoint if declared
	if m.Entrypoint != "" {
		hasEP := false
		for _, f := range files {
			if f == m.Entrypoint {
				hasEP = true
				break
			}
		}
		if !hasEP {
			files = append(files, m.Entrypoint)
		}
	}

	// If manifest declares explicit files, use those instead
	if len(m.Files) > 0 {
		files = m.Files
		// Make sure manifest is always included
		hasManifest := false
		for _, f := range files {
			if f == "pharos.json" {
				hasManifest = true
				break
			}
		}
		if !hasManifest {
			files = append([]string{"pharos.json"}, files...)
		}
	}

	// Deduplicate
	seen := make(map[string]bool)
	var result []string
	for _, f := range files {
		if !seen[f] {
			seen[f] = true
			result = append(result, f)
		}
	}
	return result
}

// createTarball creates a .tar.gz archive containing the specified files
// from the source directory.
func createTarball(outputPath, srcDir string, files []string) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	for _, file := range files {
		filePath := filepath.Join(srcDir, file)
		info, err := os.Stat(filePath)
		if err != nil {
			return fmt.Errorf("cannot stat %s: %w", file, err)
		}
		if info.IsDir() {
			continue
		}

		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("cannot read %s: %w", file, err)
		}

		header := &tar.Header{
			Name: file,
			Size: info.Size(),
			Mode: int64(info.Mode()),
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	return nil
}

// formatSize formats a byte count as a human-readable string.
func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
	)
	switch {
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// computeSHA512 returns the hex-encoded SHA-512 hash of the data,
// prefixed with "sha512-" as per the Pharos integrity format.
func computeSHA512(data []byte) string {
	h := sha512.Sum512(data)
	return "sha512-" + hex.EncodeToString(h[:])
}

// ── pharos package command ───────────────────────────────────────────────────

var packageCmd = &cobra.Command{
	Use:   "package [dir]",
	Short: "Create a .tgz tarball from a package directory (like npm pack)",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		dir := "."
		if len(args) == 1 {
			dir = args[0]
		}

		m, err := manifest.Load(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot read manifest:"), err)
			return
		}
		if err := m.Validate(); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Invalid manifest:"), err)
			return
		}

		tarballName := fmt.Sprintf("%s-%s.tgz", m.Name, m.Version)
		tarballPath := filepath.Join(dir, tarballName)

		filesToPack := determinePackFiles(dir, m)
		fmt.Printf("%s  %s\n", ui.Label.Render("Packaging:"), strings.Join(filesToPack, ", "))

		if err := createTarball(tarballPath, dir, filesToPack); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Packaging failed:"), err)
			return
		}

		info, _ := os.Stat(tarballPath)
		fmt.Printf("%s  %s (%s)\n", ui.Success.Render("✓ Created:"), tarballName, formatSize(info.Size()))
	},
}

func init() {
	publishCmd.Flags().StringVarP(&publishToken, "token", "t", "", "auth token (overrides config)")
	publishCmd.Flags().BoolVarP(&publishDryRun, "dry-run", "", false, "validate without publishing")
	rootCmd.AddCommand(publishCmd)
	rootCmd.AddCommand(packageCmd)
}
