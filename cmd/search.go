package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var searchLimit int
var searchPage int
var searchRegistry string
var searchTransport string

var searchCmd = &cobra.Command{
	Use:     "search <query>",
	Aliases: []string{"s"},
	Short:   "Search the PHAROS registry for packages",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		_, client := loadConfig()
		query := args[0]
		results, err := client.Search(api.SearchParams{
			Query:     query,
			Page:      searchPage,
			Limit:     searchLimit,
			Registry:  searchRegistry,
			Transport: searchTransport,
		})
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
		cols := searchTableColumns()
		var rows []ui.TableRow
		for _, r := range results.Results {
			rows = append(rows, searchTableRow(r))
		}
		fmt.Print(ui.RenderTable(cols, rows))
		fmt.Println(searchInfoFooter())
		fmt.Printf("\n%s\n", ui.Muted.Render(fmt.Sprintf("%d package(s) found", results.Total)))
		if hint := searchNextPageHint(query, searchPage, searchRegistry, searchTransport, results.NextCursor); hint != "" {
			fmt.Println(ui.Muted.Render(hint))
		}
	},
}

// searchInfoFooter is the one-line hint printed after the search table.
// NAME is the package ID (including spaces); quote it for `pharos info`.
func searchInfoFooter() string {
	return `Use: pharos info "<PACKAGE ID>"   (quote IDs that contain spaces)`
}

func init() {
	searchCmd.Flags().IntVarP(&searchLimit, "limit", "n", 10, "number of results")
	searchCmd.Flags().IntVarP(&searchPage, "page", "p", 1, "page number")
	searchCmd.Flags().StringVar(&searchRegistry, "registry", "", "filter by source registry (e.g. mcp.io, pharos)")
	searchCmd.Flags().StringVar(&searchTransport, "transport", "", "filter by transport (e.g. stdio, http-sse)")
	searchCmd.Flags().BoolVar(&jsonFlag, "json", false, "output as JSON")
	rootCmd.AddCommand(searchCmd)
}

// searchNextPageHint is printed after the table when the registry returns
// nextCursor. --page stays as the user-facing pagination flag.
func searchNextPageHint(query string, page int, registry, transport, nextCursor string) string {
	if nextCursor == "" {
		return ""
	}
	if page < 1 {
		page = 1
	}
	var b strings.Builder
	b.WriteString(`next page: pharos search "`)
	b.WriteString(query)
	b.WriteString(`" --page `)
	b.WriteString(strconv.Itoa(page + 1))
	if registry = strings.TrimSpace(registry); registry != "" {
		b.WriteString(" --registry ")
		b.WriteString(registry)
	}
	if transport = strings.TrimSpace(transport); transport != "" {
		b.WriteString(" --transport ")
		b.WriteString(transport)
	}
	return b.String()
}

// searchTableColumns is the human table header for `pharos search`.
// TRANSPORT and REGISTRY sit after VERSION so a hit's protocol and
// originating catalog are visible without --json.
func searchTableColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{Title: "NAME", Width: 20, MaxWidth: 0},
		{Title: "VERSION", Width: 10, MaxWidth: 10},
		{Title: "TRANSPORT", Width: 16, MaxWidth: 24},
		{Title: "REGISTRY", Width: 10, MaxWidth: 16},
		{Title: "DESCRIPTION", Width: 30, MaxWidth: 50},
		{Title: "DOWNLOADS", Width: 10, MaxWidth: 10},
	}
}

func searchCellOrDash(s string) string {
	if s == "" {
		return listDash
	}
	return s
}

// formatSearchTransport joins registry transport strings with ",".
// Empty or all-blank values render as the same em dash list uses.
func formatSearchTransport(transports []string) string {
	var parts []string
	for _, t := range transports {
		if t != "" {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return listDash
	}
	return strings.Join(parts, ",")
}

func searchTableRow(r api.SearchResult) ui.TableRow {
	return ui.TableRow{
		ui.PackageName.Render(r.Name),
		r.Version,
		formatSearchTransport(r.Transport),
		searchCellOrDash(r.SourceRegistry),
		r.Description,
		strconv.FormatInt(r.Downloads, 10),
	}
}
