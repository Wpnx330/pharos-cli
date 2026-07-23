package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/install"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var installVersion string
var installGlobal bool

var installCmd = &cobra.Command{
	Use:     "install <name>",
	Aliases: []string{"i"},
	Short:   "Download and install an MCP server package",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		_, client := loadConfig()
		name := args[0]

		fmt.Printf("%s  %s\n", ui.Label.Render("Fetching package info..."), name)
		pkg, err := client.GetPackage(name)
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to fetch package:"), err)
			return
		}

		latest := ""
		if pkg.DistTags != nil {
			latest = pkg.DistTags["latest"]
		}
		version := installVersion
		if version == "" {
			version = latest
		}
		if version == "" && len(pkg.Versions) > 0 {
			version = pkg.Versions[0].Version
		}

		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot determine home directory:"), err)
			return
		}
		pkgDir := filepath.Join(home, ".pharos", "packages")
		if installGlobal {
			pkgDir = "/usr/local/pharos/packages"
		}
		mgr := install.NewManager(pkgDir)

		if mgr.IsInstalled(name) {
			fmt.Printf("%s  %s\n", ui.Muted.Render("Already installed:"), name)
			return
		}

		fmt.Printf("%s  %s@%s\n", ui.Label.Render("Downloading..."), name, version)
		// Construct tarball URL from repo or registry convention.
		tarballURL := fmt.Sprintf("%s/v1/packages/%s/tarballs/%s", client.BaseURL, name, version)
		if err := mgr.Install(name, version, tarballURL); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Install failed:"), err)
			return
		}

		fmt.Printf("%s  %s\n", ui.Success.Render("✓ Installed:"), fmt.Sprintf("%s@%s", name, version))
		if len(pkg.Versions) > 0 {
			fmt.Printf("\n%s  %s\n", ui.Label.Render("Usage:"), fmt.Sprintf("pharos info %s", name))
		}
	},
}

func init() {
	installCmd.Flags().StringVarP(&installVersion, "version", "v", "", "specific version to install")
	installCmd.Flags().BoolVarP(&installGlobal, "global", "g", false, "install system-wide")
	rootCmd.AddCommand(installCmd)
}
