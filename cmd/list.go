package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/install"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List locally installed packages",
	Run: func(cmd *cobra.Command, args []string) {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot determine home directory:"), err)
			return
		}
		mgr := install.NewManager(filepath.Join(home, ".pharos", "store"))
		pkgs, err := mgr.List()
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to list packages:"), err)
			return
		}
		if len(pkgs) == 0 {
			fmt.Println(ui.Muted.Render("No packages installed."))
			return
		}
		cols := []ui.TableColumn{
			{Title: "NAME", Width: 28},
			{Title: "VERSION", Width: 12},
			{Title: "LOCATION", Width: 50},
		}
		var rows []ui.TableRow
		for _, p := range pkgs {
			rows = append(rows, ui.TableRow{
				ui.PackageName.Render(p.Name),
				p.Version,
				p.Location,
			})
		}
		fmt.Print(ui.RenderTable(cols, rows))
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
