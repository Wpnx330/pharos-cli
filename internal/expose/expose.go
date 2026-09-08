// Package expose implements the engine behind `pharos expose` (W5.3 A5,
// scoped form): a token-authed reverse proxy in front of one daemon-managed
// server's loopback proxy listener.
//
// Security posture (this is the product's first non-loopback binding):
//
//   - Default-deny: every request must carry "Authorization: Bearer <token>";
//     anything else gets a 401 JSON envelope and NEVER reaches the backing
//     server. Comparison is constant-time (crypto/subtle), never ==.
//   - The token is generated from crypto/rand (32 bytes, base64url). At rest
//     only its SHA-256 hash is persisted (~/.pharos/expose.json) — a local
//     read of the store must not yield the bearer token. The plain token is
//     printed once by the CLI at start and passed to the expose process via
//     its environment; it is never logged.
//   - The validated Authorization header is stripped before proxying, so the
//     public token is not propagated to the local daemon/backing server.
//   - TTL is enforced by the expose process itself: on expiry the listener
//     closes and the process exits cleanly (default 8h, cap 24h).
//   - Shutdown is graceful on SIGINT/SIGTERM, on TTL expiry, and on a stop
//     request file (~/.pharos/expose.stop/<name>) — the same file-based
//     control pattern the daemon uses for Windows-safe signaling.
//
// The package never touches daemon code or daemon state files: the daemon
// is a separate process, and a crash here must never affect it.
package expose

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ── TTL policy ───────────────────────────────────────────────────────────

const (
	// DefaultTTL is the lifetime of an expose when --ttl is omitted.
	DefaultTTL = 8 * time.Hour
	// MaxTTL caps every expose. Long-lived tunnels are a different feature
	// with different key-rotation semantics; they are deliberately out of
	// scope for A5.
	MaxTTL = 24 * time.Hour
)

// ValidateTTL checks a user-supplied TTL. Non-positive and > MaxTTL are
// errors; everything in between is returned unchanged (no silent clamping —
// the operator asked for a specific lifetime).
func ValidateTTL(d time.Duration) (time.Duration, error) {
	if d <= 0 {
		return 0, fmt.Errorf("ttl must be positive (got %s)", d)
	}
	if d > MaxTTL {
		return 0, fmt.Errorf("ttl %s exceeds the %s maximum — long-lived tunnels are a different feature", d, MaxTTL)
	}
	return d, nil
}

// ── Token generation and hashing ─────────────────────────────────────────

// GenerateToken returns a 256-bit token, base64url-encoded without padding
// (43 characters). The entropy comes from crypto/rand.
func GenerateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate expose token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken returns the SHA-256 hex digest of the token. This is the only
// form of the token that is ever persisted at rest.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Fingerprint returns a short, non-reversible prefix of the token hash for
// display in listings. Safe to show: it does not reconstruct the token.
func Fingerprint(tokenHash string) string {
	if len(tokenHash) > 8 {
		return tokenHash[:8]
	}
	return tokenHash
}

// ── Bearer authorization ─────────────────────────────────────────────────

// bearerToken extracts the credentials from an Authorization header value.
// Only the Bearer scheme is accepted (case-insensitive, per RFC 7235);
// empty, malformed, and wrong-scheme values all yield "".
func bearerToken(header string) string {
	const prefix = "bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

// authorized reports whether the request carries exactly the expected
// token. The comparison is constant-time; an empty presented token never
// matches (ConstantTimeCompare returns 0 for length mismatch, and the
// empty-token case is rejected before it anyway).
func authorized(r *http.Request, token string) bool {
	got := bearerToken(r.Header.Get("Authorization"))
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// errorShape matches the registry error envelope: {"error": {code, message}}.
type errorShape struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteError emits the registry-style error envelope with the given status.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]errorShape{"error": {Code: code, Message: message}})
}

// AuthMiddleware denies every request that does not carry the exact bearer
// token. Denied requests get a 401 JSON envelope and never reach next.
func AuthMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, token) {
			WriteError(w, http.StatusUnauthorized, "unauthorized",
				"missing or invalid bearer token")
			return
		}
		// The token has done its job — strip it so the public credential is
		// not propagated into the local daemon/backing server (their logs
		// stay free of the bearer token).
		r.Header.Del("Authorization")
		next.ServeHTTP(w, r)
	})
}

// ── Reverse proxy ────────────────────────────────────────────────────────

// NewProxy returns a reverse proxy to the daemon's loopback proxy listener
// for one server. FlushInterval -1 flushes immediately so SSE/streamable
// streams are not buffered. Proxy errors become a 502 JSON envelope.
func NewProxy(backingPort int) http.Handler {
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(backingPort))}
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.FlushInterval = -1
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		WriteError(w, http.StatusBadGateway, "backing_unreachable",
			"cannot reach the daemon proxy for this server (it may have stopped)")
	}
	return rp
}

// Handler assembles the full public handler chain: auth gate → proxy.
func Handler(token string, backingPort int) http.Handler {
	return AuthMiddleware(token, NewProxy(backingPort))
}

// ── Store (~/.pharos/expose.json) ────────────────────────────────────────

// StoreVersion is the schema version of expose.json.
const StoreVersion = 1

// Entry is one running (or recently dead) expose process.
// TokenHash is the SHA-256 hex of the bearer token — never the token itself.
type Entry struct {
	Name        string    `json:"name"`
	PID         int       `json:"pid"`         // expose process PID; 0 = unknown
	Addr        string    `json:"addr"`        // public listen address as configured
	Port        int       `json:"port"`        // public port
	BackingPort int       `json:"backingPort"` // daemon proxy port for the server
	TokenHash   string    `json:"tokenHash"`   // SHA-256 hex — NOT the token
	CreatedAt   time.Time `json:"createdAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// store is the on-disk shape of expose.json.
type store struct {
	Version int              `json:"version"`
	Exposes map[string]Entry `json:"exposes"`
}

// DirFn is the expose home directory (~/.pharos), overridable by tests to
// isolate filesystem state (cmd-level tests and this package's own tests).
var DirFn = defaultDir

func defaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".pharos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// StorePath returns the path to expose.json.
func StorePath() (string, error) {
	dir, err := DirFn()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "expose.json"), nil
}

// StopDirPath returns the directory holding per-name stop request files.
func StopDirPath() (string, error) {
	dir, err := DirFn()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "expose.stop"), nil
}

// LogPath returns the path to the expose log (background workers only).
func LogPath() (string, error) {
	dir, err := DirFn()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "expose.log"), nil
}

// loadStore reads expose.json. A missing file is an empty store, not an
// error. A present-but-unparsable file is an error — the operator should
// know their control state is unreadable rather than silently reset it.
func loadStore() (*store, error) {
	path, err := StorePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &store{Version: StoreVersion, Exposes: map[string]Entry{}}, nil
		}
		return nil, fmt.Errorf("read expose store: %w", err)
	}
	var st store
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse expose store: %w", err)
	}
	if st.Exposes == nil {
		st.Exposes = map[string]Entry{}
	}
	return &st, nil
}

// saveStore writes expose.json atomically (temp file + rename) with 0600 —
// token hashes are credentials-adjacent state, not world-readable data.
func saveStore(st *store) error {
	path, err := StorePath()
	if err != nil {
		return err
	}
	st.Version = StoreVersion
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write expose store: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace expose store: %w", err)
	}
	return nil
}

// lockStore takes an exclusive lock around read-modify-write cycles on
// expose.json. Multiple expose processes (and the stop/list commands) can
// touch the file concurrently; the lock keeps an entry from being lost
// between a read and its write. It is advisory: a crashed holder's lock is
// stolen after lockStaleAge, and a live lock held past the two-second
// deadline fails with an error rather than proceeding unlocked.
func lockStore() (func(), error) {
	path, err := StorePath()
	if err != nil {
		return nil, err
	}
	lockPath := path + ".lock"
	deadline := time.Now().Add(lockTimeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { os.Remove(lockPath) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("lock expose store: %w", err)
		}
		// Steal a stale lock (holder died mid-write) after the grace age.
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > lockStaleAge {
			os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			// Failing loudly beats racing an unseen writer: an unlocked
			// read-modify-write could drop a concurrent expose's entry.
			// Stale locks are stolen above, so this only fires while a real
			// writer holds the lock — retrying the command is safe.
			return nil, fmt.Errorf("expose store is locked by another process (%s) — retry in a moment", lockPath)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// lockTimeout bounds how long store writers wait for a live lock before
// failing. It is a var so tests can shrink it.
var lockTimeout = 2 * time.Second

const lockStaleAge = 5 * time.Second

// UpsertEntry inserts or replaces the entry for e.Name under the store
// lock, persisting immediately.
func UpsertEntry(e Entry) error {
	unlock, err := lockStore()
	if err != nil {
		return err
	}
	defer unlock()
	st, err := loadStore()
	if err != nil {
		return err
	}
	st.Exposes[e.Name] = e
	return saveStore(st)
}

// RemoveEntry deletes the entry for name — but only when it still matches
// the caller's fingerprint of that entry — and persists. The guard stops a
// late shutdown of an old process from clobbering a newer expose's entry.
// It reports whether an entry was removed.
func RemoveEntry(name, tokenHash string, pid int) (bool, error) {
	unlock, err := lockStore()
	if err != nil {
		return false, err
	}
	defer unlock()
	st, err := loadStore()
	if err != nil {
		return false, err
	}
	existing, ok := st.Exposes[name]
	if !ok {
		return false, nil
	}
	if existing.TokenHash != tokenHash || existing.PID != pid {
		return false, nil
	}
	delete(st.Exposes, name)
	return true, saveStore(st)
}

// LoadEntries returns all entries sorted by name (read-only; no locking —
// listing tolerates a torn last write by design of the atomic rename).
func LoadEntries() ([]Entry, error) {
	st, err := loadStore()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(st.Exposes))
	for name := range st.Exposes {
		names = append(names, name)
	}
	sortStrings(names)
	entries := make([]Entry, 0, len(names))
	for _, name := range names {
		entries = append(entries, st.Exposes[name])
	}
	return entries, nil
}

// GetEntry returns the entry for name.
func GetEntry(name string) (Entry, bool, error) {
	st, err := loadStore()
	if err != nil {
		return Entry{}, false, err
	}
	e, ok := st.Exposes[name]
	return e, ok, nil
}

// sortStrings is a tiny local sort to keep the package dependency-light.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// ── Stop requests (Windows-safe cooperative shutdown) ────────────────────

// RequestStop writes the stop-request file for name. A running expose
// process polls for this file and shuts down gracefully — the same
// file-based control pattern the daemon uses for reload/stop on Windows.
func RequestStop(name string) error {
	dir, err := StopDirPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create expose stop dir: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, filepath.Base(name)), []byte("stop"), 0o600)
}

// stopRequested reports whether a stop-request file exists for name.
func StopRequested(name string) bool {
	dir, err := StopDirPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, filepath.Base(name)))
	return err == nil
}

// clearStop removes the stop-request file for name (shutdown cleanup).
func ClearStop(name string) {
	dir, err := StopDirPath()
	if err != nil {
		return
	}
	os.Remove(filepath.Join(dir, filepath.Base(name)))
}

// ── Serve loop ───────────────────────────────────────────────────────────

// ServeConfig configures ServeListener. Zero-value fields select defaults;
// Now/StopPoll/SignalCh are injection points for deterministic tests.
type ServeConfig struct {
	Entry Entry         // identity of this expose (Name, Addr, Port, BackingPort, TokenHash)
	Token string        // plain bearer token (never persisted, never logged)
	TTL   time.Duration // remaining lifetime; expiry closes the listener

	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
	// StopPoll is the stop-request file polling interval (default 1s).
	StopPoll time.Duration
	// SignalCh overrides the SIGINT/SIGTERM channel (tests). nil = real signals.
	SignalCh <-chan os.Signal
	// OnStop fires once on graceful shutdown with the cause (tests/telemetry).
	OnStop func(cause string)
}

// Listen binds the public listener. The address is opt-in by contract: the
// CLI refuses to run expose without an explicit --addr, and whatever it
// passes (0.0.0.0, a LAN IP, or 127.0.0.1) is what binds here.
func Listen(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

// ServeListener runs the expose server on an already-bound listener until
// the TTL expires, a signal arrives, or a stop request is filed — then
// closes the listener, removes its store entry, and returns nil (clean
// exit). A listener-level failure (accept error) returns the error.
func ServeListener(l net.Listener, cfg ServeConfig) error {
	if cfg.Entry.TokenHash == "" || cfg.Token == "" {
		return fmt.Errorf("expose requires a token and its recorded hash")
	}
	if cfg.TTL <= 0 {
		return fmt.Errorf("expose ttl must be positive")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	stopPoll := cfg.StopPoll
	if stopPoll <= 0 {
		stopPoll = time.Second
	}

	// TTL enforcement lives here, in the expose process: expiry stops the
	// listener and exits cleanly. No external watchdog involved.
	ttlExpired := make(chan struct{})
	ttl := time.AfterFunc(cfg.TTL, func() { close(ttlExpired) })
	defer ttl.Stop()

	server := &http.Server{Handler: Handler(cfg.Token, cfg.Entry.BackingPort)}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(l) }()

	sigCh := cfg.SignalCh
	if sigCh == nil {
		owned := make(chan os.Signal, 1)
		signal.Notify(owned, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(owned)
		sigCh = owned
	}

	stopTick := time.NewTicker(stopPoll)
	defer stopTick.Stop()

	cause := ""
	for cause == "" {
		select {
		case err := <-serveErr:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				// A non-shutdown accept failure: clean up state, report it.
				removeOwnEntry(cfg.Entry)
				ClearStop(cfg.Entry.Name)
				return fmt.Errorf("expose listener: %w", err)
			}
			cause = "listener-closed"
		case <-ttlExpired:
			cause = "ttl"
		case s := <-sigCh:
			cause = fmt.Sprintf("signal:%v", s)
		case <-stopTick.C:
			if StopRequested(cfg.Entry.Name) {
				cause = "stop-request"
			}
		}
	}

	// Graceful drain: let in-flight requests finish, then force.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	_ = l.Close()

	removeOwnEntry(cfg.Entry)
	ClearStop(cfg.Entry.Name)
	if cfg.OnStop != nil {
		cfg.OnStop(cause)
	}
	return nil
}

// removeOwnEntry drops this expose's store entry, but only if it still
// describes this process (same pid + token hash) — never a newer expose's.
func removeOwnEntry(e Entry) {
	if e.PID <= 0 {
		return
	}
	_, _ = RemoveEntry(e.Name, e.TokenHash, e.PID)
}
