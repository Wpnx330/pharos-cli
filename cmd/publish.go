package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
			fmt.Fprintln(os.Stderr, ui.Error.Render("No auth token. Use --token or set 'pharos config token <token>'"))
			return
		}

		cfg, _ := config.Load()
		client := api.New(cfg.Registry, token)

		// Phase 1: upload
		fmt.Printf("%s  %s\n", ui.Label.Render("Uploading tarball..."), filepath.Join(dir, "dist"))
		uploadResp, err := client.Upload(m.Name+"-"+m.Version+".tgz", 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Upload failed:"), err)
			return
		}

		// Phase 2: publish
		pubReq := &api.PublishRequest{
			Name:        m.Name,
			Version:     m.Version,
			Description: m.Description,
			License:     m.License,
			Homepage:    m.Homepage,
			Repository:  m.Repository,
			Bin:         m.Bin,
			Files:       m.Files,
			UploadID:    uploadResp.UploadID,
			DistTags:    map[string]string{"latest": m.Version},
		}
		fmt.Printf("%s  %s\n", ui.Label.Render("Publishing..."), m.Name)
		if err := client.Publish(m.Name, pubReq); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Publish failed:"), err)
			return
		}
		fmt.Printf("%s  %s@%s\n", ui.Success.Render("✓ Published:"), m.Name, m.Version)
	},
}

func init() {
	publishCmd.Flags().StringVarP(&publishToken, "token", "t", "", "auth token (overrides config)")
	publishCmd.Flags().BoolVar(&publishDryRun, "dry-run", false, "validate without publishing")
	rootCmd.AddCommand(publishCmd)
}
