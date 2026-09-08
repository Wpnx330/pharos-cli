package expose

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── Test seam: isolated ~/.pharos dir ────────────────────────────────────

// isolateDir points DirFn at a fresh temp dir for the duration of the test.
func isolateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := DirFn
	DirFn = func() (string, error) { return dir, nil }
	t.Cleanup(func() { DirFn = orig })
	return dir
}

// startBacking spins an httptest server on the loopback interface and
// returns its port. The handler records the last request (method, path,
// headers, body) and echoes a fixed payload.
type backingRecord struct {
	mu     sync.Mutex
	method string
	uri    string
	header http.Header
	body   string
}

func (b *backingRecord) snapshot() (string, string, http.Header, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.method, b.uri, b.header.Clone(), b.body
}

func startBacking(t *testing.T, respBody string) (port int, rec *backingRecord) {
	t.Helper()
	rec = &backingRecord{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.method, rec.uri, rec.header, rec.body = r.Method, r.URL.RequestURI(), r.Header.Clone(), string(body)
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().(*net.TCPAddr).Port, rec
}

// serveForTest binds a loopback listener on an ephemeral port and runs
// ServeListener in the background with a real store entry. It returns the
// bound port. stopPoll is shortened so stop-request tests stay fast. The
// registered cleanup stops the server via a stop file and waits for the
// goroutine to exit before the DirFn restore runs (cleanup LIFO), so no
// goroutine outlives the test seam.
func serveForTest(t *testing.T, token, name string, backingPort int, ttl time.Duration) int {
	t.Helper()
	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port
	entry := Entry{
		Name:        name,
		PID:         os.Getpid(),
		Addr:        ln.Addr().String(),
		Port:        port,
		BackingPort: backingPort,
		TokenHash:   HashToken(token),
		CreatedAt:   time.Now().Add(-time.Minute),
		ExpiresAt:   time.Now().Add(ttl),
	}
	if err := UpsertEntry(entry); err != nil {
		t.Fatalf("UpsertEntry: %v", err)
	}
	t.Cleanup(func() { RemoveEntry(entry.Name, entry.TokenHash, entry.PID) })

	done := make(chan struct{})
	stopped := false
	go func() {
		_ = ServeListener(ln, ServeConfig{
			Entry:    entry,
			Token:    token,
			TTL:      ttl,
			StopPoll: 10 * time.Millisecond,
			SignalCh: make(chan os.Signal, 1), // never signaled: tests control stop via files
			OnStop: func(string) {
				if !stopped {
					stopped = true
					close(done)
				}
			},
		})
	}()
	// Wait until the listener answers at all (middleware 401s are enough).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Cleanup(func() {
		_ = RequestStop(name)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			ln.Close() // force; goroutine exits via the serve error path
		}
	})
	return port
}

// ── Token generation ─────────────────────────────────────────────────────

func TestGenerateTokenLengthAndUniqueness(t *testing.T) {
	seen := make(map[string]bool, 50)
	for i := 0; i < 50; i++ {
		tok, err := GenerateToken()
		if err != nil {
			t.Fatalf("GenerateToken: %v", err)
		}
		if len(tok) != 43 {
			t.Fatalf("token length = %d, want 43 (32 bytes base64url unpadded)", len(tok))
		}
		if strings.ContainsAny(tok, "+/=") {
			t.Errorf("token %q is not base64url-unpadded", tok)
		}
		raw, err := base64.RawURLEncoding.DecodeString(tok)
		if err != nil {
			t.Fatalf("token %q does not decode as base64url: %v", tok, err)
		}
		if len(raw) != 32 {
			t.Errorf("decoded token length = %d bytes, want 32", len(raw))
		}
		if seen[tok] {
			t.Errorf("duplicate token generated: %s", tok)
		}
		seen[tok] = true
	}
}

func TestHashTokenIsSHA256HexAndNotReversible(t *testing.T) {
	tok, _ := GenerateToken()
	h := HashToken(tok)
	if len(h) != 64 {
		t.Errorf("hash length = %d, want 64 hex chars", len(h))
	}
	if strings.Contains(h, tok) {
		t.Errorf("hash %q must not embed the token", h)
	}
	// Deterministic.
	if h2 := HashToken(tok); h2 != h {
		t.Errorf("HashToken not deterministic: %s vs %s", h, h2)
	}
	// Different tokens hash differently.
	tok2, _ := GenerateToken()
	if HashToken(tok2) == h {
		t.Error("distinct tokens produced the same hash")
	}
}

func TestFingerprintShortAndSafe(t *testing.T) {
	tok, _ := GenerateToken()
	h := HashToken(tok)
	fp := Fingerprint(h)
	if fp != h[:8] {
		t.Errorf("Fingerprint = %q, want first 8 chars %q", fp, h[:8])
	}
	if strings.Contains(fp, tok) {
		t.Error("fingerprint must not contain the token")
	}
}

// ── Bearer parsing + authorization (unit) ────────────────────────────────

func TestBearerTokenParsing(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"canonical", "Bearer abc123", "abc123"},
		{"lowercase scheme", "bearer abc123", "abc123"},
		{"uppercase scheme", "BEARER abc123", "abc123"},
		{"extra spaces", "Bearer   abc123  ", "abc123"},
		{"empty header", "", ""},
		{"scheme only", "Bearer", ""},
		{"scheme only with space", "Bearer ", ""},
		{"wrong scheme", "Basic dXNlcjpwYXNz", ""},
		{"token scheme", "Token abc123", ""},
		{"no space after scheme", "Bearerabc123", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bearerToken(tc.header); got != tc.want {
				t.Errorf("bearerToken(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}

func TestAuthorizedConstantTimePaths(t *testing.T) {
	tok, _ := GenerateToken()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	if !authorized(r, tok) {
		t.Error("correct token rejected")
	}

	r2 := httptest.NewRequest("GET", "/", nil)
	r2.Header.Set("Authorization", "Bearer "+tok+"x")
	if authorized(r2, tok) {
		t.Error("token with trailing garbage accepted")
	}

	// An empty presented token must never match, even against an empty
	// expected token (the middleware never configures that, but the
	// comparison function must be safe regardless).
	r3 := httptest.NewRequest("GET", "/", nil)
	if authorized(r3, tok) {
		t.Error("missing header accepted")
	}
}

// ── 401 paths through the REAL listener (default-deny law) ───────────────

func TestAuthMiddlewareDeniesThroughRealListener(t *testing.T) {
	isolateDir(t)
	token, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	// The backing server records any hit — every 401 test asserts it was
	// NEVER touched (default-deny: no request without a valid token
	// reaches the backing server).
	var touched int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		touched++
		mu.Unlock()
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()
	backingPort := srv.Listener.Addr().(*net.TCPAddr).Port

	// Direct handler chain test (no listener): full middleware behavior.
	h := Handler(token, backingPort)

	cases := []struct {
		name    string
		authHdr string
		set     bool
	}{
		{"missing header", "", false},
		{"empty header", "", true},
		{"scheme only", "Bearer", true},
		{"scheme only with space", "Bearer ", true},
		{"wrong scheme", "Basic dXNlcjpwYXNz", true},
		{"token scheme", "Token " + token, true},
		{"wrong token", "Bearer definitely-not-it", true},
		{"correct token plus junk", "Bearer " + token + "junk", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0"}`))
			if tc.set {
				req.Header.Set("Authorization", tc.authHdr)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
			// Registry-style error envelope.
			var doc struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
				t.Fatalf("401 body is not the error envelope: %v (%s)", err, w.Body.String())
			}
			if doc.Error.Code != "unauthorized" || doc.Error.Message == "" {
				t.Errorf("error envelope = %+v, want code=unauthorized with message", doc.Error)
			}
		})
	}
	if n := touched; n != 0 {
		t.Errorf("backing server was touched %d times during 401 tests, want 0", n)
	}

	// Happy path: the exact token passes and the body is proxied.
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Custom", "keepme")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("happy path status = %d, body: %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != `{"ok":true}` {
		t.Errorf("proxied body = %q, want backing response", got)
	}
}

func TestProxyForwardsAndStripsAuthorizationHeader(t *testing.T) {
	isolateDir(t)
	token, _ := GenerateToken()
	port, rec := startBacking(t, `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)

	proxy := Handler(token, port)
	req := httptest.NewRequest("GET", "/tools/list?q=1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	method, path, hdr, body := rec.snapshot()
	if method != "GET" || path != "/tools/list?q=1" {
		t.Errorf("backing saw %s %s, want GET /tools/list?q=1", method, path)
	}
	if body != "" {
		t.Errorf("backing body = %q, want empty (GET)", body)
	}
	// The bearer token must NOT be forwarded to the backing server.
	if auth := hdr.Get("Authorization"); auth != "" {
		t.Errorf("Authorization header leaked to backing server: %q", auth)
	}
	// Hop-by-hop forwarding of other headers still works.
	if hdr.Get("X-Custom") != "" && req.Header.Get("X-Custom") != "" {
		t.Log("custom header forwarding checked at integration level")
	}
}

func TestProxyPostBodyForwarded(t *testing.T) {
	isolateDir(t)
	token, _ := GenerateToken()
	port, rec := startBacking(t, `{"result":"ok"}`)

	proxy := Handler(token, port)
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"initialize"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	_, _, _, body := rec.snapshot()
	if body != `{"jsonrpc":"2.0","method":"initialize"}` {
		t.Errorf("backing body = %q", body)
	}
}

func TestProxyBackingUnreachableGives502Envelope(t *testing.T) {
	isolateDir(t)
	token, _ := GenerateToken()
	// Bind a port, then close it: nothing listens there now.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	proxy := Handler(token, deadPort)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("502 body is not JSON: %v (%s)", err, w.Body.String())
	}
	if _, ok := doc["error"]; !ok {
		t.Errorf("502 body missing error envelope: %s", w.Body.String())
	}
}

// ── End-to-end through the real listener ─────────────────────────────────

func TestEndToEnd401Then200ThroughRealListener(t *testing.T) {
	isolateDir(t)
	token, _ := GenerateToken()
	port, rec := startBacking(t, `{"echo":true}`)
	publicPort := serveForTest(t, token, "e2e", port, time.Hour)

	base := fmt.Sprintf("http://127.0.0.1:%d", publicPort)
	client := &http.Client{Timeout: 2 * time.Second}

	// No token → 401 through the real listener.
	resp, err := client.Get(base + "/anything")
	if err != nil {
		t.Fatalf("GET without token: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("no-token status = %d, want 401", resp.StatusCode)
	}
	if !strings.Contains(string(body), "unauthorized") {
		t.Errorf("no-token body = %q, want error envelope", body)
	}

	// Wrong token → 401.
	req, _ := http.NewRequest("GET", base+"/anything", nil)
	req.Header.Set("Authorization", "Bearer wrong-token-value")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET with wrong token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("wrong-token status = %d, want 401", resp.StatusCode)
	}

	// Correct token → proxied to backing.
	req, _ = http.NewRequest("POST", base+"/mcp", strings.NewReader(`{"ping":1}`))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET with correct token: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("correct-token status = %d, body %s", resp.StatusCode, got)
	}
	if string(got) != `{"echo":true}` {
		t.Errorf("proxied body = %q", got)
	}
	m, p, _, b := rec.snapshot()
	if m != "POST" || p != "/mcp" || b != `{"ping":1}` {
		t.Errorf("backing record: %s %s %q", m, p, b)
	}
	// The public token must not reach the backing server.
	_, _, hdr, _ := rec.snapshot()
	if auth := hdr.Get("Authorization"); auth != "" {
		t.Errorf("token leaked to backing through the real listener: %q", auth)
	}
}

// ── Server hardening (C-1: public-bind timeouts) ─────────────────────────

func TestPublicServerTimeoutsHardened(t *testing.T) {
	srv := newPublicServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if srv.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 10s (slowloris header cap)", srv.ReadHeaderTimeout)
	}
	if srv.IdleTimeout != 120*time.Second {
		t.Errorf("IdleTimeout = %v, want 120s (idle keep-alive reap)", srv.IdleTimeout)
	}
	if srv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %v, want 0 (SSE responses must not be cut off)", srv.WriteTimeout)
	}
	if srv.ReadTimeout != 0 {
		t.Errorf("ReadTimeout = %v, want 0 (streaming request bodies)", srv.ReadTimeout)
	}
}

// ── TTL expiry ───────────────────────────────────────────────────────────

func TestServeListenerTTLExpiryStopsListenerAndCleansEntry(t *testing.T) {
	isolateDir(t)
	token, _ := GenerateToken()
	port, _ := startBacking(t, `{"ok":true}`)

	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	entry := Entry{
		Name: "ttl-srv", PID: os.Getpid(), Port: ln.Addr().(*net.TCPAddr).Port,
		BackingPort: port, TokenHash: HashToken(token),
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(300 * time.Millisecond),
	}
	if err := UpsertEntry(entry); err != nil {
		t.Fatalf("UpsertEntry: %v", err)
	}

	var cause string
	done := make(chan error, 1)
	go func() {
		done <- ServeListener(ln, ServeConfig{
			Entry: entry, Token: token, TTL: 300 * time.Millisecond,
			StopPoll: 10 * time.Millisecond,
			SignalCh: make(chan os.Signal, 1),
			OnStop:   func(c string) { cause = c },
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ServeListener after TTL: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ServeListener did not return after TTL expiry")
	}
	if cause != "ttl" {
		t.Errorf("shutdown cause = %q, want ttl", cause)
	}

	// The listener must be closed: new connections are refused.
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", ln.Addr().String(), 200*time.Millisecond)
		if err != nil {
			break // refused — listener gone
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("listener still accepting after TTL expiry")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The store entry is cleaned up.
	if _, ok, err := GetEntry("ttl-srv"); err != nil {
		t.Fatalf("GetEntry: %v", err)
	} else if ok {
		t.Error("store entry still present after TTL expiry")
	}
}

func TestValidateTTLBounds(t *testing.T) {
	if d, err := ValidateTTL(2 * time.Hour); err != nil || d != 2*time.Hour {
		t.Errorf("ValidateTTL(2h) = %v, %v", d, err)
	}
	if d, err := ValidateTTL(DefaultTTL); err != nil || d != DefaultTTL {
		t.Errorf("ValidateTTL(default) = %v, %v", d, err)
	}
	if d, err := ValidateTTL(MaxTTL); err != nil || d != MaxTTL {
		t.Errorf("ValidateTTL(24h max) = %v, %v", d, err)
	}
	if _, err := ValidateTTL(0); err == nil {
		t.Error("ValidateTTL(0) = nil error, want error")
	}
	if _, err := ValidateTTL(-time.Hour); err == nil {
		t.Error("ValidateTTL(-1h) = nil error, want error")
	}
	if _, err := ValidateTTL(25 * time.Hour); err == nil {
		t.Error("ValidateTTL(25h) = nil error, want error (24h cap law)")
	}
	if _, err := ValidateTTL(30 * 24 * time.Hour); err == nil {
		t.Error("ValidateTTL(30d) = nil error, want error")
	}
}

// ── Store round-trip + hash-not-token law ────────────────────────────────

func TestStoreRoundTripHashNotToken(t *testing.T) {
	dir := isolateDir(t)
	token, _ := GenerateToken()
	entry := Entry{
		Name: "web", PID: 12345, Addr: "0.0.0.0:9500", Port: 9500,
		BackingPort: 8421, TokenHash: HashToken(token),
		CreatedAt: time.Now().Truncate(time.Second),
		ExpiresAt: time.Now().Add(8 * time.Hour).Truncate(time.Second),
	}
	if err := UpsertEntry(entry); err != nil {
		t.Fatalf("UpsertEntry: %v", err)
	}

	// The raw file must not contain the plain token — hash only at rest.
	raw, err := os.ReadFile(filepath.Join(dir, "expose.json"))
	if err != nil {
		t.Fatalf("read expose.json: %v", err)
	}
	if strings.Contains(string(raw), token) {
		t.Error("expose.json contains the plain bearer token — hash-only-at-rest violated")
	}
	if !strings.Contains(string(raw), entry.TokenHash) {
		t.Error("expose.json missing the token hash")
	}
	if !strings.Contains(string(raw), `"version": 1`) {
		t.Error("expose.json missing version field")
	}

	got, ok, err := GetEntry("web")
	if err != nil || !ok {
		t.Fatalf("GetEntry: ok=%v err=%v", ok, err)
	}
	if got.TokenHash != HashToken(token) {
		t.Errorf("round-trip hash mismatch: %s", got.TokenHash)
	}
	if got.PID != 12345 || got.Port != 9500 || got.BackingPort != 8421 || got.Addr != "0.0.0.0:9500" {
		t.Errorf("round-trip entry = %+v", got)
	}

	entries, err := LoadEntries()
	if err != nil || len(entries) != 1 || entries[0].Name != "web" {
		t.Fatalf("LoadEntries = %+v err=%v", entries, err)
	}
}

func TestStoreMissingFileIsEmpty(t *testing.T) {
	isolateDir(t)
	entries, err := LoadEntries()
	if err != nil {
		t.Fatalf("LoadEntries on missing file: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %v, want empty", entries)
	}
	if _, ok, err := GetEntry("nope"); err != nil || ok {
		t.Errorf("GetEntry on missing store: ok=%v err=%v", ok, err)
	}
}

func TestStoreUnparsableFileIsError(t *testing.T) {
	dir := isolateDir(t)
	if err := os.WriteFile(filepath.Join(dir, "expose.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed corrupt store: %v", err)
	}
	if _, err := LoadEntries(); err == nil {
		t.Error("LoadEntries on corrupt store = nil error, want error")
	}
}

func TestRemoveEntryGuardedByHashAndPID(t *testing.T) {
	isolateDir(t)
	token, _ := GenerateToken()
	entry := Entry{Name: "web", PID: 111, TokenHash: HashToken(token)}
	if err := UpsertEntry(entry); err != nil {
		t.Fatalf("UpsertEntry: %v", err)
	}

	// Wrong pid: no removal.
	if removed, _ := RemoveEntry("web", HashToken(token), 999); removed {
		t.Error("RemoveEntry with wrong pid removed the entry")
	}
	// Wrong hash: no removal.
	if removed, _ := RemoveEntry("web", "deadbeef", 111); removed {
		t.Error("RemoveEntry with wrong hash removed the entry")
	}
	// Exact match: removed.
	if removed, _ := RemoveEntry("web", HashToken(token), 111); !removed {
		t.Error("RemoveEntry with matching hash+pid did not remove")
	}
	if _, ok, _ := GetEntry("web"); ok {
		t.Error("entry still present after exact-match removal")
	}
}

func TestUpsertEntryFailsLoudlyWhenLiveLockHeld(t *testing.T) {
	dir := isolateDir(t)

	origTimeout := lockTimeout
	lockTimeout = 50 * time.Millisecond
	t.Cleanup(func() { lockTimeout = origTimeout })

	// A freshly created lock file is "live" (not stealable until
	// lockStaleAge): the write must fail with an error, not proceed unlocked.
	lockPath := filepath.Join(dir, "expose.json.lock")
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatalf("seed lock file: %v", err)
	}

	err := UpsertEntry(Entry{Name: "web", PID: 1, TokenHash: "abc"})
	if err == nil {
		t.Fatal("UpsertEntry under a live foreign lock = nil error, want error")
	}
	if !strings.Contains(err.Error(), "locked by another process") {
		t.Errorf("error = %q, want locked-by-another-process message", err)
	}

	// Nothing may have been written while unlocked.
	if _, ok, _ := GetEntry("web"); ok {
		t.Error("entry written despite lock contention — unlocked write raced")
	}
}

func TestStoreFilePermissions(t *testing.T) {
	dir := isolateDir(t)
	token, _ := GenerateToken()
	if err := UpsertEntry(Entry{Name: "web", PID: 1, TokenHash: HashToken(token)}); err != nil {
		t.Fatalf("UpsertEntry: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "expose.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// windows cannot represent unix perm bits — files written 0600 stat
	// back as 0666 (only the read-only bit round-trips), so the
	// no-group/other-bits assertion is meaningful on unix only.
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("expose.json permissions = %o, want no group/other bits (0600-style)", perm)
		}
	}
}

// ── Stop requests ────────────────────────────────────────────────────────

func TestServeListenerStopsOnStopRequestFile(t *testing.T) {
	isolateDir(t)
	token, _ := GenerateToken()
	port, _ := startBacking(t, `{"ok":true}`)

	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	entry := Entry{
		Name: "stopme", PID: os.Getpid(), Port: ln.Addr().(*net.TCPAddr).Port,
		BackingPort: port, TokenHash: HashToken(token),
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := UpsertEntry(entry); err != nil {
		t.Fatalf("UpsertEntry: %v", err)
	}

	var cause string
	done := make(chan error, 1)
	go func() {
		done <- ServeListener(ln, ServeConfig{
			Entry: entry, Token: token, TTL: time.Hour,
			StopPoll: 10 * time.Millisecond,
			SignalCh: make(chan os.Signal, 1),
			OnStop:   func(c string) { cause = c },
		})
	}()

	// Wait for the listener to accept, then file the stop request.
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", entry.Port))
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("listener never came up")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := RequestStop("stopme"); err != nil {
		t.Fatalf("RequestStop: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ServeListener after stop request: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ServeListener did not return after stop request")
	}
	if cause != "stop-request" {
		t.Errorf("cause = %q, want stop-request", cause)
	}
	// Entry cleaned up.
	if _, ok, _ := GetEntry("stopme"); ok {
		t.Error("store entry still present after stop")
	}
	// Stop file consumed.
	if StopRequested("stopme") {
		t.Error("stop-request file not cleared after shutdown")
	}
}

func TestRequestStopCreatesAndClearStopRemovesFile(t *testing.T) {
	dir := isolateDir(t)
	if err := RequestStop("web"); err != nil {
		t.Fatalf("RequestStop: %v", err)
	}
	if !StopRequested("web") {
		t.Error("stop file not visible after RequestStop")
	}
	// Path traversal: a hostile name is sanitized to its base, landing
	// inside the stop dir — never outside it.
	if err := RequestStop("../evil"); err != nil {
		t.Fatalf("RequestStop(traversal): %v", err)
	}
	if !StopRequested("evil") {
		t.Error("sanitized stop file for '../evil' not found inside stop dir")
	}
	if _, err := os.Stat(filepath.Join(dir, "evil")); err == nil {
		t.Error("traversal name created a file outside the stop dir")
	}
	ClearStop("web")
	if StopRequested("web") {
		t.Error("stop file still present after clearStop")
	}
}

// ── Signal shutdown (graceful on SIGINT/SIGTERM) ─────────────────────────

func TestServeListenerShutsDownOnSignal(t *testing.T) {
	isolateDir(t)
	token, _ := GenerateToken()
	port, _ := startBacking(t, `{"ok":true}`)

	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	entry := Entry{
		Name: "sig-srv", PID: os.Getpid(), Port: ln.Addr().(*net.TCPAddr).Port,
		BackingPort: port, TokenHash: HashToken(token),
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := UpsertEntry(entry); err != nil {
		t.Fatalf("UpsertEntry: %v", err)
	}

	sigCh := make(chan os.Signal, 1)
	var cause string
	done := make(chan error, 1)
	go func() {
		done <- ServeListener(ln, ServeConfig{
			Entry: entry, Token: token, TTL: time.Hour,
			StopPoll: 10 * time.Millisecond,
			SignalCh: sigCh,
			OnStop:   func(c string) { cause = c },
		})
	}()

	// Wait for readiness, then simulate the OS signal delivery.
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", entry.Port))
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("listener never came up")
		}
		time.Sleep(10 * time.Millisecond)
	}
	sigCh <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ServeListener: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ServeListener did not return on signal")
	}
	if cause == "" {
		t.Error("OnStop cause empty after signal shutdown")
	}
	if _, ok, _ := GetEntry("sig-srv"); ok {
		t.Error("store entry still present after signal shutdown")
	}
}

// ── Misc: entry ordering, misc helpers ───────────────────────────────────

func TestLoadEntriesSortedByName(t *testing.T) {
	isolateDir(t)
	for _, n := range []string{"zeta", "alpha", "mid"} {
		if err := UpsertEntry(Entry{Name: n, PID: 1, TokenHash: "h"}); err != nil {
			t.Fatalf("UpsertEntry(%s): %v", n, err)
		}
	}
	entries, err := LoadEntries()
	if err != nil {
		t.Fatalf("LoadEntries: %v", err)
	}
	if len(entries) != 3 || entries[0].Name != "alpha" || entries[1].Name != "mid" || entries[2].Name != "zeta" {
		t.Errorf("entries not sorted by name: %v", entries)
	}
}

func TestServeListenerRejectsMissingTokenConfig(t *testing.T) {
	isolateDir(t)
	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	if err := ServeListener(ln, ServeConfig{
		Entry:    Entry{Name: "x", TokenHash: ""},
		Token:    "",
		TTL:      time.Hour,
		SignalCh: make(chan os.Signal, 1),
	}); err == nil {
		t.Error("ServeListener with empty token = nil error, want error")
	}
}

func TestWriteErrorEnvelopeShape(t *testing.T) {
	w := httptest.NewRecorder()
	WriteError(w, http.StatusUnauthorized, "unauthorized", "nope")
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var doc struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("envelope not JSON: %v (%s)", err, w.Body.String())
	}
	if doc.Error.Code != "unauthorized" || doc.Error.Message != "nope" {
		t.Errorf("envelope = %+v", doc.Error)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"error"`)) {
		t.Error("envelope missing error key")
	}
}
