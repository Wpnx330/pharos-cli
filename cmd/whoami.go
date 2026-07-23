package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/auth"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Display the currently authenticated user",
	Run: func(cmd *cobra.Command, args []string) {
		creds, err := auth.Load()
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Not logged in."), ui.Muted.Render("Run `pharos login` first."))
			os.Exit(1)
		}

		_, client := loadConfig()
		client.Token = creds.Token

		user, err := client.GetCurrentUser()
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to fetch user info:"), err)
			fmt.Fprintln(os.Stderr, ui.Muted.Render("Your token may be expired. Run `pharos login` again."))
			return
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(user, "", "  ")
			fmt.Println(string(data))
			return
		}

		fmt.Printf("%s  %s\n", ui.Label.Render("User:"), ui.PackageName.Render("@"+user.Username))
		if user.Email != "" {
			fmt.Printf("%s  %s\n", ui.Label.Render("Email:"), user.Email)
		}
		if user.AvatarURL != "" {
			fmt.Printf("%s  %s\n", ui.Label.Render("Avatar:"), user.AvatarURL)
		}
		if user.ID != "" {
			fmt.Printf("%s  %s\n", ui.Label.Render("ID:"), user.ID)
		}
		if len(user.Namespaces) > 0 {
			fmt.Printf("%s  %s\n", ui.Label.Render("Namespaces:"), strings.Join(user.Namespaces, ", "))
		}
	},
}

func init() {
	whoamiCmd.Flags().BoolVar(&jsonFlag, "json", false, "output as JSON")
	rootCmd.AddCommand(whoamiCmd)
}
