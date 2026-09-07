// W5.1 A6 — origin-aware update extras.
//
// `pharos update --check` derives repo/changelog links from data the
// update loop ALREADY has: the lockfile entry's Origin.Ref and the
// packument's repo_url fetched by GetPackage. No extra network calls,
// and links are only derived for known git hosts (github/gitlab) —
// pharos never fabricates URLs for hosts it does not know the URL
// shape of.
package cmd

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

// knownGitHosts maps a git host to its releases path segment. A repo
// URL on any other host yields NO links (honest absence over guesses).
var knownGitHosts = map[string]string{
	"github.com": "releases",
	"gitlab.com": "-/releases", // GitLab nests releases under /-/
}

// normalizeRepoURL cleans a repository reference into a comparable
// browse URL: strips "git+" prefixes (npm-style), "git@" scp-style
// hosts are NOT rewritten (they rarely appear in registry data and
// rewriting risks fabrication), trims whitespace and a trailing
// ".git" suffix.
func normalizeRepoURL(raw string) string {
	repo := strings.TrimSpace(raw)
	repo = strings.TrimPrefix(repo, "git+")
	repo = strings.TrimSuffix(repo, ".git")
	return repo
}

// gitHostReleasesBase parses a repo URL and, when its host is a known
// git host (scheme http/https), returns the normalized repo URL base
// suitable for building links. ok=false for anything else — including
// non-URLs (e.g. a registry origin Ref like "name@1.0.0"), unknown
// hosts, and missing schemes.
func gitHostReleasesBase(raw string) (repo string, releasesPath string, ok bool) {
	repo = normalizeRepoURL(raw)
	if repo == "" {
		return "", "", false
	}
	u, err := url.Parse(repo)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", "", false
	}
	path, known := knownGitHosts[strings.ToLower(u.Host)]
	if !known {
		return "", "", false
	}
	return repo, path, true
}

// deriveChangelogURL returns the changelog link for a known-host repo:
// the releases page (github: <repo>/releases, gitlab: <repo>/-/releases).
// A CHANGELOG.md blob link is deliberately NOT derived: whether the file
// exists (or on which branch) is unknowable without a fetch, and the
// releases page is derivable from the URL shape alone.
func deriveChangelogURL(repo, releasesPath string) string {
	return repo + "/" + releasesPath
}

// deriveRepoLinks derives the repo + changelog links for one server.
//
// Source order (backfill rule in action): a nil Origin (legacy entry)
// or a registry origin (Ref = "name@version", not a URL) both fall
// through to the packument's repo_url — legacy entries are treated as
// registry installs, so they get exactly the registry derivation. An
// adopted origin carrying a repo URL in Ref wins when it is a known
// git host. Unknown hosts and unparseable refs yield empty strings.
func deriveRepoLinks(entry lockfile.ServerEntry, pkg *api.PackageDetail) (repo, changelog string) {
	if entry.Origin != nil {
		if base, releases, ok := gitHostReleasesBase(entry.Origin.Ref); ok {
			return base, deriveChangelogURL(base, releases)
		}
	}
	if pkg != nil {
		if base, releases, ok := gitHostReleasesBase(pkg.RepoURL); ok {
			return base, deriveChangelogURL(base, releases)
		}
	}
	return "", ""
}

// printCheckOriginExtras renders the --check repo/changelog lines after
// a server's preview row (human mode only; JSON carries repo/changelog
// fields instead).
func printCheckOriginExtras(repo, changelog string) {
	if repo == "" {
		return
	}
	if changelog != "" {
		fmt.Printf("      %s %s  %s %s\n", ui.Muted.Render("repo:"), repo, ui.Muted.Render("changelog:"), changelog)
		return
	}
	fmt.Printf("      %s %s\n", ui.Muted.Render("repo:"), repo)
}
