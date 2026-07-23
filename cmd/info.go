package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

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
		var versions []string
		for _, v := range pkg.Versions {
			versions = append(versions, v.Version)
		}
		var tags []string
		for k, v := range pkg.DistTags {
			tags = append(tags, k+":"+v)
		}
		sort.Strings(tags)

		fmt.Printf("%s  %s\n", ui.Label.Render("Name:"), ui.PackageName.Render(pkg.Name))
		if pkg.Title != "" {
			fmt.Printf("%s  %s\n", ui.Label.Render("Title:"), pkg.Title)
		}
		fmt.Printf("%s  %s\n", ui.Label.Render("Description:"), pkg.Description)
		fmt.Printf("%s  %s\n", ui.Label.Render("Latest:"), latest)
		fmt.Printf("%s  %s\n", ui.Label.Render("Versions:"), strings.Join(versions, ", "))
		if pkg.License != "" {
			fmt.Printf("%s  %s\n", ui.Label.Render("License:"), pkg.License)
		}
		if pkg.RepoURL != "" {
			fmt.Printf("%s  %s\n", ui.Label.Render("Repository:"), pkg.RepoURL)
		}
		if pkg.RepoURL != "" {
			fmt.Printf("%s  %s\n", ui.Label.Render("Homepage:"), pkg.RepoURL)
		}
		if len(tags) > 0 {
			fmt.Printf("%s  %s\n", ui.Label.Render("Dist-tags:"), strings.Join(tags, ", "))
		}
		fmt.Printf("%s  %s\n", ui.Label.Render("Created:"), pkg.CreatedAt)
	},
}

func init() {
	infoCmd.Flags().BoolVar(&jsonFlag, "json", false, "output as JSON")
	rootCmd.AddCommand(infoCmd)
}
