package mcpserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
)

// Tool is one entry of the tools/list result (MCP tools/list shape).
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Input schemas (JSON Schema, draft-agnostic object shapes — the subset
// every MCP client validates against).
const (
	searchSchema = `{"type":"object","properties":{"query":{"type":"string","description":"Search text (package name or description keywords)"},"limit":{"type":"integer","minimum":1,"maximum":50,"default":10,"description":"Maximum number of results"},"transport":{"type":"string","description":"Filter by transport, e.g. stdio, http-sse, streamable-http"}},"required":["query"]}`

	infoSchema = `{"type":"object","properties":{"name":{"type":"string","description":"Package name, e.g. git-mcp or @scope/pkg"}},"required":["name"]}`

	listInstalledSchema = `{"type":"object","properties":{}}`

	installSchema = `{"type":"object","properties":{"name":{"type":"string","description":"Package to install, e.g. git-mcp or @scope/pkg"},"version":{"type":"string","description":"Exact version or semver range (^1.0.0, ~2.1.0, latest); default is latest"}},"required":["name"]}`
)

// buildTools fixes the tool surface. Decision (documented contract): when
// AllowInstall is false the install tool is OMITTED from tools/list — a
// tool that always errors is noise for clients — while it stays registered
// for tools/call, answering an isError result that names --allow-install,
// so clients that cached a differently-configured server's tools/list get
// an honest explanation instead of "unknown tool".
func buildTools(cfg Config) ([]Tool, map[string]toolDef) {
	defs := []toolDef{
		{
			spec: Tool{
				Name: "search",
				Description: "Search the PHAROS registry for MCP server packages. " +
					"Returns name, version, description, 30-day downloads, transports, and the security scorecard grade when scored.",
				InputSchema: json.RawMessage(searchSchema),
			},
			run: (*Server).runSearch,
		},
		{
			spec: Tool{
				Name: "info",
				Description: "Fetch full details for one registry package: description, publisher, versions, latest version, " +
					"transport, scorecard grade, and a ready-to-run pharos install hint.",
				InputSchema: json.RawMessage(infoSchema),
			},
			run: (*Server).runInfo,
		},
		{
			spec: Tool{
				Name:        "list_installed",
				Description: "List MCP servers installed and managed by Pharos on this machine (from pharos.lock). Read-only.",
				InputSchema: json.RawMessage(listInstalledSchema),
			},
			run: (*Server).runListInstalled,
		},
		{
			spec: Tool{
				Name: "install",
				Description: "Install a registry package with the same pipeline as 'pharos install' (semver resolve, " +
					"integrity-verified tarball, canonical config, MCP client configs, pharos.lock, dependency resolution). " +
					"Only available when the server was started with --allow-install.",
				InputSchema: json.RawMessage(installSchema),
			},
			run: (*Server).runInstall,
		},
	}
	byName := make(map[string]toolDef, len(defs))
	tools := make([]Tool, 0, len(defs))
	for _, d := range defs {
		byName[d.spec.Name] = d
		if d.spec.Name == "install" && !cfg.AllowInstall {
			continue // flag-gated: registered for tools/call, hidden from tools/list
		}
		tools = append(tools, d.spec)
	}
	return tools, byName
}

// ── shared helpers ──────────────────────────────────────────────────────────

// badParamsError marks argument problems: the protocol layer turns it
// into a JSON-RPC -32602 error instead of an isError tool result.
type badParamsError struct{ msg string }

func (e *badParamsError) Error() string { return e.msg }

func badParams(format string, a ...any) error {
	return &badParamsError{msg: fmt.Sprintf(format, a...)}
}

// decodeArgs unmarshals tool arguments, mapping shape failures to -32602.
func decodeArgs(args json.RawMessage, v any) error {
	if err := json.Unmarshal(args, v); err != nil {
		return badParams("invalid arguments: %v", err)
	}
	return nil
}

// registryClient returns the wired registry client or an honest error.
func (s *Server) registryClient() (*api.Client, error) {
	if s.cfg.Client == nil {
		return nil, fmt.Errorf("registry client not configured")
	}
	return s.cfg.Client, nil
}

// ── search ──────────────────────────────────────────────────────────────────

const (
	defaultSearchLimit = 10
	maxSearchLimit     = 50
)

// searchHit is one compact search row: exactly the fields an agent needs
// to decide whether to inspect further. No decoration.
type searchHit struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Downloads   int64    `json:"downloads"`
	Transports  []string `json:"transports"`
	// Grade is the scorecard grade (A–F) when the package is scored;
	// omitted otherwise.
	Grade string `json:"grade,omitempty"`
}

func (s *Server) runSearch(args json.RawMessage) (any, error) {
	var a struct {
		Query     string `json:"query"`
		Limit     int    `json:"limit"`
		Transport string `json:"transport"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(a.Query)
	if query == "" {
		return nil, badParams("search requires a non-empty query")
	}
	limit := a.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	client, err := s.registryClient()
	if err != nil {
		return nil, err
	}
	resp, err := client.Search(api.SearchParams{
		Query:     query,
		Limit:     limit,
		Transport: strings.TrimSpace(a.Transport),
	})
	if err != nil {
		return nil, fmt.Errorf("registry search failed: %w", err)
	}
	hits := make([]searchHit, 0, len(resp.Results))
	for _, r := range resp.Results {
		transports := make([]string, len(r.Transport))
		copy(transports, r.Transport)
		hit := searchHit{
			Name:        r.Name,
			Version:     r.Version,
			Description: r.Description,
			Downloads:   r.Downloads,
			Transports:  transports,
		}
		if r.Scorecard != nil {
			hit.Grade = r.Scorecard.Grade
		}
		hits = append(hits, hit)
	}
	return hits, nil
}

// ── info ────────────────────────────────────────────────────────────────────

type scorecardBrief struct {
	Score int    `json:"score"`
	Grade string `json:"grade"`
}

// infoDoc is the info tool payload: registry detail plus a ready-to-run
// install hint (data only — the client renders it however it wants).
type infoDoc struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	Publisher   string          `json:"publisher,omitempty"`
	Category    string          `json:"category,omitempty"`
	License     string          `json:"license,omitempty"`
	RepoURL     string          `json:"repo_url,omitempty"`
	Latest      string          `json:"latest,omitempty"`
	Versions    []string        `json:"versions"`
	Transport   string          `json:"transport,omitempty"`
	Scorecard   *scorecardBrief `json:"scorecard,omitempty"`
	InstallHint string          `json:"install_hint"`
}

func (s *Server) runInfo(args json.RawMessage) (any, error) {
	var a struct {
		Name string `json:"name"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(a.Name)
	if name == "" {
		return nil, badParams("info requires a package name")
	}
	client, err := s.registryClient()
	if err != nil {
		return nil, err
	}
	pkg, err := client.GetPackage(name)
	if err != nil {
		return nil, fmt.Errorf("lookup %q failed: %w", name, err)
	}

	doc := infoDoc{
		Name:        pkg.Name,
		Title:       pkg.Title,
		Description: pkg.Description,
		Publisher:   string(pkg.Publisher),
		Category:    pkg.Category,
		License:     pkg.License,
		RepoURL:     pkg.RepoURL,
		Latest:      pkg.DistTags["latest"],
		Versions:    pkg.VersionStrings(),
		Transport:   detailTransport(pkg),
	}
	if pkg.Scorecard != nil {
		doc.Scorecard = &scorecardBrief{Score: pkg.Scorecard.Score, Grade: pkg.Scorecard.Grade}
	}
	doc.InstallHint = "pharos install " + name
	if doc.Latest != "" {
		doc.InstallHint += "@" + doc.Latest
	}
	return doc, nil
}

// detailTransport picks the transport of the latest (or first) version.
func detailTransport(pkg *api.PackageDetail) string {
	if v := pkg.FindVersion(pkg.DistTags["latest"]); v != nil {
		if t := strings.TrimSpace(v.Manifest.Transport); t != "" {
			return t
		}
	}
	if len(pkg.Versions) > 0 {
		if t := strings.TrimSpace(pkg.Versions[0].Manifest.Transport); t != "" {
			return t
		}
	}
	return "stdio"
}

// ── list_installed ──────────────────────────────────────────────────────────

// installedEntry is one lockfile row for agents. Origin is the provenance
// kind (registry | adopted | manual); legacy entries (written before
// origin tracking) are reported as "registry", matching the W5.1 backfill
// rule update applies to them.
type installedEntry struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Origin  string   `json:"origin"`
	Pinned  bool     `json:"pinned"`
	Clients []string `json:"clients"`
}

func (s *Server) runListInstalled(args json.RawMessage) (any, error) {
	path, err := lockfile.DefaultPath()
	if err != nil {
		return nil, fmt.Errorf("resolve lockfile path: %w", err)
	}
	lf, err := lockfile.Load(path)
	if err != nil {
		return nil, fmt.Errorf("read lockfile: %w", err)
	}
	names := make([]string, 0, len(lf.Servers))
	for name := range lf.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]installedEntry, 0, len(names))
	for _, name := range names {
		e := lf.Servers[name]
		origin := lockfile.OriginKindRegistry
		if e.Origin != nil && e.Origin.Kind != "" {
			origin = e.Origin.Kind
		}
		clients := make([]string, len(e.Clients))
		copy(clients, e.Clients)
		entries = append(entries, installedEntry{
			Name:    name,
			Version: e.Version,
			Origin:  origin,
			Pinned:  e.PinnedAt != nil,
			Clients: clients,
		})
	}
	return entries, nil
}
