package cmd

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/canonical"
	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/install"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
)

// W1.4 — config drift detection (`pharos doctor --diff`, SPEC B3).
//
// Drift = a pharos-managed server entry in a client config no longer
// matches what pharos last wrote. The baseline is the intersection of
// pharos.lock (which servers pharos manages) and ~/.pharos/mcp.json (the
// canonical config recording the command/args/env/url/transport values the
// install path fed into every client write). The expected per-client entry
// is re-derived through the EXACT install-path helpers —
// install.BuildClientConfig for the server shape and
// clientconfig.ExpectedEntry for the per-format serialization — then
// compared field-by-field against the current config content read via
// clientconfig.ReadServersFormat. Everything here is read-only.
//
// Deliberate choices:
//   - Only clients whose config references at least one lockfile server
//     get a "Config drift: <client>" check (clients pharos never wrote to
//     are skipped silently — absence = untouched). Consequence: a client
//     whose LAST managed entry is hand-removed also reads as untouched.
//   - Compared fields are command, args, env, url, and type only. Other
//     keys inside an entry (Zed/Aider user settings, Hermes `enabled`)
//     and unknown top-level config keys are the user's business.
//   - Comparison runs on normalized JSON: key order, whitespace, and the
//     spelling of empty containers (absent vs null vs {}) never count as
//     drift — a pure reformat is not drift.
//   - Servers present in a config but not in the lockfile are reported as
//     INFO findings ("unmanaged (hand-added?)") and do not fail the check.
//   - A server whose canonical entry cannot be re-derived (no command and
//     no URL, or a client that cannot represent it — e.g. Claude Desktop
//     remotes) is skipped for that client: pharos would not have written
//     it there either.

// doctorDiff is bound to the --diff flag on `pharos doctor`.
var doctorDiff bool

func init() {
	doctorCmd.Flags().BoolVar(&doctorDiff, "diff", false,
		"compare pharos-managed server entries in client configs against the pharos.lock baseline (read-only)")
}

// Finding kinds and severities for drift results.
const (
	driftKindMissing  = "missing"  // managed server absent from the client config
	driftKindModified = "modified" // entry present but a field differs
	driftKindExtra    = "extra"    // config entry not managed by pharos

	driftSeverityError = "error" // fails the drift check
	driftSeverityInfo  = "info"  // reported, check still passes
)

// driftComparedFields are the entry fields drift detection compares, in
// report order. Everything else inside a server entry is ignored.
var driftComparedFields = []string{"command", "args", "env", "url", "type"}

// doctorFinding is one drift observation inside a doctor check. Findings
// are attached to "Config drift: <client>" checks and appear in the JSON
// report (additive field on doctorCheck) and as indented lines in the
// human output.
type doctorFinding struct {
	Server   string `json:"server"`
	Kind     string `json:"kind"`               // missing | modified | extra
	Severity string `json:"severity"`           // error | info
	Field    string `json:"field,omitempty"`    // e.g. "env.PHAROS_TOKEN", "args[2]"
	Expected string `json:"expected,omitempty"` // truncated, JSON-encoded
	Actual   string `json:"actual,omitempty"`   // truncated, JSON-encoded
	Message  string `json:"message"`
}

// runDriftChecks builds the "Config drift: <client>" checks for every
// detected existing client that references at least one lockfile server.
// The second return value is an optional human-facing note for the
// nothing-to-compare cases; it is never emitted under JSON output.
func runDriftChecks() ([]doctorCheck, string) {
	lf, canon, managed, err := loadDriftBaseline()
	if err != nil {
		return []doctorCheck{{
			Name:   "Config drift",
			Status: "fail",
			Error:  err.Error(),
		}}, ""
	}
	if len(managed) == 0 {
		return nil, "no pharos-managed servers in pharos.lock + ~/.pharos/mcp.json — nothing to compare"
	}

	var checks []doctorCheck
	for _, c := range clientconfig.Detect() {
		if !c.Existing {
			continue
		}
		c := c // capture for closure
		if check, ok := driftCheckForClient(c, lf, canon, managed); ok {
			checks = append(checks, check)
		}
	}
	if len(checks) == 0 {
		return nil, "no client configs reference pharos-managed servers — nothing to compare"
	}
	return checks, ""
}

// loadDriftBaseline loads the lockfile and canonical config and returns
// the sorted list of server names that are both locked and canonical —
// the servers drift detection can verify. A lockfile that does not exist
// (or an empty/canonical-less state) yields zero managed servers; a
// corrupt one is an error so the failure is visible.
func loadDriftBaseline() (*lockfile.Lockfile, *canonical.Config, []string, error) {
	lockPath, err := lockfile.DefaultPath()
	if err != nil {
		// Cannot even locate a lockfile — nothing to diff against.
		return nil, nil, nil, nil
	}
	lf, err := lockfile.Load(lockPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot read pharos.lock: %w", err)
	}
	canon, err := canonical.Load()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot read ~/.pharos/mcp.json: %w", err)
	}

	var managed []string
	for name := range lf.Servers {
		if _, ok := canon.Servers[name]; ok {
			managed = append(managed, name)
		}
	}
	sort.Strings(managed)
	return lf, canon, managed, nil
}

// driftCheckForClient compares one client config against the baseline. The
// second return value is false when the client is skipped silently (its
// config references no lockfile servers — pharos never wrote to it).
func driftCheckForClient(c clientconfig.Client, lf *lockfile.Lockfile, canon *canonical.Config, managed []string) (doctorCheck, bool) {
	check := doctorCheck{Name: fmt.Sprintf("Config drift: %s", c.Name)}

	servers, err := clientconfig.ReadServersFormat(c.Path, c.Format)
	if err != nil {
		check.Status = "fail"
		check.Error = fmt.Sprintf("config unreadable: %v", err)
		return check, true
	}

	// Skip clients pharos has never written to (no lockfile overlap).
	relevant := false
	for name := range servers {
		if lf.Has(name) {
			relevant = true
			break
		}
	}
	if !relevant {
		return doctorCheck{}, false
	}

	var findings []doctorFinding
	checked := 0
	for _, name := range managed {
		actual, present := servers[name]
		srv := canon.Servers[name]
		expected, derivable := driftExpectedEntry(name, srv, c)
		if !derivable {
			continue // no baseline for this client — cannot judge
		}
		if !present {
			findings = append(findings, doctorFinding{
				Server:   name,
				Kind:     driftKindMissing,
				Severity: driftSeverityError,
				Message:  fmt.Sprintf("server '%s' is missing from this config (managed in pharos.lock)", name),
			})
			continue
		}
		checked++
		findings = append(findings, driftFieldFindings(name, expected, actual)...)
	}

	extras := 0
	for _, name := range sortedServerNames(servers) {
		if !lf.Has(name) {
			extras++
			findings = append(findings, doctorFinding{
				Server:   name,
				Kind:     driftKindExtra,
				Severity: driftSeverityInfo,
				Message:  fmt.Sprintf("server '%s' is not managed by pharos (not in pharos.lock) — unmanaged (hand-added?)", name),
			})
		}
	}

	check.Findings = findings
	drifted := 0
	for _, f := range findings {
		if f.Severity == driftSeverityError {
			drifted++
		}
	}
	if drifted > 0 {
		check.Status = "fail"
		check.Error = fmt.Sprintf("%d drift finding(s) in this config", drifted)
	} else {
		check.Status = "ok"
		detail := fmt.Sprintf("%d managed server(s) match", checked)
		if extras > 0 {
			detail += fmt.Sprintf("; %d unmanaged noted", extras)
		}
		check.Detail = detail
	}
	return check, true
}

// driftExpectedEntry re-derives the entry pharos would write for name in
// client c today, from the canonical record. It reconstructs the registry
// manifest shape from the canonical fields and pushes it through the same
// helpers the install path uses (install.BuildClientConfig then
// clientconfig.ExpectedEntry). The second return value is false when no
// reliable expectation exists: the canonical entry has neither URL nor
// command, does not classify to an installable kind, or the client cannot
// represent it (SkipError — pharos skipped that client at install time).
func driftExpectedEntry(name string, srv canonical.Server, c clientconfig.Client) (json.RawMessage, bool) {
	if strings.TrimSpace(srv.URL) == "" && strings.TrimSpace(srv.Command) == "" {
		return nil, false
	}
	manifest := api.Manifest{
		Name:      name,
		Version:   srv.Package.Version,
		Transport: srv.Transport,
		Endpoint:  srv.URL,
		Command:   srv.Command,
		Args:      srv.Args,
		Env:       srv.Env,
	}
	if install.ClassifyManifest(manifest) == install.KindNone {
		return nil, false
	}
	clientCfg := install.BuildClientConfig(manifest, "")
	raw, err := clientconfig.ExpectedEntry(c, name, clientCfg)
	if err != nil {
		return nil, false
	}
	return raw, true
}

// driftFieldFindings diffs expected against actual for the compared
// fields and returns one finding per divergence. Both raws are JSON
// entry values (as produced by clientconfig.ExpectedEntry and
// clientconfig.ReadServersFormat).
func driftFieldFindings(name string, expected, actual json.RawMessage) []doctorFinding {
	exp := entryFieldMap(expected)
	act := entryFieldMap(actual)

	var findings []doctorFinding
	add := func(field string, ev, ae any) {
		findings = append(findings, doctorFinding{
			Server:   name,
			Kind:     driftKindModified,
			Severity: driftSeverityError,
			Field:    field,
			Expected: formatDriftValue(ev),
			Actual:   formatDriftValue(ae),
			Message: fmt.Sprintf("server '%s' modified: %s: expected %s, got %s",
				name, field, formatDriftValue(ev), formatDriftValue(ae)),
		})
	}

	for _, field := range driftComparedFields {
		ev := exp[field] // absent → nil
		av := act[field]
		if ev == nil && av == nil {
			continue
		}
		if reflect.DeepEqual(ev, av) {
			continue
		}
		switch field {
		case "env":
			findEnvDivergences(name, ev, av, add)
		case "args":
			findArgDivergences(name, ev, av, add)
		default:
			add(field, ev, av)
		}
	}
	return findings
}

// findEnvDivergences reports per-key env differences (the common hand-edit:
// a changed variable value), falling back to a single field finding when
// either side is not an object.
func findEnvDivergences(name string, ev, av any, add func(field string, exp, act any)) {
	expMap, expOK := ev.(map[string]any)
	actMap, actOK := av.(map[string]any)
	if !expOK || !actOK {
		add("env", ev, av)
		return
	}
	keys := make([]string, 0, len(expMap)+len(actMap))
	seen := make(map[string]bool, len(expMap)+len(actMap))
	for k := range expMap {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for k := range actMap {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		expVal, inExp := expMap[k]
		actVal, inAct := actMap[k]
		switch {
		case inExp && inAct && reflect.DeepEqual(expVal, actVal):
			continue
		case inExp && !inAct:
			add("env."+k, expVal, nil)
		case !inExp && inAct:
			add("env."+k, nil, actVal)
		default:
			add("env."+k, expVal, actVal)
		}
	}
}

// findArgDivergences reports the first differing index plus a length
// mismatch when the argument lists diverge.
func findArgDivergences(name string, ev, av any, add func(field string, exp, act any)) {
	expArgs, expOK := ev.([]any)
	actArgs, actOK := av.([]any)
	if !expOK || !actOK {
		add("args", ev, av)
		return
	}
	n := len(expArgs)
	if len(actArgs) < n {
		n = len(actArgs)
	}
	for i := 0; i < n; i++ {
		if !reflect.DeepEqual(expArgs[i], actArgs[i]) {
			add(fmt.Sprintf("args[%d]", i), expArgs[i], actArgs[i])
			return
		}
	}
	if len(expArgs) != len(actArgs) {
		add("args", ev, av)
	}
}

// entryFieldMap parses an entry's raw JSON into a field map with empty
// containers normalized to nil (absent == null == {} == []).
func entryFieldMap(raw json.RawMessage) map[string]any {
	m, _ := normalizeDriftValue(raw).(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

// normalizeDriftValue unmarshals raw JSON into generic Go values with
// empty maps/slices collapsed to nil so cosmetic shape never counts as
// drift. Values that do not parse come back as their literal string.
func normalizeDriftValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	return collapseEmpty(v)
}

func collapseEmpty(v any) any {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 {
			return nil
		}
		for k, e := range t {
			t[k] = collapseEmpty(e)
		}
		return t
	case []any:
		if len(t) == 0 {
			return nil
		}
		for i, e := range t {
			t[i] = collapseEmpty(e)
		}
		return t
	default:
		return v
	}
}

// formatDriftValue renders a normalized value as compact JSON, truncated
// for message width.
func formatDriftValue(v any) string {
	if v == nil {
		return "(absent)"
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return truncateDrift(string(data))
}

func truncateDrift(s string) string {
	const max = 80
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// sortedServerNames returns a config's server names sorted for
// deterministic finding order.
func sortedServerNames(servers map[string]json.RawMessage) []string {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
