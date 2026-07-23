package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/auth"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var loginManual bool

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with the PHAROS registry via GitHub OAuth",
	Long: ui.Label.Render("pharos login") + ` opens your browser to complete GitHub OAuth.

After successful authentication, an API token is stored in ~/.pharos/credentials.json
and used for all subsequent commands that require authentication.

Use --manual to skip the browser flow and paste a token directly.`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg, _ := loadConfig()

		if loginManual {
			runManualLogin(cfg.Registry)
			return
		}
		runBrowserLogin(cfg.Registry)
	},
}

// runBrowserLogin starts a local HTTP server, opens the OAuth URL, and
// waits for the callback to deliver a token.
func runBrowserLogin(registry string) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Could not start local server:"), err)
		fmt.Fprintln(os.Stderr, ui.Muted.Render("Try `pharos login --manual` instead."))
		return
	}
	port := listener.Addr().(*net.TCPAddr).Port
	callbackURL := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	oauthURL := fmt.Sprintf("%s/v1/auth/github?redirect_uri=%s", registry, callbackURL)

	fmt.Printf("%s  %s\n", ui.Label.Render("Opening browser for GitHub login..."), oauthURL)
	fmt.Printf("%s\n\n", ui.Muted.Render("If your browser doesn't open, visit the URL above manually."))

	if err := openBrowser(oauthURL); err != nil {
		fmt.Fprintf(os.Stderr, "%s  %v\n", ui.Muted.Render("(could not auto-open browser)"), err)
	}

	tokenCh := make(chan string, 1)
	errCh := make(chan error, 1)

	srv := &http.Server{}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		// The registry may redirect with ?token=... directly, or with ?code=...
		token := r.URL.Query().Get("token")
		if token != "" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, loginSuccessHTML)
			tokenCh <- token
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "Missing code or token parameter.")
			errCh <- fmt.Errorf("callback missing token and code")
			return
		}
		// Exchange code for token via the registry
		client := api.New(registry, "")
		lr, err := client.ExchangeCodeForToken(code)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "Token exchange failed: "+err.Error())
			errCh <- err
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, loginSuccessHTML)
		tokenCh <- lr.Token
	})

	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	go func() {
		<-ctx.Done()
		errCh <- fmt.Errorf("login timed out after 5 minutes")
	}()

	select {
	case token := <-tokenCh:
		srv.Shutdown(context.Background())
		creds := &auth.Credentials{Token: token}
		if err := auth.Save(creds); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to save credentials:"), err)
			return
		}
		// Also store token in config for commands that read cfg.Token
		cfg, _ := loadConfig()
		cfg.Token = token
		_ = cfg.Save()
		fmt.Printf("\n%s  %s\n", ui.Success.Render("✓ Logged in."), "Token stored in ~/.pharos/credentials.json")
		fmt.Printf("%s\n", ui.Muted.Render("Run `pharos whoami` to verify your identity."))
	case err := <-errCh:
		srv.Shutdown(context.Background())
		fmt.Fprintln(os.Stderr, ui.Error.Render("Login failed:"), err)
		fmt.Fprintln(os.Stderr, ui.Muted.Render("Try `pharos login --manual` to paste a token directly."))
	}
}

// runManualLogin prompts the user to paste a token directly.
func runManualLogin(registry string) {
	oauthURL := fmt.Sprintf("%s/v1/auth/github", registry)
	fmt.Printf("%s  %s\n", ui.Label.Render("Open this URL to authenticate:"), oauthURL)
	fmt.Printf("%s\n", ui.Muted.Render("After authorising, copy the token from the response and paste it below."))
	fmt.Print("\nToken: ")

	var token string
	if _, err := fmt.Fscanln(os.Stdin, &token); err != nil || token == "" {
		fmt.Fprintln(os.Stderr, ui.Error.Render("No token entered."))
		return
	}
	creds := &auth.Credentials{Token: token}
	if err := auth.Save(creds); err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to save credentials:"), err)
		return
	}
	cfg, _ := loadConfig()
	cfg.Token = token
	_ = cfg.Save()
	fmt.Printf("%s  %s\n", ui.Success.Render("✓ Token stored."), "~/.pharos/credentials.json")
}

// openBrowser attempts to open the OS default browser to the given URL.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

const loginSuccessHTML = `<!DOCTYPE html>
<html><body style="font-family:sans-serif;text-align:center;padding:3em">
<h2>✓ Login Successful</h2>
<p>You can close this tab and return to your terminal.</p>
</body></html>`

func init() {
	loginCmd.Flags().BoolVar(&loginManual, "manual", false, "paste token manually instead of browser flow")
	rootCmd.AddCommand(loginCmd)
}
