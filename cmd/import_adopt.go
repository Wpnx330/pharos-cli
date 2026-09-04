// W2.1 — `pharos import --adopt` (SPEC A2, one-command onboarding).
//
// Adopt reads every detected client config (or a single --from client),
// dedupes servers by name, resolves conflicts with the SAME field rules
// doctor --diff uses (driftFieldFindings / driftLooseEqual), and writes
// the managed baseline: pharos.lock (with the per-client Clients record)
// plus ~/.pharos/mcp.json (canonical). Source client configs are read,
// never rewritten — except the explicit "use everywhere" conflict choice.
//
// Key policy decisions (SPEC A2):
//   - Config-truth wins: servers unresolved in the registry still adopt
//     as managed (version "") — registry resolution is best-effort
//     enrichment only.
//   - Conflicts: materially different configs for the same server name
//     across clients. Interactive runs prompt (pick 1-N / u[N] / s);
//     --yes picks the FIRST client's config (Detect() order); JSON and
//     PHAROS_NON_INTERACTIVE runs skip conflicting servers (exit 1) and
//     adopt the rest.
//   - --dry-run computes the full report — including conflict
//     resolution — but writes nothing (no lockfile, no canonical, no
//     client rewrites).
package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/canonical"
	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

// Adopt row statuses (report + exit-code semantics).
const (
	adoptStatusAdopted      = "adopted"                // single config, taken as-is
	adoptStatusResolved     = "conflict-resolved"      // user picked a variant
	adoptStatusAutoResolved = "conflict-auto-resolved" // --yes picked the first client's config
	adoptStatusSkipped      = "conflict-skipped"       // conflict left unresolved (exit 1)

	adoptNextHint       = "Run 'pharos doctor --diff' to verify clean state."
	adoptNextHintPicked = "Run 'pharos doctor --diff' — clients keeping a different variant of a resolved conflict will show as drift."
)

// adoptOptions carries everything runAdoptImport needs. The cobra layer
// (import.go) builds it from flags; tests construct it directly.
type adoptOptions struct {
	Client string      // --client / --from ("" = every detected client)
	Yes    bool        // --yes (PHAROS_ASSUME_YES is honored separately)
	DryRun bool        // report only — write nothing
	API    *api.Client // registry enrichment (best-effort; nil allowed)
}

// adoptReport is the JSON shape of `import --adopt` (--json / PHAROS_JSON=1).
type adoptReport struct {
	Mode               string           `json:"mode"`
	DryRun             bool             `json:"dry_run"`
	Lockfile           string           `json:"lockfile"`
	Canonical          string           `json:"canonical,omitempty"`
	ClientsScanned     int              `json:"clients_scanned"`
	Found              int              `json:"found"`
	Adopted            int              `json:"adopted"`
	Conflicts          int              `json:"conflicts"`
	ConflictsResolved  int              `json:"conflicts_resolved"`
	ConflictsSkipped   int              `json:"conflicts_skipped"`
	UnresolvedRegistry int              `json:"unresolved_in_registry"`
	Servers            []adoptServerRow `json:"servers"`
	Warnings           []string         `json:"warnings,omitempty"`
	Next               string           `json:"next"`
}

// adoptServerRow is one unique server name in the adopt report.
type adoptServerRow struct {
	Name          string             `json:"name"`
	Clients       []string           `json:"clients"`
	Status        string             `json:"status"`
	Version       string             `json:"version,omitempty"`
	SourceClient  string             `json:"source_client,omitempty"`
	UseEverywhere bool               `json:"use_everywhere,omitempty"`
	Conflict      *adoptConflictInfo `json:"conflict,omitempty"`
}

// adoptConflictInfo lists every distinct config for a conflicted server
// (both/all configs are always reported) plus how it was resolved.
type adoptConflictInfo struct {
	Variants   []adoptVariantJSON `json:"variants"`
	Resolution string             `json:"resolution,omitempty"` // auto-first | picked | use-everywhere | skipped
}

type adoptVariantJSON struct {
	Clients []string        `json:"clients"`
	Config  json.RawMessage `json:"config"`
}

// adoptClientEntry is one (client, raw entry) observation.
type adoptClientEntry struct {
	Client clientconfig.Client
	Raw    json.RawMessage
	Cfg    clientconfig.ServerConfig
}

// adoptVariant groups the entries that are equivalent under the drift
// field rules. The first entry observed is the representative.
type adoptVariant struct {
	Raw     json.RawMessage           // representative entry as read
	Config  clientconfig.ServerConfig // representative entry parsed
	Clients []clientconfig.Client     // clients holding this exact config
}

// adoptStdin is the reader for interactive conflict prompts. Tests swap
// it to simulate stdin.
var adoptStdin io.Reader = os.Stdin

// runAdoptImport executes the adopt flow and returns the report plus an
// exit code (0 clean adopt, 1 when any conflict was skipped). A non-nil
// error means the adopt could not run at all (bad paths, corrupt state).
func runAdoptImport(opts adoptOptions) (*adoptReport, int, error) {
	yes := opts.Yes || AssumeYes()
	interactive := !JSONRequested() && !NonInteractive() && !yes

	var clients []clientconfig.Client
	if opts.Client != "" {
		c := clientconfig.DetectByID(clientconfig.ClientID(opts.Client))
		if c == nil {
			return nil, 1, fmt.Errorf("client %q not detected", opts.Client)
		}
		clients = append(clients, *c)
	} else {
		clients = clientconfig.Detect()
	}
	// Only clients whose config file exists have anything to adopt;
	// dir-only detections (Existing=false) would inflate the
	// "across M clients" count with empty scans.
	scannable := make([]clientconfig.Client, 0, len(clients))
	for _, c := range clients {
		if c.Existing {
			scannable = append(scannable, c)
		}
	}
	clients = scannable

	report := &adoptReport{
		Mode:           "adopt",
		DryRun:         opts.DryRun,
		ClientsScanned: len(clients),
		Servers:        []adoptServerRow{},
	}

	lockPath, err := adoptLockPath(opts.DryRun)
	if err != nil {
		return nil, 1, fmt.Errorf("cannot determine lockfile path: %w", err)
	}
	report.Lockfile = lockPath
	if canonPath, err := canonical.FilePath(); err == nil {
		report.Canonical = canonPath
	}

	lf, err := lockfile.Load(lockPath)
	if err != nil {
		// Unlike plain import (which silently resets a corrupt
		// lockfile), adopt is the managed-baseline write — surfacing
		// the corruption beats overwriting it.
		return nil, 1, fmt.Errorf("cannot read pharos.lock: %w", err)
	}
	canon, err := canonical.Load()
	if err != nil {
		return nil, 1, fmt.Errorf("cannot read ~/.pharos/mcp.json: %w", err)
	}

	// 1. Scan every client config. Entries are grouped by server name in
	//    Detect() order so "first client" is deterministic.
	groups := make(map[string][]adoptClientEntry)
	var names []string
	for _, c := range clients {
		if !JSONRequested() {
			fmt.Printf("%s  %s (%s)\n", ui.Label.Render("Scanning:"), c.Name, c.Path)
		}
		raws, err := clientconfig.ReadServersFormat(c.Path, c.Format)
		if err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: unreadable config: %v", c.Name, err))
			continue
		}
		for _, name := range sortedServerNames(raws) {
			entry := adoptClientEntry{Client: c, Raw: raws[name]}
			entry.Cfg = parseAdoptServerEntry(raws[name], c)
			if _, seen := groups[name]; !seen {
				names = append(names, name)
			}
			groups[name] = append(groups[name], entry)
		}
	}
	sort.Strings(names)
	report.Found = len(names)

	reader := bufio.NewReader(adoptStdin)
	canonDirty := false

	for _, name := range names {
		entries := groups[name]
		variants := groupAdoptVariants(name, entries)
		row := adoptServerRow{
			Name:    name,
			Clients: adoptClientIDs(entries),
		}

		if len(variants) == 1 {
			v := variants[0]
			row.Status = adoptStatusAdopted
			row.SourceClient = string(v.Clients[0].ID)
			adoptApply(&report.Warnings, lf, canon, &canonDirty, opts, name, v, entries, false)
		} else {
			row.Conflict = &adoptConflictInfo{Variants: adoptVariantJSONs(variants)}
			report.Conflicts++

			var picked int
			var everywhere bool
			switch {
			case yes:
				picked = 0
				row.Status = adoptStatusAutoResolved
				row.Conflict.Resolution = "auto-first"
				report.ConflictsResolved++
			case !interactive:
				row.Status = adoptStatusSkipped
				row.Conflict.Resolution = "skipped"
				report.ConflictsSkipped++
			default:
				idx, ev, skip := promptAdoptConflict(os.Stdout, reader, name, variants)
				if skip {
					row.Status = adoptStatusSkipped
					row.Conflict.Resolution = "skipped"
					report.ConflictsSkipped++
				} else {
					picked, everywhere = idx, ev
					row.Status = adoptStatusResolved
					row.Conflict.Resolution = "picked"
					if everywhere {
						row.Conflict.Resolution = "use-everywhere"
						row.UseEverywhere = true
					}
					report.ConflictsResolved++
				}
			}
			if row.Status == adoptStatusSkipped {
				// Nothing was adopted, but if the server was already
				// managed (previous install) the untouched lockfile
				// entry's version is still the honest row state.
				if entry, ok := lf.Get(name); ok {
					row.Version = entry.Version
				}
				report.Servers = append(report.Servers, row)
				continue
			}
			v := variants[picked]
			row.SourceClient = string(v.Clients[0].ID)
			adoptApply(&report.Warnings, lf, canon, &canonDirty, opts, name, v, entries, everywhere)
		}

		if entry, ok := lf.Get(name); ok {
			row.Version = entry.Version
		}
		if row.Version == "" {
			report.UnresolvedRegistry++
		}
		report.Adopted++
		report.Servers = append(report.Servers, row)
	}

	if !opts.DryRun {
		if err := lf.Save(lockPath); err != nil {
			return nil, 1, fmt.Errorf("write lockfile: %w", err)
		}
		if canonDirty {
			if err := canonical.Save(canon); err != nil {
				return nil, 1, fmt.Errorf("write canonical config: %w", err)
			}
		}
	}

	report.Next = adoptNextHint
	code := 0
	if report.ConflictsSkipped > 0 {
		code = 1
	}
	return report, code, nil
}

// adoptLockPath resolves the pharos.lock path for an adopt run. A live
// adopt uses lockfile.DefaultPath verbatim — including its CreateTemp
// writability probe, which is honest work for a run about to write. A
// dry-run promises to write nothing, so the path is predicted read-only
// (os.Getwd only — the doctor --diff read-only precedent):
// DefaultPath prefers cwd/pharos.lock whenever cwd is writable, so the
// cwd candidate is reported; only when cwd cannot even be resolved does
// it fall back to DefaultPath's own resolution.
func adoptLockPath(dryRun bool) (string, error) {
	if dryRun {
		if cwd, err := os.Getwd(); err == nil {
			return filepath.Join(cwd, "pharos.lock"), nil
		}
	}
	return lockfile.DefaultPath()
}

// adoptApply records one adopted server in the lockfile and canonical
// config, and performs the "use everywhere" client rewrites when asked.
// Dry-run opts skip every write.
func adoptApply(warnings *[]string, lf *lockfile.Lockfile, canon *canonical.Config, canonDirty *bool, opts adoptOptions, name string, v adoptVariant, entries []adoptClientEntry, everywhere bool) {
	version, integrity, regTransport := adoptResolveRegistry(opts.API, name)
	transport := regTransport
	if transport == "" {
		transport = adoptTransport(v.Config)
	}

	clientIDs := adoptClientIDs(entries)
	if prev, ok := lf.Get(name); ok {
		clientIDs = adoptUnionClientIDs(prev.Clients, clientIDs)
	}
	lf.Set(name, lockfile.ServerEntry{
		Version:     version,
		Integrity:   integrity,
		Transport:   transport,
		InstalledAt: time.Now().UTC(),
		Clients:     clientIDs,
	})

	canon.Servers[name] = adoptCanonicalServer(name, v.Config, version, integrity)
	*canonDirty = true

	if everywhere && !opts.DryRun {
		for _, e := range entries {
			// Clients already holding the picked config are not rewritten.
			if adoptDerivedEqual(name, e.Client, v.Config, e.Raw) {
				continue
			}
			if err := clientconfig.MergeServer(e.Client, name, v.Config); err != nil {
				*warnings = append(*warnings, fmt.Sprintf("%s: %v", e.Client.Name, err))
			}
		}
	}
}

// adoptResolveRegistry best-effort resolves name against the registry,
// mirroring plain import's enrichment (latest dist-tag version, tarball
// integrity, transport from the first listed version). Any failure
// yields empty strings — the config stays the source of truth.
func adoptResolveRegistry(apiClient *api.Client, name string) (version, integrity, transport string) {
	if apiClient == nil {
		return "", "", ""
	}
	pkg, err := apiClient.GetPackage(name)
	if err != nil {
		return "", "", ""
	}
	if pkg.DistTags != nil {
		version = pkg.DistTags["latest"]
	}
	if len(pkg.Versions) > 0 {
		transport = pkg.Versions[0].Manifest.Transport
	}
	if vd := pkg.FindVersion(version); vd != nil {
		integrity = vd.Manifest.Integrity
	}
	return version, integrity, transport
}

// adoptTransport derives the transport recorded in the lockfile/canonical
// from the adopted client config itself.
func adoptTransport(cfg clientconfig.ServerConfig) string {
	if strings.TrimSpace(cfg.URL) != "" {
		if strings.EqualFold(strings.TrimSpace(cfg.Type), "sse") {
			return "sse"
		}
		return "http"
	}
	return "stdio"
}

// adoptCanonicalServer builds the canonical record for an adopted server.
// The shape is exactly what doctor --diff's driftExpectedEntry re-derives
// through install.BuildClientConfig + clientconfig.ExpectedEntry, so an
// untouched client config reads as clean after adopt.
func adoptCanonicalServer(name string, cfg clientconfig.ServerConfig, version, integrity string) canonical.Server {
	srv := canonical.Server{
		Transport:   adoptTransport(cfg),
		Command:     cfg.Command,
		Args:        cfg.Args,
		Env:         cfg.Env,
		URL:         cfg.URL,
		Package:     canonical.PackageInfo{Name: name, Version: version, Integrity: integrity, Source: "pharos"},
		Enabled:     true,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}
	if len(srv.Args) == 0 {
		srv.Args = nil
	}
	if len(srv.Env) == 0 {
		srv.Env = nil
	}
	return srv
}

// parseAdoptServerEntry parses a raw client-config entry into the logical
// ServerConfig. Handles every format quirk the readers produce:
// OpenCode command arrays and "KEY=VALUE" env arrays, TOML env numbers,
// and Grok remote `headers` (compared with env semantics by doctor).
func parseAdoptServerEntry(raw json.RawMessage, c clientconfig.Client) clientconfig.ServerConfig {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return clientconfig.ServerConfig{}
	}
	cfg := clientconfig.ServerConfig{}
	if s, ok := m["command"].(string); ok {
		cfg.Command = s
	}
	if arr, ok := m["command"].([]any); ok {
		for i, a := range arr {
			s, ok := a.(string)
			if !ok {
				continue
			}
			if i == 0 && cfg.Command == "" {
				cfg.Command = s
			} else {
				cfg.Args = append(cfg.Args, s)
			}
		}
	}
	if arr, ok := m["args"].([]any); ok {
		for _, a := range arr {
			if s, ok := a.(string); ok {
				cfg.Args = append(cfg.Args, s)
			}
		}
	}
	switch env := m["env"].(type) {
	case map[string]any:
		cfg.Env = adoptStringMap(env)
	case []any:
		cfg.Env = adoptKVArray(env)
	}
	if s, ok := m["url"].(string); ok {
		cfg.URL = s
	}
	if s, ok := m["type"].(string); ok {
		cfg.Type = s
	}
	if c.Format == clientconfig.FormatTOML && c.ID == clientconfig.ClientGrok {
		if headers, ok := m["headers"].(map[string]any); ok {
			h := adoptStringMap(headers)
			if len(h) > 0 {
				if cfg.Env == nil {
					cfg.Env = h
				} else {
					for k, v := range h {
						cfg.Env[k] = v
					}
				}
			}
		}
	}
	return cfg
}

// adoptStringMap converts a generic JSON object to env strings. TOML/YAML
// readers surface unquoted values as float64/bool; they are formatted the
// same way driftLooseEqual compares them ("%v").
func adoptStringMap(m map[string]any) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		switch t := v.(type) {
		case string:
			out[k] = t
		case float64:
			out[k] = strconv.FormatFloat(t, 'f', -1, 64)
		case bool:
			out[k] = strconv.FormatBool(t)
		}
	}
	return out
}

// adoptKVArray parses OpenCode's env as ["KEY=VALUE", ...].
func adoptKVArray(arr []any) map[string]string {
	out := make(map[string]string, len(arr))
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			continue
		}
		if idx := strings.Index(s, "="); idx > 0 {
			out[s[:idx]] = s[idx+1:]
		}
	}
	return out
}

// groupAdoptVariants buckets entries into distinct configs. Equivalence
// is judged with the doctor --diff field rules, so cosmetic-only
// differences (key order, formatting, numeric env spellings) never
// become conflicts.
func groupAdoptVariants(name string, entries []adoptClientEntry) []adoptVariant {
	var variants []adoptVariant
	for _, e := range entries {
		matched := false
		for i := range variants {
			if adoptEquivalent(name, variants[i], e) {
				variants[i].Clients = append(variants[i].Clients, e.Client)
				matched = true
				break
			}
		}
		if !matched {
			variants = append(variants, adoptVariant{
				Raw:     e.Raw,
				Config:  e.Cfg,
				Clients: []clientconfig.Client{e.Client},
			})
		}
	}
	return variants
}

// adoptEquivalent reports whether entry b holds the same logical server
// config as variant a. Both adoption directions are checked: deriving
// what pharos would write to b's client from a's config (and vice versa)
// must match the actual file content. That is exactly the comparison
// doctor --diff performs per client, applied cross-client, so anything
// it would flag as drift counts as a conflict here. Clients that cannot
// represent the shape (ExpectedEntry skip/error) fall back to a raw
// field comparison.
func adoptEquivalent(name string, a adoptVariant, b adoptClientEntry) bool {
	if !adoptDerivedEqual(name, b.Client, a.Config, b.Raw) {
		return false
	}
	return adoptDerivedEqual(name, a.Clients[0], b.Cfg, a.Raw)
}

// adoptDerivedEqual compares the entry pharos would serialize for client
// c from cfg against the raw entry actually present in c's config. The
// headerKey is passed in both paths: when ExpectedEntry succeeds, Grok's
// serialized `headers` must still be compared with env semantics so
// numeric TOML values don't false-conflict via strict DeepEqual.
func adoptDerivedEqual(name string, c clientconfig.Client, cfg clientconfig.ServerConfig, raw json.RawMessage) bool {
	expected, err := clientconfig.ExpectedEntry(c, name, cfg)
	if err != nil {
		data, merr := json.Marshal(cfg)
		if merr != nil {
			return false
		}
		return len(driftFieldFindings(name, data, raw, adoptHeaderKey(c))) == 0
	}
	return len(driftFieldFindings(name, expected, raw, adoptHeaderKey(c))) == 0
}

// adoptHeaderKey names the extra field compared with env semantics in
// raw-fallback comparisons (Grok remote `headers`).
func adoptHeaderKey(c clientconfig.Client) string {
	if c.Format == clientconfig.FormatTOML && c.ID == clientconfig.ClientGrok {
		return "headers"
	}
	return ""
}

// promptAdoptConflict renders the conflict and reads a choice from r.
// Inputs: a variant number 1-N (adopt that config), "u" / "u<N>"
// (adopt variant N — default 1 — and write it to every client that has
// the server), or "s" (skip). Invalid input re-asks — including an
// out-of-range u<N>, which never silently falls back to variant 1; EOF
// skips.
func promptAdoptConflict(w io.Writer, r *bufio.Reader, name string, variants []adoptVariant) (pick int, everywhere bool, skip bool) {
	fmt.Fprintf(w, "\n%s  %s — %d distinct configs\n", ui.Warning.Render("Conflict:"), name, len(variants))
	for i, v := range variants {
		fmt.Fprintf(w, "  [%d] %s  %s\n", i+1, v.Clients[0].Name, adoptCompactJSON(v.Raw))
		if i > 0 {
			for _, f := range driftFieldFindings(name, variants[0].Raw, v.Raw, "") {
				fmt.Fprintf(w, "      %s: %s → %s\n", f.Field, f.Expected, f.Actual)
			}
		}
	}
	for {
		fmt.Fprintf(w, "  Pick 1-%d, u[N] = use N everywhere, s = skip: ", len(variants))
		line, err := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" && err != nil {
			return 0, false, true
		}
		lower := strings.ToLower(line)
		switch {
		case lower == "s" || lower == "skip":
			return 0, false, true
		case lower == "u" || lower == "use":
			return 0, true, false
		case strings.HasPrefix(lower, "u"):
			if n, convErr := strconv.Atoi(strings.TrimSpace(lower[1:])); convErr == nil {
				if n >= 1 && n <= len(variants) {
					return n - 1, true, false
				}
				// Out-of-range u<N> re-asks, same as a bare
				// out-of-range variant number.
				continue
			}
			return 0, true, false
		}
		if n, convErr := strconv.Atoi(line); convErr == nil && n >= 1 && n <= len(variants) {
			return n - 1, false, false
		}
	}
}

// adoptVariantJSONs renders variants for the report, compacting the raw
// configs for stable machine output.
func adoptVariantJSONs(variants []adoptVariant) []adoptVariantJSON {
	out := make([]adoptVariantJSON, 0, len(variants))
	for _, v := range variants {
		out = append(out, adoptVariantJSON{
			Clients: adoptClientIDsFromClients(v.Clients),
			Config:  json.RawMessage(adoptCompactJSON(v.Raw)),
		})
	}
	return out
}

// adoptClientIDs returns the sorted unique client IDs of the entries.
func adoptClientIDs(entries []adoptClientEntry) []string {
	set := make(map[string]bool, len(entries))
	for _, e := range entries {
		set[string(e.Client.ID)] = true
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// adoptUnionClientIDs merges two client-ID lists into a sorted unique one.
func adoptUnionClientIDs(a, b []string) []string {
	set := make(map[string]bool, len(a)+len(b))
	for _, id := range a {
		set[id] = true
	}
	for _, id := range b {
		set[id] = true
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// adoptClientIDsFromClients returns the sorted unique IDs of clients.
func adoptClientIDsFromClients(clients []clientconfig.Client) []string {
	set := make(map[string]bool, len(clients))
	for _, c := range clients {
		set[string(c.ID)] = true
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// adoptCompactJSON renders a raw entry as a single line.
func adoptCompactJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

// printAdoptJSON emits the adopt report as JSON on stdout (W1.1: pure
// stdout JSON, never a prompt).
func printAdoptJSON(report *adoptReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// printAdoptHuman renders the human-facing adopt report.
func printAdoptHuman(report *adoptReport) {
	for _, row := range report.Servers {
		clients := strings.Join(row.Clients, ", ")
		switch row.Status {
		case adoptStatusAdopted:
			fmt.Printf("  %s  %s — adopted (%s)%s\n",
				ui.Success.Render("✓"), ui.PackageName.Render(row.Name), clients, adoptVersionSuffix(row))
		case adoptStatusResolved:
			extra := ""
			if row.UseEverywhere {
				extra = ", written to all clients"
			}
			fmt.Printf("  %s  %s — conflict resolved: using %s's config (%s)%s%s\n",
				ui.Warning.Render("≈"), ui.PackageName.Render(row.Name), row.SourceClient, clients, extra, adoptVersionSuffix(row))
		case adoptStatusAutoResolved:
			fmt.Printf("  %s  %s — conflict auto-resolved: using %s's config (%s)%s\n",
				ui.Warning.Render("≈"), ui.PackageName.Render(row.Name), row.SourceClient, clients, adoptVersionSuffix(row))
		case adoptStatusSkipped:
			fmt.Printf("  %s  %s — conflict skipped (%s have different configs)\n",
				ui.Error.Render("✗"), ui.PackageName.Render(row.Name), clients)
		}
	}
	fmt.Println()

	summary := fmt.Sprintf("%d servers across %d clients; %d adopted", report.Found, report.ClientsScanned, report.Adopted)
	if report.Conflicts > 0 {
		summary += fmt.Sprintf(", %d conflict(s) (%d resolved, %d skipped)", report.Conflicts, report.ConflictsResolved, report.ConflictsSkipped)
	}
	if report.UnresolvedRegistry > 0 {
		summary += fmt.Sprintf(", %d unresolved in registry (still adopted)", report.UnresolvedRegistry)
	}
	if report.DryRun {
		fmt.Printf("%s  %s\n", ui.Label.Render("Adopt preview."), summary)
		fmt.Printf("%s\n", ui.Muted.Render("Dry run — nothing written."))
	} else {
		fmt.Printf("%s  %s\n", ui.Success.Render("✓ Adopt complete."), summary)
	}
	for _, w := range report.Warnings {
		fmt.Printf("%s  %s\n", ui.Warning.Render("Warning:"), w)
	}
	fmt.Printf("%s  %s\n", ui.Muted.Render("Lockfile:"), report.Lockfile)
	fmt.Printf("%s  %s\n", ui.Muted.Render("Next:"), adoptHumanNextHint(report))
}

// adoptHumanNextHint picks the human-mode Next hint for a finished adopt
// report (the JSON "next" field keeps the plain clean-state hint, so the
// machine contract stays stable). A conflict resolved by picking one
// variant without "use everywhere" — an interactive pick or --yes
// auto-first — leaves the unpicked clients on their own config, which
// doctor --diff honestly reports as drift; the hint must say so instead
// of promising a clean read.
func adoptHumanNextHint(report *adoptReport) string {
	for _, row := range report.Servers {
		if row.Conflict == nil || row.Status == adoptStatusSkipped {
			continue
		}
		if !row.UseEverywhere {
			return adoptNextHintPicked
		}
	}
	return adoptNextHint
}

// adoptVersionSuffix annotates a report row with registry state.
func adoptVersionSuffix(row adoptServerRow) string {
	if row.Version == "" {
		return " — not in registry (adopted as managed)"
	}
	return " @" + row.Version
}
