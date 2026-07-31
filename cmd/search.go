package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var searchLimit int
var searchPage int

var searchCmd = &cobra.Command{
	Use:     "search <query>",
	Aliases: []string{"s"},
	Short:   "Search the PHAROS registry for packages",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		_, client := loadConfig()
		query := args[0]
		results, err := client.Search(query, searchPage, searchLimit)
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Search failed:"), err)
			return
		}
		if len(results.Results) == 0 {
			fmt.Println(ui.Muted.Render("No packages found."))
			return
		}
		if jsonFlag {
			data, _ := json.MarshalIndent(results, "", "  ")
			fmt.Println(string(data))
			return
		}
		cols := []ui.TableColumn{
			{Title: "NAME", Width: 20, MaxWidth: 0},
			{Title: "VERSION", Width: 10, MaxWidth: 10},
			{Title: "DESCRIPTION", Width: 30, MaxWidth: 50},
			{Title: "DOWNLOADS", Width: 10, MaxWidth: 10},
		}
		var rows []ui.TableRow
		for _, r := range results.Results {
			rows = append(rows, ui.TableRow{
				ui.PackageName.Render(r.Name),
				r.Version,
				r.Description,
				strconv.FormatInt(r.Downloads, 10),
			})
		}
		fmt.Print(ui.RenderTable(cols, rows))
		fmt.Printf("\n%s\n", ui.Muted.Render(fmt.Sprintf("%d package(s) found", results.Total)))
	},
}

func init() {
	searchCmd.Flags().IntVarP(&searchLimit, "limit", "n", 10, "number of results")
	searchCmd.Flags().IntVarP(&searchPage, "page", "p", 1, "page number")
	searchCmd.Flags().BoolVar(&jsonFlag, "json", false, "output as JSON")
	rootCmd.AddCommand(searchCmd)
}
