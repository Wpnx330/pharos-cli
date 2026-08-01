package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var auditJSON bool

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Scan installed servers for known security vulnerabilities",
	Long: ui.Label.Render("pharos audit") + ` checks every server in pharos.lock (or detected client
configs if no lockfile exists) against the PHAROS security advisory database.

Exit code is 1 if any vulnerable versions are found.`,
	Run: func(cmd *cobra.Command, args []string) {
		_, client := loadConfig()

		servers := collectServersForAudit()
		if len(servers) == 0 {
			fmt.Println(ui.Muted.Render("No installed servers found. Run `pharos install` or `pharos import` first."))
			return
		}

		report := runAudit(client, servers)

		if auditJSON {
			data, _ := json.MarshalIndent(report, "", "  ")
			fmt.Println(string(data))
		} else {
			fmt.Print(formatAuditReport(report))
		}

		if report.HasVulns {
			os.Exit(1)
		}
	},
}

// auditReport holds the results of scanning all servers.
type auditReport struct {
	Total      int           `json:"total_servers"`
	Scanned    int           `json:"scanned"`
	Vulnerable int           `json:"vulnerable_servers"`
	Entries    []auditEntry  `json:"entries"`
	HasVulns   bool          `json:"has_vulnerabilities"`
}

// auditEntry pairs a server with its advisories.
type auditEntry struct {
	Server     string          `json:"server"`
	Version    string          `json:"version"`
	Advisories []api.Advisory   `json:"advisories"`
	Error      string          `json:"error,omitempty"`
}

// serverInfo is a minimal server representation for audit scanning.
type serverInfo struct {
	Name    string
	Version string
}

// runAudit queries the registry for advisories on each server.
func runAudit(client *api.Client, servers []serverInfo) *auditReport {
	report := &auditReport{Total: len(servers)}

	for _, s := range servers {
		entry := auditEntry{Server: s.Name, Version: s.Version}
		advisories, err := client.GetAdvisories(s.Name)
		if err != nil {
			entry.Error = err.Error()
		} else {
			entry.Advisories = filterApplicable(advisories, s.Version)
		}
		report.Entries = append(report.Entries, entry)
		report.Scanned++
		if len(entry.Advisories) > 0 {
			report.Vulnerable++
			report.HasVulns = true
		}
	}
	return report
}

// collectServersForAudit gathers servers from the lockfile or client configs.
func collectServersForAudit() []serverInfo {
	lockPath, err := lockfile.DefaultPath()
	if err == nil {
		if lf, err := lockfile.Load(lockPath); err == nil && len(lf.Servers) > 0 {
			var servers []serverInfo
			for name, entry := range lf.Servers {
				servers = append(servers, serverInfo{Name: name, Version: entry.Version})
			}
			sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
			return servers
		}
	}

	// Fall back to client configs
	var servers []serverInfo
	clients := clientconfig.Detect()
	for _, c := range clients {
		rawServers, err := clientconfig.ReadServersFormat(c.Path, c.Format)
		if err != nil {
			continue
		}
		for name := range rawServers {
			servers = append(servers, serverInfo{Name: name})
		}
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	return servers
}

// filterApplicable returns only advisories that affect the given version.
// If we can't parse the affected range, we include the advisory to be safe.
func filterApplicable(advisories []api.Advisory, version string) []api.Advisory {
	var result []api.Advisory
	for _, a := range advisories {
		if a.Affected == "" || versionMatchesRange(version, a.Affected) {
			result = append(result, a)
		}
	}
	return result
}

// versionMatchesRange does a simple check for "< x.y.z" patterns.
// This is intentionally conservative — when in doubt, return true.
func versionMatchesRange(version, affected string) bool {
	affected = strings.TrimSpace(affected)
	if strings.HasPrefix(affected, "< ") {
		bound := strings.TrimPrefix(affected, "< ")
		return compareVersions(version, bound) < 0
	}
	if strings.HasPrefix(affected, "<=") {
		bound := strings.TrimPrefix(affected, "<= ")
		return compareVersions(version, bound) <= 0
	}
	return true
}

// compareVersions compares two semver-like strings. Returns -1, 0, or 1.
func compareVersions(a, b string) int {
	pa := parseSemver(a)
	pb := parseSemver(b)
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

// parseSemver extracts [major, minor, patch] from a version string.
func parseSemver(v string) [3]int {
	var result [3]int
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	for i := 0; i < len(parts) && i < 3; i++ {
		n := 0
		for _, c := range parts[i] {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		result[i] = n
	}
	return result
}

// formatAuditReport renders the audit report as a human-readable table.
func formatAuditReport(report *auditReport) string {
	var b strings.Builder

	if len(report.Entries) == 0 {
		return ui.Muted.Render("No servers to audit.\n")
	}

	b.WriteString(ui.Label.Render("Security Audit Report") + "\n\n")

	cols := []ui.TableColumn{
		{Title: "SERVER", Width: 24, MaxWidth: 0},
		{Title: "VERSION", Width: 10, MaxWidth: 10},
		{Title: "ADVISORY", Width: 16, MaxWidth: 16},
		{Title: "SEVERITY", Width: 10, MaxWidth: 10},
		{Title: "FIXED IN", Width: 10, MaxWidth: 10},
		{Title: "TITLE", Width: 40, MaxWidth: 60},
	}

	var rows []ui.TableRow
	hasVulns := false

	for _, entry := range report.Entries {
		if len(entry.Advisories) == 0 && entry.Error == "" {
			rows = append(rows, ui.TableRow{
				ui.PackageName.Render(entry.Server),
				entry.Version,
				ui.Muted.Render("none"),
				ui.Success.Render("ok"),
				"",
				"",
			})
			continue
		}
		if entry.Error != "" {
			rows = append(rows, ui.TableRow{
				ui.PackageName.Render(entry.Server),
				entry.Version,
				ui.Muted.Render("error"),
				ui.Muted.Render("—"),
				"",
				truncateStr(entry.Error, 40),
			})
			continue
		}
		hasVulns = true
		for _, adv := range entry.Advisories {
			rows = append(rows, ui.TableRow{
				ui.PackageName.Render(entry.Server),
				entry.Version,
				adv.ID,
				severityStyle(adv.Severity),
				adv.FixedIn,
				truncateStr(adv.Title, 40),
			})
		}
	}

	b.WriteString(ui.RenderTable(cols, rows))
	b.WriteString("\n")

	if hasVulns {
		b.WriteString(ui.Error.Render(fmt.Sprintf("✗ %d vulnerable server(s) found.\n", report.Vulnerable)))
	} else {
		b.WriteString(ui.Success.Render("✓ No vulnerabilities found.\n"))
	}

	return b.String()
}

// severityStyle renders severity with appropriate colour.
func severityStyle(sev string) string {
	switch strings.ToLower(sev) {
	case "critical", "high":
		return ui.Error.Render(sev)
	case "moderate", "medium":
		return ui.Label.Render(sev)
	default:
		return ui.Muted.Render(sev)
	}
}

// truncateStr shortens a string to n runes.
func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func init() {
	auditCmd.Flags().BoolVar(&auditJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(auditCmd)
}
