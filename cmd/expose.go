package cmd

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/daemon"
	"github.com/Wpnx330/pharos-cli/internal/expose"
	"github.com/Wpnx330/pharos-cli/internal/procs"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

// ── pharos expose — token-authed reverse proxy (W5.3 A5, scoped) ─────────
//
// Publishes ONE daemon-managed HTTP/SSE server through a public listener
// guarded by a bearer token. What it is: a reverse proxy in front of the
// daemon's loopback proxy listener, with a one-time token handoff, a
// process-enforced TTL (default 8h, max 24h), and hash-only token storage.
//
// What it is NOT: not a public relay service, not WebRTC, not a Cloudflare
// Tunnel integration — no third-party relay exists in this path; whoever
// gets the token can reach the listener you bind, and nobody else.
//
// This is the product's first non-loopback binding, so the posture here is
// deliberately strict: default-deny auth in front of every request, no
// token at rest (SHA-256 hash only), no token in logs, explicit opt-in for
// the public address.

// exposeTokenEnv carries the one-time bearer token from the CLI parent to
// a --background worker process. The worker reads it once at startup; the
// token never touches disk in plain form.
const exposeTokenEnv = "PHAROS_EXPOSE_TOKEN"

// exposeStopWait is how long `expose stop` waits for a running expose to
// acknowledge its stop request and exit (same grace as `daemon stop`). A
// var so tests can shorten the grace period.
var exposeStopWait = 10 * time.Second

// osExit is the process exit seam (tests capture it instead of dying).
var osExit = os.Exit

// Liveness seams (overridable in tests): exposeAliveFn probes the expose
// worker's PID; exposeDaemonAliveFn probes the daemon's PID through the
// daemon's own canonical check.
var (
	exposeAliveFn       = procs.Alive
	exposeDaemonAliveFn = daemon.IsProcessAlive
)

var (
	exposeAddr       string
	exposeTTL        time.Duration
	exposeBackground bool
	exposeJSON       bool
	exposeInternal   bool // hidden: marks the background worker process
)

var exposeCmd = &cobra.Command{
	Use:   "expose <name>",
	Short: "Expose a daemon-managed server via a token-authed reverse proxy",
	Long: ui.Label.Render("pharos expose") + ` — share a running MCP server over a token-authed proxy.

Starts a reverse proxy in FRONT of the daemon's loopback proxy listener for
<name>: a public listener you choose (--addr, required) forwards to
127.0.0.1:<daemon proxy port>. Every request must present
"Authorization: Bearer <token>" — generated once, shown once, stored only
as a SHA-256 hash in ~/.pharos/expose.json.

The expose process enforces its own TTL (default 8h, max 24h): at expiry it
stops listening and exits cleanly. This is NOT a public relay service — no
third-party tunnel is involved; remote clients talk to your listener
directly. The backing server itself stays loopback-only.

Examples:
  pharos expose web --addr :9500              # share on all interfaces
  pharos expose web --addr 127.0.0.1:9500     # token-authed loopback only
  pharos expose web --addr :9500 --ttl 2h     # shorter lifetime
  pharos expose web --addr :9500 --background # detach after showing the token
  pharos expose list                          # show exposes + liveness
  pharos expose stop web                      # graceful stop`,
	Run: runExpose,
}

var exposeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured exposes with liveness",
	RunE:  runExposeList,
}

var exposeStopCmd = &cobra.Command{
	Use:   "stop <name>",
	Short: "Gracefully stop a running expose",
	Args:  cobra.ExactArgs(1),
	Run:   runExposeStop,
}

func init() {
	exposeCmd.Flags().StringVar(&exposeAddr, "addr", "", "public listen address, host:port (REQUIRED) — e.g. :9500 for all interfaces or 127.0.0.1:9500 for loopback only")
	exposeCmd.Flags().DurationVar(&exposeTTL, "ttl", expose.DefaultTTL, "lifetime of the expose (max 24h)")
	exposeCmd.Flags().BoolVar(&exposeBackground, "background", false, "start detached in the background after showing the token")
	exposeCmd.Flags().BoolVar(&exposeJSON, "json", false, "output as JSON")
	exposeCmd.Flags().BoolVar(&exposeInternal, "expose-internal", false, "internal: run the expose worker loop")
	exposeCmd.Flags().MarkHidden("expose-internal")

	exposeListCmd.Flags().BoolVar(&exposeJSON, "json", false, "output as JSON")

	exposeCmd.AddCommand(exposeListCmd)
	exposeCmd.AddCommand(exposeStopCmd)
	rootCmd.AddCommand(exposeCmd)
}

// ── Preconditions ────────────────────────────────────────────────────────

// resolveExposeTarget verifies the daemon is running and manages name, and
// returns the daemon proxy port requests should forward to. Errors mirror
// `pharos daemon status` style; the caller renders and exits.
func resolveExposeTarget(state *daemon.DaemonState, name string) (backingPort int, managed []string, err error) {
	if state == nil || state.PID <= 0 {
		return 0, nil, fmt.Errorf("daemon is not running")
	}
	if !exposeDaemonAliveFn(state.PID) {
		return 0, nil, fmt.Errorf("daemon is not running")
	}
	if len(state.Servers) == 0 {
		return 0, nil, fmt.Errorf("daemon is not managing any servers")
	}
	ss, ok := state.Servers[name]
	if !ok || ss.Port <= 0 {
		names := make([]string, 0, len(state.Servers))
		for n := range state.Servers {
			names = append(names, n)
		}
		sortStringsCmd(names)
		return 0, names, fmt.Errorf("server %q is not managed by the daemon", name)
	}
	return ss.Port, nil, nil
}

// sortStringsCmd sorts in place (small helper for the managed-names hint).
func sortStringsCmd(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// parseExposeAddr validates the --addr flag: host:port with an explicit
// non-zero port. The host may be empty (":9500" = all interfaces) — the
// public bind is opt-in precisely because --addr must be passed.
func parseExposeAddr(addr string) (host string, port int, err error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("--addr must be host:port (e.g. :9500 or 127.0.0.1:9500)")
	}
	port, err = strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("--addr port %q is not a valid TCP port (1-65535)", portStr)
	}
	return host, port, nil
}

// ── Command entry points ─────────────────────────────────────────────────

// runExpose dispatches: with the hidden --expose-internal flag this process
// IS the expose worker; otherwise it is the CLI front end.
func runExpose(cmd *cobra.Command, args []string) {
	if exposeInternal {
		runExposeWorker(cmd, args)
		return
	}
	if len(args) == 0 {
		_ = cmd.Help()
		return
	}
	runExposeStart(cmd, args[0])
}

// runExposeStart validates everything, then either serves in the foreground
// or backgrounds a worker (repo pattern: re-exec with a hidden internal
// flag + detachProcess, as daemon start does).
func runExposeStart(cmd *cobra.Command, name string) {
	// --addr is REQUIRED, with guidance — a public bind must be an explicit
	// decision, never a silent default.
	if exposeAddr == "" {
		fmt.Fprintln(os.Stderr, ui.Error.Render("--addr is required — expose must be told where to listen."))
		fmt.Fprintln(os.Stderr, ui.Muted.Render("  e.g. --addr :9500 (all interfaces) or --addr 127.0.0.1:9500 (loopback only)."))
		osExit(1)
	}
	_, port, err := parseExposeAddr(exposeAddr)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Invalid --addr:"), err)
		osExit(1)
	}
	ttl, err := expose.ValidateTTL(exposeTTL)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Invalid --ttl:"), err)
		osExit(1)
	}

	// Preconditions: daemon running + name managed (friendly error, exit 1 —
	// mirrors `pharos daemon status`).
	state, err := daemon.ReadState()
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot read daemon state:"), err)
		osExit(1)
	}
	backingPort, managed, err := resolveExposeTarget(state, name)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot expose:"), err)
		if managed != nil {
			fmt.Fprintf(os.Stderr, "  %s  %s\n", ui.Muted.Render("Managed by daemon:"), strings.Join(managed, ", "))
		} else {
			fmt.Fprintf(os.Stderr, "\n  %s  %s\n", ui.Muted.Render("Start it with:"), "pharos daemon start")
		}
		osExit(1)
	}

	// One expose per name: a live entry means a worker already owns the name.
	if e, ok, _ := expose.GetEntry(name); ok && e.PID > 0 && exposeAliveFn(e.PID) && time.Now().Before(e.ExpiresAt) {
		fmt.Fprintf(os.Stderr, "%s  an expose for %s is already running (PID %d)\n",
			ui.Error.Render("✗"), name, e.PID)
		fmt.Fprintf(os.Stderr, "  %s  %s\n", ui.Muted.Render("Stop it first:"), fmt.Sprintf("pharos expose stop %s", name))
		osExit(1)
	}

	token, err := expose.GenerateToken()
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot generate token:"), err)
		osExit(1)
	}
	now := time.Now()
	entry := expose.Entry{
		Name:        name,
		Addr:        exposeAddr,
		Port:        port,
		BackingPort: backingPort,
		TokenHash:   expose.HashToken(token),
		CreatedAt:   now,
		ExpiresAt:   now.Add(ttl),
	}

	if exposeBackground {
		startExposeWorker(entry, token, ttl)
		return
	}

	// Foreground: bind FIRST so a taken port fails loudly before any output.
	ln, err := expose.Listen(exposeAddr)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot listen on "+exposeAddr+":"), err)
		osExit(1)
	}
	entry.PID = os.Getpid()
	if err := expose.UpsertEntry(entry); err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot record expose state:"), err)
		osExit(1)
	}
	// A stop request that predates this process is stale by definition —
	// clear it so the first 1s stop tick cannot kill the fresh tunnel
	// (W5.3 review R-2).
	expose.ClearStop(name)

	printExposeHandoff(entry, token, ttl)
	// Stop instruction + serving notice go through progressf: stdout in
	// human mode, stderr in JSON mode — the handoff document stays the
	// only stdout output in both modes (W5.3 review R-1; the spec's
	// "handoff: stop instructions" note is honored on stderr in JSON).
	progressf("  %s  Ctrl-C here, or 'pharos expose stop %s' from another shell\n",
		ui.Muted.Render("Stop it:"), entry.Name)
	progressf("\n%s\n", ui.Muted.Render("Serving... (Ctrl-C to stop)"))

	err = expose.ServeListener(ln, expose.ServeConfig{
		Entry:  entry,
		Token:  token,
		TTL:    ttl,
		OnStop: notifyExposeStopped,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Expose stopped with error:"), err)
		osExit(1)
	}
}

// notifyExposeStopped prints the graceful-shutdown notice (human mode only —
// JSON stdout stays a single pure document).
func notifyExposeStopped(cause string) {
	if JSONRequested() {
		fmt.Fprintf(os.Stderr, "expose stopped (%s)\n", cause)
		return
	}
	fmt.Printf("\n%s  expose stopped (%s) — state cleaned up\n",
		ui.Muted.Render("·"), cause)
}

// printExposeHandoff emits the one-time token handoff shared by foreground
// and background starts: the banner, the token (human mode) or the pure
// JSON start document ({name, addr, port, expiresAt, token} — --json), and
// the ready-to-copy client snippet. Callers append their own footer lines.
func printExposeHandoff(entry expose.Entry, token string, ttl time.Duration) {
	if JSONRequested() {
		data, err := json.MarshalIndent(struct {
			Name      string `json:"name"`
			Addr      string `json:"addr"`
			Port      int    `json:"port"`
			ExpiresAt string `json:"expiresAt"`
			Token     string `json:"token"`
		}{
			Name:      entry.Name,
			Addr:      entry.Addr,
			Port:      entry.Port,
			ExpiresAt: entry.ExpiresAt.UTC().Format(time.RFC3339),
			Token:     token,
		}, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot encode expose start:"), err)
			osExit(1)
		}
		fmt.Println(string(data))
		return
	}

	target := fmt.Sprintf("127.0.0.1:%d", entry.BackingPort)
	fmt.Printf("%s  exposing %s → %s on %s\n",
		ui.Success.Render("✓"), ui.PackageName.Render(entry.Name),
		ui.Muted.Render(target), ui.Label.Render(entry.Addr))
	fmt.Printf("\n  %s (shown once; stored only as a SHA-256 hash in %s):\n",
		ui.Label.Render("Token"), ui.Muted.Render("~/.pharos/expose.json"))
	fmt.Printf("    %s\n", token)
	fmt.Printf("\n  %s\n", ui.Label.Render("Remote clients:"))
	fmt.Printf("    curl -H \"Authorization: Bearer %s\" http://<host>:%d/mcp\n", token, entry.Port)
	fmt.Printf("\n  %s\n", ui.Label.Render("MCP client header:"))
	fmt.Printf("    \"Authorization\": \"Bearer %s\"\n", token)
	fmt.Printf("\n  %s  %s (in %s) — the expose stops itself at that time\n",
		ui.Muted.Render("Expires:"), entry.ExpiresAt.UTC().Format(time.RFC3339), formatDuration(ttl))
}

// startExposeWorker spawns the detached background worker (repo pattern:
// self re-exec + hidden internal flag + detachProcess) and waits briefly
// for the worker to record its store entry, mirroring `daemon start`'s
// PID-confirmation wait. Output rules: --json keeps stdout to the single
// start document; every other line goes through progressf (stderr in JSON
// mode).
func startExposeWorker(entry expose.Entry, token string, ttl time.Duration) {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot determine executable path:"), err)
		osExit(1)
	}

	// Worker output goes to the expose log, never to a (detached) terminal.
	logPath, err := expose.LogPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot resolve expose log path:"), err)
		osExit(1)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot open expose log:"), err)
		osExit(1)
	}

	bgCmd := exec.Command(exe, "expose", entry.Name,
		"--expose-internal", "--addr", entry.Addr, "--ttl", ttl.String())
	bgCmd.Stdin = nil
	bgCmd.Stdout = logFile
	bgCmd.Stderr = logFile
	bgCmd.Env = append(os.Environ(), exposeTokenEnv+"="+token)
	detachProcess(bgCmd)

	if err := bgCmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Failed to background expose:"), err)
		osExit(1)
	}
	logFile.Close()

	if JSONRequested() {
		// The pure stdout document is the handoff; progress goes to stderr.
		printExposeHandoff(entry, token, ttl)
		progressf("  %s  backgrounding expose (PID %d)...\n", ui.Muted.Render("·"), bgCmd.Process.Pid)
	} else {
		fmt.Printf("%s  starting expose (background, PID %d)...\n",
			ui.Label.Render("pharos expose"), bgCmd.Process.Pid)
		printExposeHandoff(entry, token, ttl)
		fmt.Printf("  %s  runs detached — stop with 'pharos expose stop %s'\n",
			ui.Muted.Render("Stop it:"), entry.Name)
	}

	// Wait for the worker to bind + record its entry (it owns the real PID).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if e, ok, _ := expose.GetEntry(entry.Name); ok && e.PID > 0 && exposeAliveFn(e.PID) {
			progressf("%s  expose running (PID %d) on %s\n",
				ui.Success.Render("✓"), e.PID, e.Addr)
			progressf("  %s  %s\n", ui.Muted.Render("Check:"), "pharos expose list")
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	progressf("%s  expose process started but not confirmed yet (port may be taken)\n",
		ui.Muted.Render("⚠"))
	progressf("  %s  %s\n", ui.Muted.Render("Check:"), "pharos expose list and ~/.pharos/expose.log")
}

// ── Worker (background process) ──────────────────────────────────────────

// runExposeWorker is the detached expose process: it re-verifies the
// target, binds the listener, records its entry, and serves until the TTL,
// a stop request, or a signal. All output goes to ~/.pharos/expose.log;
// stdout stays silent (the process is detached).
func runExposeWorker(cmd *cobra.Command, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, ui.Error.Render("expose worker: missing server name"))
		osExit(1)
	}
	name := args[0]

	logger, closeLog := exposeWorkerLogger()
	defer closeLog()

	token := os.Getenv(exposeTokenEnv)
	if token == "" {
		logger.Printf("ERROR: %s is not set — cannot start expose for %s", exposeTokenEnv, name)
		osExit(1)
	}
	if exposeAddr == "" {
		logger.Printf("ERROR: --addr is required")
		osExit(1)
	}
	_, port, err := parseExposeAddr(exposeAddr)
	if err != nil {
		logger.Printf("ERROR: invalid --addr: %v", err)
		osExit(1)
	}
	ttl, err := expose.ValidateTTL(exposeTTL)
	if err != nil {
		logger.Printf("ERROR: invalid --ttl: %v", err)
		osExit(1)
	}

	// Re-verify the target: the daemon may have died since the parent checked.
	state, err := daemon.ReadState()
	if err != nil {
		logger.Printf("ERROR: cannot read daemon state: %v", err)
		osExit(1)
	}
	backingPort, _, err := resolveExposeTarget(state, name)
	if err != nil {
		logger.Printf("ERROR: %v", err)
		osExit(1)
	}

	ln, err := expose.Listen(exposeAddr)
	if err != nil {
		logger.Printf("ERROR: cannot listen on %s: %v", exposeAddr, err)
		osExit(1)
	}

	entry := expose.Entry{
		Name:        name,
		PID:         os.Getpid(),
		Addr:        exposeAddr,
		Port:        port,
		BackingPort: backingPort,
		TokenHash:   expose.HashToken(token),
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(ttl),
	}
	if err := expose.UpsertEntry(entry); err != nil {
		logger.Printf("ERROR: cannot record expose state: %v", err)
		ln.Close()
		osExit(1)
	}
	// A stop request that predates this process is stale by definition —
	// clear it so the first 1s stop tick cannot kill the fresh tunnel
	// (W5.3 review R-2).
	expose.ClearStop(name)

	logger.Printf("expose %s listening on %s -> 127.0.0.1:%d (token sha256:%s, expires %s)",
		name, exposeAddr, backingPort, expose.Fingerprint(entry.TokenHash),
		entry.ExpiresAt.UTC().Format(time.RFC3339))

	err = expose.ServeListener(ln, expose.ServeConfig{
		Entry: entry,
		Token: token,
		TTL:   ttl,
		OnStop: func(cause string) {
			logger.Printf("expose %s stopped (%s)", name, cause)
		},
	})
	if err != nil {
		logger.Printf("ERROR: expose %s stopped with error: %v", name, err)
		osExit(1)
	}
}

// exposeWorkerLogger wires the stdlib logger at the expose log path.
func exposeWorkerLogger() (*log.Logger, func()) {
	path, err := expose.LogPath()
	if err != nil {
		return log.New(os.Stderr, "expose: ", log.LstdFlags), func() {}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return log.New(os.Stderr, "expose: ", log.LstdFlags), func() {}
	}
	return log.New(f, "", log.LstdFlags), func() { f.Close() }
}

// ── list ─────────────────────────────────────────────────────────────────

// exposeListOut is the JSON shape of `pharos expose list --json`. live and
// expired are always present (false is meaningful); pid is omitted when the
// entry never recorded one.
type exposeListOut struct {
	Exposes []exposeEntryOut `json:"exposes"`
}

type exposeEntryOut struct {
	Name        string `json:"name"`
	Addr        string `json:"addr"`
	Port        int    `json:"port"`
	BackingPort int    `json:"backingPort"`
	PID         int    `json:"pid,omitempty"`
	Live        bool   `json:"live"`
	Expired     bool   `json:"expired"`
	ExpiresAt   string `json:"expiresAt"`
}

// classifyExpose derives the display state of an entry: "expired" wins
// (a dead process past its TTL is just expired), then PID liveness.
func classifyExpose(e expose.Entry, now time.Time) (live, expired bool, status string) {
	if now.After(e.ExpiresAt) {
		return false, true, "expired"
	}
	if e.PID > 0 && exposeAliveFn(e.PID) {
		return true, false, "live"
	}
	return false, false, "stopped"
}

func runExposeList(cmd *cobra.Command, args []string) error {
	entries, err := expose.LoadEntries()
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot read expose state:"), err)
		osExit(1)
	}

	if JSONRequested() {
		out := exposeListOut{Exposes: []exposeEntryOut{}}
		for _, e := range entries {
			live, expired, _ := classifyExpose(e, time.Now())
			item := exposeEntryOut{
				Name:        e.Name,
				Addr:        e.Addr,
				Port:        e.Port,
				BackingPort: e.BackingPort,
				PID:         e.PID,
				Live:        live,
				Expired:     expired,
				ExpiresAt:   e.ExpiresAt.UTC().Format(time.RFC3339),
			}
			out.Exposes = append(out.Exposes, item)
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	if len(entries) == 0 {
		fmt.Println(ui.Muted.Render("No exposes configured."))
		fmt.Printf("\n  %s  %s\n", ui.Muted.Render("Share a server with:"), "pharos expose <name> --addr :9500")
		return nil
	}

	cols := []ui.TableColumn{
		{Title: "NAME", Width: 22, MaxWidth: 0},
		{Title: "ADDR", Width: 22, MaxWidth: 22},
		{Title: "TARGET", Width: 14, MaxWidth: 14},
		{Title: "PID", Width: 8, MaxWidth: 8},
		{Title: "STATUS", Width: 10, MaxWidth: 10},
		{Title: "EXPIRES", Width: 20, MaxWidth: 20},
	}
	var rows []ui.TableRow
	for _, e := range entries {
		_, _, status := classifyExpose(e, time.Now())
		pid := ui.Muted.Render("—")
		if e.PID > 0 {
			pid = strconv.Itoa(e.PID)
		}
		var statusStr string
		switch status {
		case "live":
			statusStr = ui.Success.Render(status)
		case "expired":
			statusStr = ui.Warning.Render(status)
		default:
			statusStr = ui.Muted.Render(status)
		}
		rows = append(rows, ui.TableRow{
			ui.PackageName.Render(e.Name),
			ui.Muted.Render(e.Addr),
			fmt.Sprintf("127.0.0.1:%d", e.BackingPort),
			pid,
			statusStr,
			e.ExpiresAt.Local().Format("Jan 2 15:04 MST"),
		})
	}
	fmt.Printf("%s  %s\n\n", ui.Label.Render("pharos expose"), ui.Muted.Render("~/.pharos/expose.json"))
	fmt.Print(ui.RenderTable(cols, rows))
	return nil
}

// ── stop ─────────────────────────────────────────────────────────────────

// runExposeStop gracefully stops a running expose: it files the stop
// request (the same Windows-safe cooperative pattern the daemon uses),
// waits for the process to clean up its own store entry, and removes
// stale leftovers when the process is already gone.
func runExposeStop(cmd *cobra.Command, args []string) {
	name := args[0]

	entry, ok, err := expose.GetEntry(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot read expose state:"), err)
		osExit(1)
	}
	if !ok {
		fmt.Fprintln(os.Stderr, ui.Error.Render("No expose found for "+name))
		fmt.Fprintf(os.Stderr, "  %s  %s\n", ui.Muted.Render("Check:"), "pharos expose list")
		osExit(1)
	}

	// Already dead: clean up the stale entry and any leftover stop file.
	if entry.PID <= 0 || !exposeAliveFn(entry.PID) {
		expose.ClearStop(name)
		if _, err := expose.RemoveEntry(name, entry.TokenHash, entry.PID); err != nil {
			fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot clean up expose state:"), err)
			osExit(1)
		}
		fmt.Printf("%s  removed stale expose entry for %s (process not running)\n",
			ui.Success.Render("✓"), ui.PackageName.Render(name))
		return
	}

	if err := expose.RequestStop(name); err != nil {
		fmt.Fprintln(os.Stderr, ui.Error.Render("Cannot write stop request:"), err)
		osExit(1)
	}
	progressf("%s  stop requested for %s (PID %d)...\n",
		ui.Muted.Render("·"), ui.PackageName.Render(name), entry.PID)

	// The expose process removes its own store entry on graceful shutdown —
	// wait for that (with a PID-liveness fallback for crashed processes).
	deadline := time.Now().Add(exposeStopWait)
	for time.Now().Before(deadline) {
		if _, still, _ := expose.GetEntry(name); !still {
			fmt.Printf("%s  expose %s stopped\n", ui.Success.Render("✓"), ui.PackageName.Render(name))
			return
		}
		if !exposeAliveFn(entry.PID) {
			// Process died without cleanup (crash / SIGKILL) — remove the entry.
			expose.ClearStop(name)
			_, _ = expose.RemoveEntry(name, entry.TokenHash, entry.PID)
			fmt.Printf("%s  expose %s stopped (cleaned up after dead PID %d)\n",
				ui.Success.Render("✓"), ui.PackageName.Render(name), entry.PID)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}

	fmt.Fprintf(os.Stderr, "%s  expose %s did not stop within %s (PID %d)\n",
		ui.Error.Render("✗"), name, exposeStopWait, entry.PID)
	osExit(1)
}
