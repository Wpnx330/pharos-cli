package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/daemon"
	"github.com/Wpnx330/pharos-cli/internal/procs"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

// ── pharos budget — idle-cost budgets (W5.2 C2) ──────────────────────────
//
// Aggregate view of what the managed MCP fleet costs while it sits idle:
// resident processes (live backing processes + the daemon), estimated
// memory (RSS, estimate-grade), per-server idle time against each
// server's own idle-timeout, and advisory unload suggestions.
//
// Strictly read-only over ~/.pharos/daemon.json + OS process probes:
// no daemon or config writes, no receipt, no mutation. Suggestions are
// advisory text — nothing is ever applied automatically. Token metering
// is deliberately absent (spec: vaporware until APIs expose usage).

var budgetJSON bool

var budgetCmd = &cobra.Command{
	Use:   "budget",
	Short: "Show the idle-cost budget — resident processes, memory, unload suggestions",
	Long: ui.Label.Render("pharos budget") + ` — what your daemon-managed fleet costs while it sits idle.

Covers daemon-managed resident servers only: each daemon-managed
HTTP/SSE server holds a JIT-loaded backing process until its idle
timeout unloads it. pharos budget aggregates that standing cost —
live processes, estimated memory, per-server idle time against each
server's own idle-timeout, and advisory suggestions. Plain stdio
servers are launched on demand by the MCP client itself, and budget
reads only the daemon's state (~/.pharos/daemon.json), so they never
appear in this report.

Read-only: nothing is unloaded or reconfigured by this command, and
no receipt is written. Memory figures are OS RSS estimates, never
billing numbers. Token metering is not implemented (no API exposes it).

Examples:
  pharos budget          # human report
  pharos budget --json   # full structured report`,
	Run: runBudget,
}

func init() {
	budgetCmd.Flags().BoolVar(&budgetJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(budgetCmd)
}

// Suggestion thresholds, in minutes. The generic idle nudge fires at 30
// days. Always-on servers (idleTimeout=0) are deliberate standing cost,
// so they are surfaced sooner — after 7 days without a single request.
const (
	budgetIdleSuggestMinutes     int64 = 30 * 24 * 60 // 30 days
	budgetAlwaysOnSuggestMinutes int64 = 7 * 24 * 60  // 7 days
)

// Machine-stable suggestion codes.
const (
	budgetSuggestIdle      = "idle_servers"
	budgetSuggestAlwaysOn  = "always_on_idle"
	budgetSuggestNoServers = "daemon_no_servers"
)

// ── Report model ─────────────────────────────────────────────────────────

// budgetServer is one daemon-managed server in the budget report.
type budgetServer struct {
	Name         string
	PID          int
	Port         int
	StartedAt    time.Time
	LastActivity time.Time
	IdleTimeout  int // minutes; 0 = never unload
	Resident     bool
	IdleMinutes  int64
	HasActivity  bool
	MemoryRSS    int64
	MemoryKnown  bool
}

// budgetReport is the assembled budget view. When the daemon is not
// running the report is the valid idle-system report: 0 processes.
type budgetReport struct {
	DaemonRunning        bool
	DaemonPID            int
	DaemonStartedAt      time.Time
	DaemonUptimeMinutes  int64
	DaemonMemoryRSS      int64
	DaemonMemoryKnown    bool
	Servers              []budgetServer
	ResidentProcesses    int // live backing processes + the daemon itself
	EstimatedMemoryBytes int64
	MemoryEstimateKnown  bool
	Suggestions          []budgetSuggestion
}

// budgetSuggestion is one advisory suggestion. Advisory only: it is
// printed, never applied.
type budgetSuggestion struct {
	Code    string
	Message string
}

// procsProbe abstracts the OS process probes so report building is
// deterministic under test.
type procsProbe interface {
	Alive(pid int) bool
	RSS(pid int) (int64, bool)
}

// osProbes is the real probe set. Liveness routes through the daemon's
// own canonical check (daemon.IsProcessAlive — the read-only export) so
// budget sees process state exactly as the daemon does and the check is
// not duplicated here; RSS stays on internal/procs, the portable probe
// library.
type osProbes struct{}

func (osProbes) Alive(pid int) bool        { return daemon.IsProcessAlive(pid) }
func (osProbes) RSS(pid int) (int64, bool) { return procs.RSS(pid) }

// ── Idle classification ──────────────────────────────────────────────────

// budgetFlag classifies a server's idle time against its own idle-timeout:
//
//	never-used  no recorded activity (never served a request)
//	always-on   idleTimeout=0 — no auto-unload budget exists
//	active      inside its idle budget
//	over        past its idle budget and still resident (due to unload)
//	idle        past its idle budget, unloaded (the normal JIT end state)
type budgetFlag string

const (
	flagActive   budgetFlag = "active"
	flagIdle     budgetFlag = "idle"
	flagOver     budgetFlag = "over"
	flagNever    budgetFlag = "never-used"
	flagAlwaysOn budgetFlag = "always-on"
)

// classifyBudgetIdle maps idle minutes + timeout + residency to a flag.
// hasActivity=false wins first (no clock to compare against); timeout 0
// means there is no budget to exceed.
func classifyBudgetIdle(idleMinutes int64, idleTimeoutMin int, resident, hasActivity bool) budgetFlag {
	if !hasActivity {
		return flagNever
	}
	if idleTimeoutMin <= 0 {
		return flagAlwaysOn
	}
	if idleMinutes < int64(idleTimeoutMin) {
		return flagActive
	}
	if resident {
		return flagOver
	}
	return flagIdle
}

// ── Report assembly ──────────────────────────────────────────────────────

// buildBudgetReport assembles the budget report from the daemon's
// persisted state (~/.pharos/daemon.json), read via the read-only
// daemon.ReadState export. Daemon liveness comes from probe.Alive — the
// daemon's own check (daemon.IsProcessAlive) — so a persisted state with
// a dead PID is reported as an idle system, and residency is never
// inferred from the state file's bookkeeping. Nothing in this path
// writes or mutates anything (budget's write-nothing contract): unlike
// daemon.Status(), a stale daemon.pid is left exactly where it is. now
// is injected for deterministic tests.
func buildBudgetReport(now time.Time, state *daemon.DaemonState, probe procsProbe) *budgetReport {
	rep := &budgetReport{
		Servers:     []budgetServer{},
		Suggestions: []budgetSuggestion{},
	}
	if state == nil || state.PID <= 0 || !probe.Alive(state.PID) {
		// Idle system: a valid budget report with 0 processes.
		return rep
	}

	rep.DaemonRunning = true
	rep.DaemonPID = state.PID
	rep.DaemonStartedAt = state.StartedAt
	rep.ResidentProcesses = 1 // the daemon itself
	if !state.StartedAt.IsZero() {
		if up := int64(now.Sub(state.StartedAt).Minutes()); up > 0 {
			rep.DaemonUptimeMinutes = up
		}
	}

	// The daemon's own RSS is part of the standing cost.
	if bytes, ok := probe.RSS(state.PID); ok {
		rep.DaemonMemoryRSS = bytes
		rep.DaemonMemoryKnown = true
		rep.EstimatedMemoryBytes += bytes
		rep.MemoryEstimateKnown = true
	}

	// Deterministic order: servers sorted by name.
	names := make([]string, 0, len(state.Servers))
	for name := range state.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		ss := state.Servers[name]
		s := budgetServer{
			Name:         name,
			PID:          ss.PID,
			Port:         ss.Port,
			StartedAt:    ss.StartedAt,
			LastActivity: ss.LastActivity,
			IdleTimeout:  ss.IdleTimeout,
			HasActivity:  !ss.LastActivity.IsZero(),
		}
		if s.HasActivity {
			idle := int64(now.Sub(ss.LastActivity).Minutes())
			if idle < 0 {
				idle = 0 // clock skew between daemon and probe host
			}
			s.IdleMinutes = idle
		}
		// Residency is liveness of the backing PID, not the state
		// file's bookkeeping — a dead PID is not a resident process.
		if ss.PID > 0 && probe.Alive(ss.PID) {
			s.Resident = true
			rep.ResidentProcesses++
			if bytes, ok := probe.RSS(ss.PID); ok {
				s.MemoryRSS = bytes
				s.MemoryKnown = true
				rep.EstimatedMemoryBytes += bytes
				rep.MemoryEstimateKnown = true
			}
		}
		rep.Servers = append(rep.Servers, s)
	}

	rep.Suggestions = buildBudgetSuggestions(rep.Servers, true)
	return rep
}

// ── Suggestions ──────────────────────────────────────────────────────────

// buildBudgetSuggestions derives the advisory suggestions. Deterministic:
// idle_servers first, then per-server always-on suggestions in server-name
// order, then daemon_no_servers. daemonRunning=false yields none — an
// idle system has no idle cost to surface.
func buildBudgetSuggestions(servers []budgetServer, daemonRunning bool) []budgetSuggestion {
	suggestions := []budgetSuggestion{}

	var longIdle []string
	for _, s := range servers {
		if !s.HasActivity || s.IdleTimeout <= 0 {
			continue
		}
		if s.IdleMinutes >= budgetIdleSuggestMinutes {
			longIdle = append(longIdle, s.Name)
		}
	}
	if len(longIdle) > 0 {
		sort.Strings(longIdle)
		suggestions = append(suggestions, budgetSuggestion{
			Code: budgetSuggestIdle,
			Message: fmt.Sprintf("%d server(s) idle >30d (%s) — consider 'pharos stop <name>' or reinstalling with a tighter --idle-timeout",
				len(longIdle), strings.Join(longIdle, ", ")),
		})
	}

	var alwaysOn []budgetServer
	for _, s := range servers {
		// Only resident always-on servers are standing cost worth
		// surfacing; an already-unloaded one has nothing to stop.
		if !s.HasActivity || !s.Resident || s.IdleTimeout != 0 {
			continue
		}
		if s.IdleMinutes >= budgetAlwaysOnSuggestMinutes {
			alwaysOn = append(alwaysOn, s)
		}
	}
	sort.Slice(alwaysOn, func(i, j int) bool { return alwaysOn[i].Name < alwaysOn[j].Name })
	for _, s := range alwaysOn {
		suggestions = append(suggestions, budgetSuggestion{
			Code: budgetSuggestAlwaysOn,
			Message: fmt.Sprintf("server %s has idleTimeout=0 (no auto-unload) and hasn't served a request in %s — consider 'pharos stop %s' or reinstalling with --idle-timeout 60",
				s.Name, formatBudgetIdle(s.IdleMinutes), s.Name),
		})
	}

	if daemonRunning && len(servers) == 0 {
		suggestions = append(suggestions, budgetSuggestion{
			Code:    budgetSuggestNoServers,
			Message: "daemon is holding 0 servers — 'pharos daemon stop' frees the port",
		})
	}

	return suggestions
}

// ── JSON (W1.1 purity: one pure JSON document on stdout) ─────────────────

// budgetOut is the JSON shape of `pharos budget --json`. Fields marked
// with ? in the spec (pid, startedAt, lastActivity, idleMinutes,
// memoryRSSBytes, estimatedMemoryBytes, daemon) are omitted when unknown
// via omitempty — never null-hacked. resident and idleTimeoutMinutes are
// always present: false / 0 are meaningful (idleTimeoutMinutes 0 =
// "no auto-unload").
type budgetOut struct {
	Daemon      *budgetDaemonOut      `json:"daemon,omitempty"`
	Servers     []budgetServerOut     `json:"servers"`
	Totals      budgetTotalsOut       `json:"totals"`
	Suggestions []budgetSuggestionOut `json:"suggestions"`
}

type budgetDaemonOut struct {
	PID           int    `json:"pid"`
	StartedAt     string `json:"startedAt,omitempty"`
	UptimeMinutes int64  `json:"uptimeMinutes,omitempty"`
}

type budgetServerOut struct {
	Name               string `json:"name"`
	PID                int    `json:"pid,omitempty"`
	Port               int    `json:"port,omitempty"`
	StartedAt          string `json:"startedAt,omitempty"`
	LastActivity       string `json:"lastActivity,omitempty"`
	IdleMinutes        int64  `json:"idleMinutes,omitempty"`
	IdleTimeoutMinutes int    `json:"idleTimeoutMinutes"`
	Resident           bool   `json:"resident"`
	MemoryRSSBytes     int64  `json:"memoryRSSBytes,omitempty"`
}

type budgetTotalsOut struct {
	ResidentProcesses    int   `json:"residentProcesses"`
	EstimatedMemoryBytes int64 `json:"estimatedMemoryBytes,omitempty"`
}

type budgetSuggestionOut struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// renderBudgetJSON encodes the report as the stdout JSON document.
func renderBudgetJSON(rep *budgetReport) ([]byte, error) {
	out := budgetOut{
		Servers:     []budgetServerOut{},
		Suggestions: []budgetSuggestionOut{},
	}

	if rep.DaemonRunning {
		d := &budgetDaemonOut{PID: rep.DaemonPID}
		if !rep.DaemonStartedAt.IsZero() {
			d.StartedAt = rep.DaemonStartedAt.UTC().Format(time.RFC3339)
		}
		d.UptimeMinutes = rep.DaemonUptimeMinutes
		out.Daemon = d
	}

	for _, s := range rep.Servers {
		so := budgetServerOut{
			Name:               s.Name,
			PID:                s.PID,
			Port:               s.Port,
			IdleMinutes:        s.IdleMinutes,
			IdleTimeoutMinutes: s.IdleTimeout,
			Resident:           s.Resident,
			MemoryRSSBytes:     s.MemoryRSS,
		}
		if !s.StartedAt.IsZero() {
			so.StartedAt = s.StartedAt.UTC().Format(time.RFC3339)
		}
		if s.HasActivity {
			so.LastActivity = s.LastActivity.UTC().Format(time.RFC3339)
		}
		if s.MemoryKnown {
			so.MemoryRSSBytes = s.MemoryRSS
		}
		out.Servers = append(out.Servers, so)
	}

	out.Totals = budgetTotalsOut{
		ResidentProcesses:    rep.ResidentProcesses,
		EstimatedMemoryBytes: rep.EstimatedMemoryBytes,
	}
	if !rep.MemoryEstimateKnown {
		out.Totals.EstimatedMemoryBytes = 0 // omitted by omitempty
	}

	for _, s := range rep.Suggestions {
		out.Suggestions = append(out.Suggestions, budgetSuggestionOut{
			Code:    s.Code,
			Message: s.Message,
		})
	}

	return json.MarshalIndent(out, "", "  ")
}

// ── Human rendering ──────────────────────────────────────────────────────

// renderBudgetHuman renders the full human report. Pure: it returns the
// text so tests can assert on it directly.
func renderBudgetHuman(rep *budgetReport) string {
	var b strings.Builder

	if !rep.DaemonRunning {
		b.WriteString(ui.Muted.Render("Daemon is not running — 0 resident processes, nothing to budget."))
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("\n  %s  %s\n", ui.Muted.Render("Start it with:"), "pharos daemon start"))
		return b.String()
	}

	daemonLine := fmt.Sprintf("daemon running (PID %d, up %s)", rep.DaemonPID, formatBudgetIdle(rep.DaemonUptimeMinutes))
	if rep.DaemonMemoryKnown {
		daemonLine += fmt.Sprintf(", ~%s RSS", ui.FormatBytes(rep.DaemonMemoryRSS))
	}
	b.WriteString(fmt.Sprintf("%s  %s\n", ui.Success.Render("✓"), daemonLine))

	memStr := ui.Muted.Render("n/a (probe failed)")
	if rep.MemoryEstimateKnown {
		memStr = ui.FormatBytes(rep.EstimatedMemoryBytes)
	}
	b.WriteString(fmt.Sprintf("\n  %s  %d (%d managed server(s) + daemon)\n",
		ui.Label.Render("Resident processes:"), rep.ResidentProcesses, len(rep.Servers)-countNotResident(rep.Servers)))
	b.WriteString(fmt.Sprintf("  %s  %s\n", ui.Label.Render("Estimated memory:"), memStr))

	if len(rep.Servers) == 0 {
		b.WriteString(fmt.Sprintf("\n%s\n", ui.Muted.Render("No servers managed by the daemon.")))
	} else {
		cols := []ui.TableColumn{
			{Title: "NAME", Width: 22, MaxWidth: 0},
			{Title: "PID", Width: 7, MaxWidth: 7},
			{Title: "IDLE", Width: 9, MaxWidth: 9},
			{Title: "BUDGET", Width: 11, MaxWidth: 11},
			{Title: "LIMIT", Width: 7, MaxWidth: 7},
			{Title: "MEMORY (EST)", Width: 13, MaxWidth: 13},
		}
		var rows []ui.TableRow
		for _, s := range rep.Servers {
			pid := ui.Muted.Render("—")
			if s.Resident {
				pid = fmt.Sprintf("%d", s.PID)
			}
			idle := ui.Muted.Render("—")
			if s.HasActivity {
				idle = formatBudgetIdle(s.IdleMinutes)
			}
			limit := ui.Muted.Render("never")
			if s.IdleTimeout > 0 {
				limit = formatBudgetIdle(int64(s.IdleTimeout))
			}
			mem := ui.Muted.Render("—")
			if s.MemoryKnown {
				mem = ui.FormatBytes(s.MemoryRSS)
			}
			rows = append(rows, ui.TableRow{
				ui.PackageName.Render(s.Name), pid, idle,
				renderBudgetFlag(s), limit, mem,
			})
		}
		b.WriteString("\n")
		b.WriteString(ui.RenderTable(cols, rows))
	}

	b.WriteString(fmt.Sprintf("\n%s\n", ui.Muted.Render("Memory is an RSS estimate sampled at report time; \"—\" means the probe failed. Suggestions are advisory — nothing is applied.")))

	if len(rep.Suggestions) > 0 {
		b.WriteString(fmt.Sprintf("\n%s\n", ui.Label.Render("Suggestions:")))
		for _, s := range rep.Suggestions {
			b.WriteString(fmt.Sprintf("  %s  %s\n", ui.Muted.Render("·"), s.Message))
		}
	}

	return b.String()
}

// renderBudgetFlag styles the per-server BUDGET cell.
func renderBudgetFlag(s budgetServer) string {
	switch classifyBudgetIdle(s.IdleMinutes, s.IdleTimeout, s.Resident, s.HasActivity) {
	case flagActive:
		return ui.Success.Render("active")
	case flagOver:
		return ui.Warning.Render("over")
	case flagAlwaysOn:
		return ui.Label.Render("always-on")
	case flagIdle:
		return ui.Muted.Render("idle")
	default:
		return ui.Muted.Render("never-used")
	}
}

func countNotResident(servers []budgetServer) int {
	n := 0
	for _, s := range servers {
		if !s.Resident {
			n++
		}
	}
	return n
}

// formatBudgetIdle renders idle minutes as a compact duration:
// "5m", "3h 12m", "45d", "3d 2h".
func formatBudgetIdle(minutes int64) string {
	if minutes < 0 {
		minutes = 0
	}
	d := minutes / (24 * 60)
	h := (minutes % (24 * 60)) / 60
	m := minutes % 60
	switch {
	case d > 0 && h > 0 && m > 0:
		return fmt.Sprintf("%dd %dh %dm", d, h, m)
	case d > 0 && h > 0:
		return fmt.Sprintf("%dd %dh", d, h)
	case d > 0 && m > 0:
		return fmt.Sprintf("%dd %dm", d, m)
	case d > 0:
		return fmt.Sprintf("%dd", d)
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dm", m)
	}
}

// ── Command entry ────────────────────────────────────────────────────────

// runBudget collects the budget view and prints it. Error handling mirrors
// `pharos daemon status`: a daemon that is not running is a valid report
// (exit 0); a state file that exists but cannot be read is a real failure
// (exit 1). Memory-probe failures never fail the command.
//
// The state is loaded through the read-only daemon.ReadState export and
// daemon liveness checked through the daemon's own canonical check (wired
// in via the osProbes seam): unlike daemon.Status(), nothing in this path
// removes a stale daemon.pid — budget writes nothing, ever.
func runBudget(cmd *cobra.Command, args []string) {
	state, err := daemon.ReadState()
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot read daemon state:"), err)
		os.Exit(1)
	}

	rep := buildBudgetReport(time.Now(), state, osProbes{})

	if JSONRequested() {
		data, err := renderBudgetJSON(rep)
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot encode budget report:"), err)
			os.Exit(1)
		}
		fmt.Println(string(data))
		return
	}

	fmt.Print(renderBudgetHuman(rep))
}
