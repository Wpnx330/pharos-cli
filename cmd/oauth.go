package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var oauthConfigureCmd = &cobra.Command{
	Use:   "configure <server-name>",
	Short: "Configure OAuth for a published MCP server",
	Long: `Configure OAuth settings for an MCP server you have published to the Pharos registry.

This registers your OAuth provider details so users can authenticate through
Pharos's embedded OAuth flow when they install your server.

You need:
- The OAuth authorization server URL (e.g. https://accounts.google.com)
- Your client ID registered with the provider
- The scopes your server needs

Example:
  pharos oauth configure my-mcp-server \
    --auth-url https://accounts.google.com/oauth/authorize \
    --client-id your-client-id \
    --scopes openid,email,profile`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runOauthConfigure(args[0])
	},
}

var (
	oauthAuthURL  string
	oauthClientID string
	oauthScopes   string
	oauthPKCE     bool
	oauthSecret   string
)

func init() {
	oauthConfigureCmd.Flags().StringVar(&oauthAuthURL, "auth-url", "", "OAuth authorization server URL")
	oauthConfigureCmd.Flags().StringVar(&oauthClientID, "client-id", "", "OAuth client ID")
	oauthConfigureCmd.Flags().StringVar(&oauthScopes, "scopes", "", "Comma-separated OAuth scopes")
	oauthConfigureCmd.Flags().BoolVar(&oauthPKCE, "pkce", true, "Require PKCE (default: true)")
	oauthConfigureCmd.Flags().StringVar(&oauthSecret, "client-secret", "", "Client secret (for server-side secret handling)")

	oauthCmd := &cobra.Command{
		Use:   "oauth",
		Short: "Manage OAuth configuration for MCP servers",
	}
	oauthCmd.AddCommand(oauthConfigureCmd)
	rootCmd.AddCommand(oauthCmd)
}

func runOauthConfigure(serverName string) {
	if oauthAuthURL == "" || oauthClientID == "" {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Error: --auth-url and --client-id are required"))
		os.Exit(1)
	}

	_, client := loadConfig()

	// Parse scopes.
	var scopes []string
	if oauthScopes != "" {
		scopes = strings.Split(oauthScopes, ",")
		for i, s := range scopes {
			scopes[i] = strings.TrimSpace(s)
		}
	}

	// Build the request body.
	body := map[string]any{
		"auth_server_url": oauthAuthURL,
		"client_id":       oauthClientID,
		"scopes":          scopes,
		"pkce_required":   oauthPKCE,
	}
	if oauthSecret != "" {
		body["client_secret"] = oauthSecret
		body["secret_handling"] = "server_side"
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to marshal request:"), err)
		os.Exit(1)
	}

	fmt.Printf("%s  Configuring OAuth for %s...\n", ui.Label.Render("OAuth:"), serverName)

	// Call the registry endpoint.
	resp, err := client.Post(fmt.Sprintf("/v1/oauth/servers/%s/configure", serverName), bodyJSON)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("OAuth configuration failed:"), err)
		os.Exit(1)
	}

	fmt.Printf("%s  OAuth configured successfully.\n", ui.Success.Render("✓"))
	if oauthPKCE {
		fmt.Printf("  %s  PKCE required (S256)\n", ui.Label.Render("Security:"))
	}
	if oauthSecret != "" {
		fmt.Printf("  %s  Secret handling: server-side\n", ui.Label.Render("Security:"))
	}
	fmt.Printf("  %s  Auth server: %s\n", ui.Label.Render("Provider:"), oauthAuthURL)
	fmt.Printf("  %s  Scopes: %s\n", ui.Label.Render("Scopes:"), oauthScopes)
	_ = resp
}
