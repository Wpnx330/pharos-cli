package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var infoCmd = &cobra.Command{
	Use:     "info <name>",
	Aliases: []string{"show"},
	Short:   "Show detailed information about a package",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		_, client := loadConfig()
		name := args[0]
		pkg, err := client.GetPackage(name)
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to get package:"), err)
			return
		}
		if jsonFlag {
			data, _ := json.MarshalIndent(pkg, "", "  ")
			fmt.Println(string(data))
			return
		}
		latest := ""
		if pkg.DistTags != nil {
			latest = pkg.DistTags["latest"]
		}

		// Determine the "latest" version manifest for header metadata.
		var latestManifest *api.Manifest
		if v := pkg.FindVersion(latest); v != nil {
			latestManifest = &v.Manifest
		} else if len(pkg.Versions) > 0 {
			latestManifest = &pkg.Versions[0].Manifest
		}

		// Header line: bold gold package name + muted version
		fmt.Printf("%s %s\n",
			ui.PackageName.Render(pkg.Name),
			ui.Muted.Render("v"+latest))

		// Description on its own line
		if pkg.Description != "" {
			fmt.Println(pkg.Description)
		}
		fmt.Println()

		// Metadata section — labels right-padded to 12 chars
		labelWidth := 12
		printMeta := func(label, value string) {
			fmt.Printf("%-*s  %s\n", labelWidth, label+":", value)
		}

		if latestManifest != nil {
			// Always show transport, runtime, capabilities, source
			transport := latestManifest.Transport
			if transport == "" {
				transport = ui.Muted.Render("Not specified")
			}
			printMeta("Transport", transport)

			runtime := latestManifest.Runtime
			if runtime == "" {
				runtime = ui.Muted.Render("Not specified")
			}
			printMeta("Runtime", runtime)

			caps := strings.Join(latestManifest.Capabilities, ", ")
			if caps == "" {
				caps = ui.Muted.Render("None")
			}
			printMeta("Capabilities", caps)

			source := pkg.RepoSource
			if source == "" {
				source = "unknown"
			}
			printMeta("Source", source)

			// Dependencies — show declared dependency constraints
			if len(latestManifest.Dependencies) > 0 {
				var deps []string
				for _, dep := range latestManifest.Dependencies {
					deps = append(deps, dep.Name+"@"+dep.Version)
				}
				printMeta("Dependencies", strings.Join(deps, ", "))
			}

			// Verified — always show
			printMeta("Verified", ui.Muted.Render("No"))
			}

		// License — show "Not specified" when empty; fall back to the
		// latest version's manifest if the packages table has no value.
		license := pkg.License
		if license == "" && latestManifest != nil {
			license = latestManifest.License
		}
		if license == "" {
			license = ui.Muted.Render("Not specified")
		}
		printMeta("License", license)

		// Repository — show "Not specified" when empty; fall back to the
		// latest version's manifest if the packages table has no value.
		repo := pkg.RepoURL
		if repo == "" && latestManifest != nil {
			repo = string(latestManifest.Repository)
		}
		if repo == "" {
			repo = ui.Muted.Render("Not specified")
		}
		// Append GitHub star count after the repo URL when known.
		// nil means we don't know (not a GitHub repo or fetch failed);
		// 0 means zero stars — both are valid but only non-nil shows.
		if pkg.GitHubStars != nil {
			stars := fmt.Sprintf("★ %d", *pkg.GitHubStars)
			printMeta("Git Repo", repo+"  "+ui.Warning.Render(stars))
		} else {
			printMeta("Git Repo", repo)
		}

		// Dates — show Created/Modified for native packages,
		// Last Sync for synced packages (created/modified are
		// internal-only for synced entries).
		if pkg.LastSyncedAt != "" {
			// Synced package — show Last Sync instead of Created/Modified
			printMeta("Last Sync", formatDate(pkg.LastSyncedAt))
		} else {
			// Native (Pharos-published) package
			printMeta("Created", formatDate(pkg.CreatedAt))
			if pkg.ModifiedAt != "" {
				printMeta("Modified", formatDate(pkg.ModifiedAt))
			}
		}

		// Versions table
		if len(pkg.Versions) > 0 {
			fmt.Println()
			fmt.Println(ui.Label.Render("VERSIONS"))
			cols := []ui.TableColumn{
				{Title: "Version", Width: 10, MaxWidth: 10},
				{Title: "Status", Width: 10, MaxWidth: 10},
				{Title: "Transport", Width: 12, MaxWidth: 12},
				{Title: "Runtime", Width: 10, MaxWidth: 10},
				{Title: "Downloads", Width: 10, MaxWidth: 10},
				{Title: "PKG SIZE", Width: 10, MaxWidth: 10},
				{Title: "Created", Width: 12, MaxWidth: 12},
			}
			var rows []ui.TableRow
			for _, v := range pkg.Versions {
				status := v.Status
				switch status {
				case "active":
					status = ui.Success.Render(status)
				case "deprecated":
					status = ui.Warning.Render(status)
				case "unpublished", "yanked":
					status = ui.Error.Render(status)
				}
				size := ui.Muted.Render("—")
				if v.ArtifactSize != nil {
					size = ui.FormatBytes(*v.ArtifactSize)
				}
				rows = append(rows, ui.TableRow{
					v.Version,
					status,
					v.Manifest.Transport,
					v.Manifest.Runtime,
					fmt.Sprintf("%d", v.Downloads),
					size,
					formatDate(v.CreatedAt),
				})
			}
			fmt.Print(ui.RenderTable(cols, rows))
		}

		// Dist-tags at the bottom
		if len(pkg.DistTags) > 0 {
			var tags []string
			for k, v := range pkg.DistTags {
				tags = append(tags, k+":"+v)
			}
			sort.Strings(tags)
			fmt.Println()
			fmt.Printf("%s  %s\n", ui.Label.Render("Dist-tags:"), strings.Join(tags, ", "))
		}

		// Runtime requirement check — show whether the executable needed
		// to run this package is available on this machine.
		if latestManifest != nil {
			transport := strings.ToLower(strings.TrimSpace(latestManifest.Transport))
			if transport == "" {
				transport = "stdio"
			}
			if warning := checkRuntimeRequirement(*latestManifest, transport); warning != "" {
				fmt.Printf("\n%s  %s\n", ui.Error.Render("⚠ Requirement not met:"), warning)
			}
		}
	},
}

func init() {
	infoCmd.Flags().BoolVar(&jsonFlag, "json", false, "output as JSON")
	rootCmd.AddCommand(infoCmd)
}

// formatDate extracts the date portion (YYYY-MM-DD) from an ISO 8601
// timestamp. If the string is shorter than 10 characters or empty,
// it returns the input unchanged.
func formatDate(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}
