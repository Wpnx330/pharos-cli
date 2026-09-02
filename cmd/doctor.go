package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/runtime"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var doctorJSON bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose PHAROS CLI health — registry, installed servers, configs",
	Long: ui.Label.Render("pharos doctor") + ` runs a series of health checks:

  • Registry connectivity
  • Installed server reachability
  • Lockfile integrity hash validation
  • Client config validity (JSON, TOML, YAML)
  • Client config drift vs the pharos.lock baseline (--diff)

Reports any issues found.`,
	Run: func(cmd *cobra.Command, args []string) {
		_, client := loadConfig()

		var checks []doctorCheck
		var failures int

		// 1. Registry connectivity
		checks = append(checks, runCheck("Registry connectivity", func() (string, error) {
			h, err := client.Health()
			if err != nil {
				return "", err
			}
			return h.Status, nil
		}))

		// 2. Lockfile
		lockPath, err := lockfile.DefaultPath()
		if err == nil {
			if lf, err := lockfile.Load(lockPath); err == nil && len(lf.Servers) > 0 {
				checks = append(checks, runCheck("Lockfile", func() (string, error) {
					lf, err := lockfile.Load(lockPath)
					if err != nil {
						return "", err
					}
					return fmt.Sprintf("%d servers tracked", len(lf.Servers)), nil
				}))
			}
		}

		// 3. Installed servers in store
		home, _ := os.UserHomeDir()
		storeDir := filepath.Join(home, ".pharos", "store")
		if entries, err := os.ReadDir(storeDir); err == nil && len(entries) > 0 {
			checks = append(checks, runCheck("Installed servers", func() (string, error) {
				count := 0
				for _, e := range entries {
					if e.IsDir() {
						count++
					}
				}
				return fmt.Sprintf("%d server(s) in store", count), nil
			}))
		}

		// 4. Client config validity
		for _, c := range clientconfig.Detect() {
			if !c.Existing {
				continue
			}
			c := c // capture for closure
			checks = append(checks, runCheck(fmt.Sprintf("Config: %s", c.Name), func() (string, error) {
				return validateConfig(c)
			}))
		}

		// 4b. Config drift (--diff, W1.4): compare pharos-managed server
		// entries in each client config against the pharos.lock +
		// canonical baseline. Strictly read-only.
		var driftNote string
		if doctorDiff {
			driftChecks, note := runDriftChecks()
			checks = append(checks, driftChecks...)
			driftNote = note
		}

		// 5. Runtime executables — check which common MCP server runtimes
		// are available on PATH. These are advisory: a missing runtime
		// only matters if you install a package that needs it.
		for _, rt := range []string{"python", "pip", "node", "npx", "uv", "uvx", "docker"} {
			rt := rt // capture for closure
			checks = append(checks, runCheck(fmt.Sprintf("Runtime: %s", rt), func() (string, error) {
				path, err := exec.LookPath(rt)
				if err != nil {
					hint := runtime.ExecutableHint(rt)
					if hint != "" {
						return "", fmt.Errorf("not found — %s", hint)
					}
					return "", fmt.Errorf("not found in $PATH")
				}
				return path, nil
			}))
		}

		// Tally
		for i := range checks {
			if checks[i].Status != "ok" {
				failures++
			}
		}

		if JSONRequested() {
			out := map[string]any{
				"checks":   checks,
				"failures": failures,
				"healthy":  failures == 0,
			}
			data, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(data))
		} else {
			printDoctorChecks(os.Stdout, checks, driftNote)
			if failures == 0 {
				fmt.Println(ui.Success.Render("✓ All checks passed."))
			} else {
				fmt.Fprintf(os.Stderr, "%s  %d check(s) failed.\n", ui.Error.Render("✗"), failures)
				os.Exit(1)
			}
		}
	},
}

// printDoctorChecks renders the human health report. Drift findings
// (--diff) render as one indented bullet line each, in the same order as
// the JSON report's findings array, so human and machine consumers see the
// same findings. driftNote is the muted nothing-to-compare footnote for
// --diff runs; it is only printed when the flag is set.
func printDoctorChecks(w io.Writer, checks []doctorCheck, driftNote string) {
	fmt.Fprintln(w, ui.Label.Render("PHAROS Doctor — Health Check")+"\n")
	for _, c := range checks {
		var icon, detail string
		if c.Status == "ok" {
			icon = ui.Success.Render("✓")
			detail = c.Detail
		} else {
			icon = ui.Error.Render("✗")
			detail = c.Error
		}
		fmt.Fprintf(w, "  %s  %-30s  %s\n", icon, c.Name, detail)
		for _, f := range c.Findings {
			fmt.Fprintf(w, "        • %s\n", f.Message)
		}
	}
	fmt.Fprintln(w)
	if doctorDiff && driftNote != "" {
		fmt.Fprintln(w, ui.Muted.Render(driftNote))
		fmt.Fprintln(w)
	}
}

// doctorCheck represents the result of a single health check. Findings is
// populated only by the drift checks (--diff) and is omitted otherwise, so
// the JSON shape stays additive for existing consumers.
type doctorCheck struct {
	Name     string          `json:"name"`
	Status   string          `json:"status"`
	Detail   string          `json:"detail,omitempty"`
	Error    string          `json:"error,omitempty"`
	Findings []doctorFinding `json:"findings,omitempty"`
}

// runCheck executes a check function and wraps the result.
func runCheck(name string, fn func() (string, error)) doctorCheck {
	c := doctorCheck{Name: name}
	detail, err := fn()
	if err != nil {
		c.Status = "fail"
		c.Error = err.Error()
	} else {
		c.Status = "ok"
		c.Detail = detail
	}
	return c
}

// validateConfig reads a client config file and verifies it's valid
// for the client's format: JSON (mcpServers/array/opencode/zed), YAML
// (hermes/aider), or TOML (codex/grok).
func validateConfig(c clientconfig.Client) (string, error) {
	data, err := os.ReadFile(c.Path)
	if err != nil {
		return "", err
	}
	switch c.Format {
	case clientconfig.FormatTOML:
		var v map[string]any
		if err := toml.Unmarshal(data, &v); err != nil {
			return "", fmt.Errorf("invalid TOML: %w", err)
		}
		return "valid (TOML)", nil
	case clientconfig.FormatHermes, clientconfig.FormatAider:
		var v any
		if err := yaml.Unmarshal(data, &v); err != nil {
			return "", fmt.Errorf("invalid YAML: %w", err)
		}
		return "valid (YAML)", nil
	default:
		// FormatMcpServers, FormatArray, FormatOpenCode, FormatZed — all JSON
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			return "", fmt.Errorf("invalid JSON: %w", err)
		}
		return "valid (JSON)", nil
	}
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(doctorCmd)
}
