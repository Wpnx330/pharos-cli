package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/daemon"
)

// ── Test fixtures ────────────────────────────────────────────────────────

// fakeProbes answers liveness/RSS from fixed maps so report building is
// deterministic. RSS reports ok=true only for keys present in rss.
type fakeProbes struct {
	alive map[int]bool
	rss   map[int]int64
}

func (f fakeProbes) Alive(pid int) bool { return f.alive[pid] }

func (f fakeProbes) RSS(pid int) (int64, bool) {
	v, ok := f.rss[pid]
	return v, ok
}

// budgetFixture builds a daemon state covering every flag class: an active
// server, a resident-past-timeout server, an unloaded server, a resident
// always-on server, and a never-used server. The daemon PID is 100, started
// 3h before now; probe fixtures decide what is actually alive.
func budgetFixture(now time.Time) *daemon.DaemonState {
	return &daemon.DaemonState{
		PID:       100,
		StartedAt: now.Add(-3 * time.Hour),
		Servers: map[string]daemon.ServerState{
			"active-srv": {
				State: "running", PID: 200, Port: 8421,
				StartedAt:    now.Add(-2 * time.Hour),
				LastActivity: now.Add(-5 * time.Minute),
				IdleTimeout:  60,
			},
			"over-srv": {
				State: "running", PID: 201, Port: 8422,
				StartedAt:    now.Add(-48 * time.Hour),
				LastActivity: now.Add(-45 * 24 * time.Hour),
				IdleTimeout:  60,
			},
			"unloaded-srv": {
				State: "unloaded", PID: 0, Port: 8423,
				LastActivity: now.Add(-3 * 24 * time.Hour),
				IdleTimeout:  60,
			},
			"always-on-srv": {
				State: "running", PID: 202, Port: 8424,
				StartedAt:    now.Add(-8 * 24 * time.Hour),
				LastActivity: now.Add(-8 * 24 * time.Hour),
				IdleTimeout:  0,
			},
			"never-srv": {
				State: "unloaded", PID: 0, Port: 8425,
				IdleTimeout: 30,
			},
		},
	}
}

// liveProbes marks pids alive with an RSS value each; the daemon (100)
// is alive too. Anything else is dead/unprobed.
func liveProbes(daemonRSS int64, perPID map[int]int64) fakeProbes {
	alive := map[int]bool{100: true}
	rss := map[int]int64{100: daemonRSS}
	for pid, mem := range perPID {
		alive[pid] = true
		rss[pid] = mem
	}
	return fakeProbes{alive: alive, rss: rss}
}

func findServer(servers []budgetServer, name string) budgetServer {
	for _, s := range servers {
		if s.Name == name {
			return s
		}
	}
	return budgetServer{}
}

func findSuggestion(suggestions []budgetSuggestion, code string) (budgetSuggestion, bool) {
	for _, s := range suggestions {
		if s.Code == code {
			return s, true
		}
	}
	return budgetSuggestion{}, false
}

// ── Idle classification ──────────────────────────────────────────────────

func TestBudgetClassifyIdle(t *testing.T) {
	tests := []struct {
		name        string
		idleMinutes int64
		idleTimeout int
		resident    bool
		hasActivity bool
		want        budgetFlag
	}{
		{"no activity wins", 0, 60, true, false, flagNever},
		{"no activity ignores timeout", 999999, 0, false, false, flagNever},
		{"timeout zero is always-on", 10, 0, true, true, flagAlwaysOn},
		{"timeout zero never over", 999999, 0, true, true, flagAlwaysOn},
		{"inside budget is active", 5, 60, true, true, flagActive},
		{"zero idle (clock-skew clamp) is active", 0, 60, true, true, flagActive},
		{"boundary is over", 60, 60, true, true, flagOver},
		{"past budget resident is over", 61, 60, true, true, flagOver},
		{"past budget unloaded is idle", 61, 60, false, true, flagIdle},
		{"past budget unloaded exactly at boundary", 90, 90, false, true, flagIdle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyBudgetIdle(tt.idleMinutes, tt.idleTimeout, tt.resident, tt.hasActivity)
			if got != tt.want {
				t.Errorf("classifyBudgetIdle(%d, %d, %v, %v) = %q, want %q",
					tt.idleMinutes, tt.idleTimeout, tt.resident, tt.hasActivity, got, tt.want)
			}
		})
	}
}

func TestFormatBudgetIdle(t *testing.T) {
	const min = int64(time.Minute / time.Minute) // 1
	tests := []struct {
		minutes int64
		want    string
	}{
		{0, "0m"},
		{5, "5m"},
		{60, "1h"},
		{60 * min * 0, "0m"}, // keep linter calm about the const
		{90, "1h 30m"},
		{1440, "1d"},
		{1440 + 120, "1d 2h"},
		{45 * 1440, "45d"},
		{3*1440 + 2*60, "3d 2h"},
		{2*1440 + 3*60 + 15, "2d 3h 15m"},
		{-5, "0m"},
	}
	for _, tt := range tests {
		if got := formatBudgetIdle(tt.minutes); got != tt.want {
			t.Errorf("formatBudgetIdle(%d) = %q, want %q", tt.minutes, got, tt.want)
		}
	}
}

// ── Suggestions ──────────────────────────────────────────────────────────

func TestBudgetSuggestionsNoneForIdleSystem(t *testing.T) {
	got := buildBudgetSuggestions([]budgetServer{}, false)
	if len(got) != 0 {
		t.Errorf("daemon not running + no servers: suggestions = %+v, want none", got)
	}
}

func TestBudgetSuggestionsDaemonNoServers(t *testing.T) {
	got := buildBudgetSuggestions([]budgetServer{}, true)
	if len(got) != 1 {
		t.Fatalf("daemon running + 0 servers: got %d suggestions, want 1", len(got))
	}
	if got[0].Code != budgetSuggestNoServers {
		t.Errorf("code = %q, want %q", got[0].Code, budgetSuggestNoServers)
	}
	for _, want := range []string{"0 servers", "pharos daemon stop"} {
		if !strings.Contains(got[0].Message, want) {
			t.Errorf("message %q missing %q", got[0].Message, want)
		}
	}
}

func TestBudgetSuggestionsIdleServers(t *testing.T) {
	day := int64(24 * 60)
	servers := []budgetServer{
		{Name: "b-srv", HasActivity: true, IdleMinutes: 31 * day, IdleTimeout: 60},
		{Name: "a-srv", HasActivity: true, IdleMinutes: 100 * day, IdleTimeout: 60},
		{Name: "fresh-srv", HasActivity: true, IdleMinutes: 10, IdleTimeout: 60},
		{Name: "always-srv", HasActivity: true, IdleMinutes: 100 * day, IdleTimeout: 0},
		{Name: "noact-srv", HasActivity: false, IdleMinutes: 0, IdleTimeout: 60},
	}

	got := buildBudgetSuggestions(servers, true)
	if len(got) != 1 {
		t.Fatalf("got %+v, want exactly one idle_servers suggestion", got)
	}
	s := got[0]
	if s.Code != budgetSuggestIdle {
		t.Errorf("code = %q, want %q", s.Code, budgetSuggestIdle)
	}
	if !strings.Contains(s.Message, "2 server(s) idle >30d") {
		t.Errorf("message %q missing count", s.Message)
	}
	// Names sorted for deterministic output.
	if !strings.Contains(s.Message, "a-srv, b-srv") {
		t.Errorf("message %q missing sorted name list", s.Message)
	}
	if strings.Contains(s.Message, "fresh-srv") || strings.Contains(s.Message, "noact-srv") {
		t.Errorf("message %q lists non-qualifying servers", s.Message)
	}
	if !strings.Contains(s.Message, "pharos stop") || !strings.Contains(s.Message, "--idle-timeout") {
		t.Errorf("message %q missing remediation commands", s.Message)
	}
}

func TestBudgetSuggestionsIdleUnderThresholdNotFlagged(t *testing.T) {
	day := int64(24 * 60)
	servers := []budgetServer{
		{Name: "edge-srv", HasActivity: true, IdleMinutes: 30*day - 1, IdleTimeout: 60},
	}
	if got := buildBudgetSuggestions(servers, true); len(got) != 0 {
		t.Errorf("29d idle produced suggestions %+v, want none", got)
	}
}

func TestBudgetSuggestionsAlwaysOnIdle(t *testing.T) {
	day := int64(24 * 60)
	tests := []struct {
		name     string
		server   budgetServer
		wantCode bool
	}{
		{
			name:     "resident always-on past 7d fires",
			server:   budgetServer{Name: "zap", Resident: true, HasActivity: true, IdleMinutes: 8 * day, IdleTimeout: 0},
			wantCode: true,
		},
		{
			name:     "resident always-on under 7d stays quiet",
			server:   budgetServer{Name: "zap", Resident: true, HasActivity: true, IdleMinutes: 7*day - 1, IdleTimeout: 0},
			wantCode: false,
		},
		{
			name:     "unloaded always-on is not standing cost",
			server:   budgetServer{Name: "zap", Resident: false, HasActivity: true, IdleMinutes: 90 * day, IdleTimeout: 0},
			wantCode: false,
		},
		{
			name:     "always-on with no recorded activity stays quiet",
			server:   budgetServer{Name: "zap", Resident: true, HasActivity: false, IdleTimeout: 0},
			wantCode: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildBudgetSuggestions([]budgetServer{tt.server}, true)
			if tt.wantCode {
				if len(got) != 1 || got[0].Code != budgetSuggestAlwaysOn {
					t.Fatalf("suggestions = %+v, want one always_on_idle", got)
				}
				for _, want := range []string{"zap", "idleTimeout=0", "pharos stop zap"} {
					if !strings.Contains(got[0].Message, want) {
						t.Errorf("message %q missing %q", got[0].Message, want)
					}
				}
			} else if len(got) != 0 {
				t.Errorf("suggestions = %+v, want none", got)
			}
		})
	}
}

func TestBudgetSuggestionsDeterministicOrder(t *testing.T) {
	day := int64(24 * 60)
	servers := []budgetServer{
		{Name: "z-always", Resident: true, HasActivity: true, IdleMinutes: 10 * day, IdleTimeout: 0},
		{Name: "m-always", Resident: true, HasActivity: true, IdleMinutes: 10 * day, IdleTimeout: 0},
		{Name: "a-idle", HasActivity: true, IdleMinutes: 40 * day, IdleTimeout: 60},
	}
	got := buildBudgetSuggestions(servers, true)
	want := []string{budgetSuggestIdle, budgetSuggestAlwaysOn, budgetSuggestAlwaysOn}
	if len(got) != len(want) {
		t.Fatalf("got %d suggestions %+v, want %d", len(got), got, len(want))
	}
	for i, code := range want {
		if got[i].Code != code {
			t.Errorf("suggestion[%d].Code = %q, want %q", i, got[i].Code, code)
		}
	}
	// Always-on suggestions follow server-name order (m before z).
	if !strings.Contains(got[1].Message, "m-always") || !strings.Contains(got[2].Message, "z-always") {
		t.Errorf("always-on order wrong: %q then %q", got[1].Message, got[2].Message)
	}
}

// ── Report assembly ──────────────────────────────────────────────────────

func TestBuildBudgetReportResidencyAndMemory(t *testing.T) {
	now := time.Now()
	state := budgetFixture(now)
	// 200, 202, and the daemon are live; 201's persisted "running" is
	// stale bookkeeping (dead PID) and must not count as resident.
	probe := liveProbes(524288, map[int]int64{
		200: 1048576,
		202: 2097152,
	})

	rep := buildBudgetReport(now, state, probe)

	if !rep.DaemonRunning || rep.DaemonPID != 100 {
		t.Fatalf("daemon = running %v pid %d, want running 100", rep.DaemonRunning, rep.DaemonPID)
	}
	if rep.DaemonUptimeMinutes != 180 {
		t.Errorf("uptimeMinutes = %d, want 180", rep.DaemonUptimeMinutes)
	}

	active := findServer(rep.Servers, "active-srv")
	if !active.Resident || !active.MemoryKnown || active.MemoryRSS != 1048576 {
		t.Errorf("active-srv = %+v, want resident with known RSS", active)
	}
	if active.IdleMinutes != 5 {
		t.Errorf("active-srv idleMinutes = %d, want 5", active.IdleMinutes)
	}

	over := findServer(rep.Servers, "over-srv")
	if over.Resident {
		t.Errorf("over-srv resident = true for dead PID 201, want false (state-file bookkeeping is not liveness)")
	}
	if over.MemoryKnown {
		t.Errorf("over-srv memory known = true, want false for dead PID")
	}
	if over.IdleMinutes != 45*24*60 {
		t.Errorf("over-srv idleMinutes = %d, want %d", over.IdleMinutes, 45*24*60)
	}

	never := findServer(rep.Servers, "never-srv")
	if never.HasActivity || never.IdleMinutes != 0 {
		t.Errorf("never-srv = %+v, want no activity", never)
	}

	// 2 live backing processes + the daemon.
	if rep.ResidentProcesses != 3 {
		t.Errorf("residentProcesses = %d, want 3", rep.ResidentProcesses)
	}
	wantMem := int64(524288 + 1048576 + 2097152)
	if !rep.MemoryEstimateKnown || rep.EstimatedMemoryBytes != wantMem {
		t.Errorf("estimatedMemoryBytes = %d (known %v), want %d", rep.EstimatedMemoryBytes, rep.MemoryEstimateKnown, wantMem)
	}
}

func TestBuildBudgetReportProbesFailGracefully(t *testing.T) {
	now := time.Now()
	state := budgetFixture(now)
	// Everything alive but no RSS answer succeeds: memory unknown everywhere.
	probe := fakeProbes{alive: map[int]bool{100: true, 200: true, 202: true}}

	rep := buildBudgetReport(now, state, probe)

	if rep.ResidentProcesses != 3 {
		t.Errorf("residentProcesses = %d, want 3 (liveness unaffected by RSS probe)", rep.ResidentProcesses)
	}
	if rep.MemoryEstimateKnown {
		t.Error("memoryEstimateKnown = true, want false when every probe fails")
	}
	if rep.EstimatedMemoryBytes != 0 {
		t.Errorf("estimatedMemoryBytes = %d, want 0", rep.EstimatedMemoryBytes)
	}
	for _, s := range rep.Servers {
		if s.MemoryKnown {
			t.Errorf("server %s memory known, want unknown", s.Name)
		}
	}
}

func TestBuildBudgetReportDaemonNotRunning(t *testing.T) {
	now := time.Now()
	state := budgetFixture(now) // lists five servers under daemon PID 100
	// The daemon PID is dead (not in the alive map) while a backing PID
	// still answers: a dead daemon collapses the whole persisted state to
	// the idle report — servers included.
	probe := fakeProbes{alive: map[int]bool{200: true}, rss: map[int]int64{200: 1}}

	rep := buildBudgetReport(now, state, probe)

	if rep.DaemonRunning {
		t.Error("daemonRunning = true, want false")
	}
	if rep.ResidentProcesses != 0 || len(rep.Servers) != 0 {
		t.Errorf("idle system must report 0 processes: %+v", rep)
	}
	if len(rep.Suggestions) != 0 {
		t.Errorf("idle system suggestions = %+v, want none", rep.Suggestions)
	}
	if rep.MemoryEstimateKnown {
		t.Error("memoryEstimateKnown = true, want false")
	}
}

func TestBuildBudgetReportNilInputs(t *testing.T) {
	rep := buildBudgetReport(time.Now(), nil, fakeProbes{})
	if rep.DaemonRunning || rep.ResidentProcesses != 0 || rep.Suggestions == nil || rep.Servers == nil {
		t.Errorf("nil status must yield the empty idle report: %+v", rep)
	}
}

func TestBuildBudgetReportServersSortedByName(t *testing.T) {
	now := time.Now()
	state := budgetFixture(now)
	// Daemon alive (so its servers are listed), every backing PID dead.
	rep := buildBudgetReport(now, state, fakeProbes{alive: map[int]bool{100: true}})

	var names []string
	for _, s := range rep.Servers {
		names = append(names, s.Name)
	}
	want := []string{"active-srv", "always-on-srv", "never-srv", "over-srv", "unloaded-srv"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("server order = %v, want %v", names, want)
	}
}

func TestBuildBudgetReportFixtureSuggestions(t *testing.T) {
	now := time.Now()
	state := budgetFixture(now)
	rep := buildBudgetReport(now, state, liveProbes(524288, map[int]int64{
		200: 1048576, 202: 2097152,
	}))

	var codes []string
	for _, s := range rep.Suggestions {
		codes = append(codes, s.Code)
	}
	want := []string{budgetSuggestIdle, budgetSuggestAlwaysOn}
	if !reflect.DeepEqual(codes, want) {
		t.Errorf("suggestion codes = %v, want %v (over-srv 45d idle; always-on-srv resident 8d)", codes, want)
	}
}

// ── JSON shape (omitempty discipline) ────────────────────────────────────

func TestBudgetJSONShapeRunning(t *testing.T) {
	now := time.Now()
	state := budgetFixture(now)
	rep := buildBudgetReport(now, state, liveProbes(524288, map[int]int64{
		200: 1048576, 202: 2097152,
	}))

	data, err := renderBudgetJSON(rep)
	if err != nil {
		t.Fatalf("renderBudgetJSON: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, data)
	}

	// Daemon section: pid, startedAt, uptimeMinutes.
	daemonDoc, ok := doc["daemon"].(map[string]any)
	if !ok {
		t.Fatalf("daemon section missing or wrong type: %v", doc["daemon"])
	}
	if daemonDoc["pid"].(float64) != 100 {
		t.Errorf("daemon.pid = %v, want 100", daemonDoc["pid"])
	}
	if _, ok := daemonDoc["startedAt"].(string); !ok {
		t.Errorf("daemon.startedAt missing: %v", daemonDoc)
	}
	if daemonDoc["uptimeMinutes"].(float64) != 180 {
		t.Errorf("daemon.uptimeMinutes = %v, want 180", daemonDoc["uptimeMinutes"])
	}

	servers, ok := doc["servers"].([]any)
	if !ok || len(servers) != 5 {
		t.Fatalf("servers = %v, want 5 entries", doc["servers"])
	}

	byName := map[string]map[string]any{}
	for _, raw := range servers {
		m := raw.(map[string]any)
		byName[m["name"].(string)] = m
	}

	// resident is always present, even when false.
	unloaded := byName["unloaded-srv"]
	if resident, present := unloaded["resident"]; !present || resident != false {
		t.Errorf("unloaded-srv resident = %v (present %v), want false present", resident, present)
	}
	// pid omitted (omitempty) for unloaded servers, present for resident ones.
	if _, present := unloaded["pid"]; present {
		t.Errorf("unloaded-srv has pid key, want omitted (omitempty, never null)")
	}
	if pid, present := byName["active-srv"]["pid"]; !present || pid.(float64) != 200 {
		t.Errorf("active-srv pid = %v (present %v), want 200 present", pid, present)
	}
	// idleTimeoutMinutes always present, 0 meaningful (no auto-unload).
	if to, present := byName["always-on-srv"]["idleTimeoutMinutes"]; !present || to.(float64) != 0 {
		t.Errorf("always-on-srv idleTimeoutMinutes = %v (present %v), want 0 present", to, present)
	}
	// idleMinutes omitted for the never-used server.
	if _, present := byName["never-srv"]["idleMinutes"]; present {
		t.Errorf("never-srv has idleMinutes key, want omitted (no recorded activity)")
	}
	// lastActivity present for servers with activity.
	if _, present := byName["active-srv"]["lastActivity"]; !present {
		t.Error("active-srv lastActivity missing, want RFC3339 string")
	}
	// memoryRSSBytes omitted when the probe failed (over-srv is dead).
	if _, present := byName["over-srv"]["memoryRSSBytes"]; present {
		t.Errorf("over-srv has memoryRSSBytes key, want omitted for dead PID")
	}
	if mem, present := byName["active-srv"]["memoryRSSBytes"]; !present || mem.(float64) != 1048576 {
		t.Errorf("active-srv memoryRSSBytes = %v (present %v), want 1048576", mem, present)
	}

	// Totals: residentProcesses always; estimatedMemoryBytes present here.
	totals, ok := doc["totals"].(map[string]any)
	if !ok {
		t.Fatalf("totals section missing: %v", doc["totals"])
	}
	if totals["residentProcesses"].(float64) != 3 {
		t.Errorf("totals.residentProcesses = %v, want 3", totals["residentProcesses"])
	}
	if totals["estimatedMemoryBytes"].(float64) != 524288+1048576+2097152 {
		t.Errorf("totals.estimatedMemoryBytes = %v", totals["estimatedMemoryBytes"])
	}

	// Suggestions: array of {code, message}, never null.
	suggestions, ok := doc["suggestions"].([]any)
	if !ok || len(suggestions) == 0 {
		t.Fatalf("suggestions = %v, want non-empty array", doc["suggestions"])
	}
	first := suggestions[0].(map[string]any)
	if first["code"] == "" || first["message"] == "" {
		t.Errorf("suggestion missing code/message: %v", first)
	}
}

func TestBudgetJSONProbesFailOmitsMemoryTotals(t *testing.T) {
	now := time.Now()
	state := budgetFixture(now)
	rep := buildBudgetReport(now, state, fakeProbes{alive: map[int]bool{100: true, 200: true}})

	data, err := renderBudgetJSON(rep)
	if err != nil {
		t.Fatalf("renderBudgetJSON: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	totals := doc["totals"].(map[string]any)
	if _, present := totals["estimatedMemoryBytes"]; present {
		t.Errorf("estimatedMemoryBytes present with all-failed probes, want omitted (omitempty)")
	}
}

func TestBudgetJSONNotRunning(t *testing.T) {
	rep := buildBudgetReport(time.Now(), nil, fakeProbes{})

	data, err := renderBudgetJSON(rep)
	if err != nil {
		t.Fatalf("renderBudgetJSON: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if _, present := doc["daemon"]; present {
		t.Errorf("daemon key present for not-running daemon, want omitted")
	}
	servers, ok := doc["servers"].([]any)
	if !ok || len(servers) != 0 {
		t.Errorf("servers = %v, want empty array (never null)", doc["servers"])
	}
	totals := doc["totals"].(map[string]any)
	if totals["residentProcesses"].(float64) != 0 {
		t.Errorf("residentProcesses = %v, want 0", totals["residentProcesses"])
	}
	if _, present := totals["estimatedMemoryBytes"]; present {
		t.Errorf("estimatedMemoryBytes present for idle system, want omitted")
	}
	suggestions, ok := doc["suggestions"].([]any)
	if !ok || len(suggestions) != 0 {
		t.Errorf("suggestions = %v, want empty array (never null)", doc["suggestions"])
	}
}

// ── Human rendering ──────────────────────────────────────────────────────

func TestRenderBudgetHumanNotRunning(t *testing.T) {
	rep := buildBudgetReport(time.Now(), nil, fakeProbes{})
	out := renderBudgetHuman(rep)
	for _, want := range []string{"Daemon is not running", "0 resident processes", "pharos daemon start"} {
		if !strings.Contains(out, want) {
			t.Errorf("not-running report missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Suggestions") {
		t.Errorf("not-running report should carry no suggestions:\n%s", out)
	}
}

func TestRenderBudgetHumanRunning(t *testing.T) {
	now := time.Now()
	state := budgetFixture(now)
	rep := buildBudgetReport(now, state, liveProbes(524288, map[int]int64{
		200: 1048576, 202: 2097152,
	}))

	out := renderBudgetHuman(rep)
	for _, want := range []string{
		"PID 100",
		"Resident processes:",
		"3 (2 resident server(s) + daemon)",
		"Estimated memory:",
		"active-srv",
		"MEMORY (EST)",
		"never-used",
		"always-on",
		"over",
		"advisory",
		"Suggestions:",
		"idle >30d",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("running report missing %q:\n%s", want, out)
		}
	}
}

func TestRenderBudgetHumanProbeFailureShowsEstimateLabels(t *testing.T) {
	now := time.Now()
	state := budgetFixture(now)
	rep := buildBudgetReport(now, state, fakeProbes{alive: map[int]bool{100: true}})

	out := renderBudgetHuman(rep)
	for _, want := range []string{"n/a (probe failed)", "RSS estimate"} {
		if !strings.Contains(out, want) {
			t.Errorf("probe-failure report missing %q:\n%s", want, out)
		}
	}
}

// ── Clock skew (Rec3) ────────────────────────────────────────────────────

// TestBuildBudgetReportClockSkewClampsIdle pins the future-LastActivity
// case: a daemon host clock ahead of the probe host yields a negative
// raw idle, which must clamp to 0 and classify as active — never a
// negative or misclassified row.
func TestBuildBudgetReportClockSkewClampsIdle(t *testing.T) {
	now := time.Now()
	state := &daemon.DaemonState{
		PID:       100,
		StartedAt: now.Add(-time.Hour),
		Servers: map[string]daemon.ServerState{
			"future-srv": {
				State: "running", PID: 200, Port: 8421,
				// Clock skew: activity stamped 5m in the future.
				LastActivity: now.Add(5 * time.Minute),
				IdleTimeout:  60,
			},
		},
	}
	rep := buildBudgetReport(now, state, liveProbes(4096, map[int]int64{200: 1024}))

	s := findServer(rep.Servers, "future-srv")
	if !s.HasActivity {
		t.Fatal("future-srv hasActivity = false, want true")
	}
	if s.IdleMinutes != 0 {
		t.Errorf("future-srv idleMinutes = %d, want 0 (negative idle clamps to zero)", s.IdleMinutes)
	}
	if got := classifyBudgetIdle(s.IdleMinutes, s.IdleTimeout, s.Resident, s.HasActivity); got != flagActive {
		t.Errorf("future-srv class = %q, want %q (clamped idle sits inside the budget)", got, flagActive)
	}
	if flag := renderBudgetFlag(s); !strings.Contains(flag, "active") {
		t.Errorf("rendered budget flag = %q, want the active styling", flag)
	}
}

// ── JSON shape: just-started daemon (Rec5) ───────────────────────────────

// TestBudgetJSONUptimeUnderAMinuteOmitted pins the omitempty discipline for
// a daemon younger than a minute: whole-minute uptime truncates to 0, and
// uptimeMinutes must be omitted (never emitted as a null-hacked zero).
func TestBudgetJSONUptimeUnderAMinuteOmitted(t *testing.T) {
	now := time.Now()
	state := &daemon.DaemonState{
		PID:       100,
		StartedAt: now.Add(-30 * time.Second), // just-started daemon
		Servers:   map[string]daemon.ServerState{},
	}
	rep := buildBudgetReport(now, state, fakeProbes{alive: map[int]bool{100: true}})

	data, err := renderBudgetJSON(rep)
	if err != nil {
		t.Fatalf("renderBudgetJSON: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}

	daemonDoc, ok := doc["daemon"].(map[string]any)
	if !ok {
		t.Fatalf("daemon section missing: %v", doc["daemon"])
	}
	if _, present := daemonDoc["uptimeMinutes"]; present {
		t.Errorf("uptimeMinutes present for a <1m-old daemon, want omitted (omitempty): %v", daemonDoc)
	}
	if pid, present := daemonDoc["pid"]; !present || pid.(float64) != 100 {
		t.Errorf("daemon.pid = %v (present %v), want 100 present", pid, present)
	}
}

// ── R1 regression: budget must not delete daemon.pid ─────────────────────

// deadPID returns the PID of a process that has fully exited and been
// reaped (spawn-wait, same pattern as the internal/procs liveness tests),
// so the PID is genuinely dead the moment it returns — a stale daemon.pid.
func deadPID(t *testing.T) int {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("dead-pid fixture relies on unix process reaping")
	}
	proc := exec.Command("true")
	if err := proc.Start(); err != nil {
		t.Skipf("cannot spawn a probe process: %v", err)
	}
	if err := proc.Wait(); err != nil {
		t.Skipf("wait for probe process failed: %v", err)
	}
	return proc.Process.Pid
}

// writeDaemonFixture plants daemon.pid (daemon format: strconv.Itoa, no
// trailing newline) plus daemon.json under the isolated home's .pharos dir.
func writeDaemonFixture(t *testing.T, pid int) (pidPath, stateJSON string) {
	t.Helper()
	dir := filepath.Join(contractHome(t), ".pharos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pidPath = filepath.Join(dir, "daemon.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	stateJSON = `{
  "pid": ` + strconv.Itoa(pid) + `,
  "startedAt": "2026-01-01T00:00:00Z",
  "servers": {}
}`
	statePath := filepath.Join(dir, "daemon.json")
	if err := os.WriteFile(statePath, []byte(stateJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return pidPath, stateJSON
}

// TestBudgetLeavesStaleDaemonPIDInPlace is the R1 regression: the daemon's
// Status() used to remove a stale daemon.pid as a side effect, violating
// budget's write-nothing contract. Budget must read the state through the
// read-only exports and leave the pid file exactly where it is, reporting
// the dead daemon as the valid idle report.
func TestBudgetLeavesStaleDaemonPIDInPlace(t *testing.T) {
	isolateHome(t)
	dead := deadPID(t)
	pidPath, stateJSON := writeDaemonFixture(t, dead)
	statePath := filepath.Join(filepath.Dir(pidPath), "daemon.json")

	_, combined := runContract(t, map[string]string{"PHAROS_NON_INTERACTIVE": "1"}, "budget")

	if _, err := os.Stat(pidPath); err != nil {
		t.Errorf("daemon.pid was removed by budget (read-only contract): %v", err)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != stateJSON {
		t.Errorf("daemon.json was modified by budget:\nwant %q\ngot  %q", stateJSON, after)
	}
	if !strings.Contains(combined, "Daemon is not running") {
		t.Errorf("stale-daemon budget output missing the not-running report:\n%s", combined)
	}
}

// TestBudgetLeavesLiveDaemonPIDInPlace mirrors the regression for a live
// daemon: the pid file survives and the report shows the daemon running.
func TestBudgetLeavesLiveDaemonPIDInPlace(t *testing.T) {
	isolateHome(t)
	self := os.Getpid()
	pidPath, _ := writeDaemonFixture(t, self)

	stdout, _ := runContract(t, map[string]string{"PHAROS_NON_INTERACTIVE": "1"}, "budget", "--json")

	if _, err := os.Stat(pidPath); err != nil {
		t.Errorf("daemon.pid was removed by budget (read-only contract): %v", err)
	}
	trimmed := strings.TrimSpace(stdout)
	if !json.Valid([]byte(trimmed)) {
		t.Fatalf("budget --json did not emit valid JSON: %.200q", trimmed)
	}
	var doc struct {
		Daemon *struct {
			PID int `json:"pid"`
		} `json:"daemon"`
	}
	if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
		t.Fatalf("decode budget JSON: %v\n%s", err, trimmed)
	}
	if doc.Daemon == nil {
		t.Fatalf("daemon block missing for a live daemon: %.200q", trimmed)
	}
	if doc.Daemon.PID != self {
		t.Errorf("daemon.pid = %d, want %d", doc.Daemon.PID, self)
	}
}
