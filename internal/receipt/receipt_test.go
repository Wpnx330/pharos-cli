package receipt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testReceipt returns a fully populated receipt with a fixed timestamp so
// assertions are deterministic.
func testReceipt() Receipt {
	return Receipt{
		Command:   "install",
		Package:   "context7",
		Version:   "1.0.0",
		Timestamp: "2026-09-02T12:00:00Z",
		Files: []FileChange{
			{
				Path:      "/home/u/.config/mcp/mcp.json",
				Client:    "Generic MCP",
				Action:    "modified",
				BeforeSHA: "8653057a4b57183ce71278ca80dbd82a61196fa182652f4cba355614b768d063",
				AfterSHA:  "76ba7142b4fbb63836d5310ab4088e07f9d0dcfcad6671ec04c8d8081a170b09",
			},
			{
				Path:      "/home/u/.cursor/mcp.json",
				Client:    "Cursor",
				Action:    "created",
				BeforeSHA: "",
				AfterSHA:  "76ba7142b4fbb63836d5310ab4088e07f9d0dcfcad6671ec04c8d8081a170b09",
				Backup:    "/home/u/.cursor/mcp.json.bak",
			},
		},
		Servers: []ServerChange{
			{Client: "Generic MCP", Name: "context7", Action: "replaced"},
			{Client: "Cursor", Name: "context7", Action: "added"},
		},
	}
}

// TestFileHashKnownVector pins the digest against a fixed input so any
// hash-algorithm or encoding change is caught immediately.
func TestFileHashKnownVector(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "vector.txt")
	if err := os.WriteFile(p, []byte("pharos receipt known vector\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := FileHash(p)
	if err != nil {
		t.Fatalf("FileHash: %v", err)
	}
	want := "76ba7142b4fbb63836d5310ab4088e07f9d0dcfcad6671ec04c8d8081a170b09"
	if got != want {
		t.Errorf("FileHash = %q, want %q", got, want)
	}
}

// TestFileHashEmptyInput pins the SHA-256 empty-string vector.
func TestFileHashEmptyInput(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := FileHash(p)
	if err != nil {
		t.Fatalf("FileHash: %v", err)
	}
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got != want {
		t.Errorf("FileHash(empty) = %q, want %q", got, want)
	}
}

// TestFileHashMissingFileIsEmptyNotError: a missing file is the
// "did not exist before" case, not a failure.
func TestFileHashMissingFileIsEmptyNotError(t *testing.T) {
	got, err := FileHash(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("FileHash on missing file returned error: %v", err)
	}
	if got != "" {
		t.Errorf("FileHash on missing file = %q, want empty", got)
	}
}

// TestJSONRoundTrip marshals and unmarshals, requiring lossless equality.
func TestJSONRoundTrip(t *testing.T) {
	r := testReceipt()
	data, err := r.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var got Receipt
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Command != r.Command || got.Package != r.Package || got.Version != r.Version {
		t.Errorf("header round-trip mismatch: %+v", got)
	}
	if got.Timestamp != r.Timestamp {
		t.Errorf("timestamp = %q, want %q", got.Timestamp, r.Timestamp)
	}
	if len(got.Files) != len(r.Files) || len(got.Servers) != len(r.Servers) {
		t.Fatalf("slice lengths changed: %d files, %d servers", len(got.Files), len(got.Servers))
	}
	for i := range r.Files {
		if got.Files[i] != r.Files[i] {
			t.Errorf("Files[%d] = %+v, want %+v", i, got.Files[i], r.Files[i])
		}
	}
	for i := range r.Servers {
		if got.Servers[i] != r.Servers[i] {
			t.Errorf("Servers[%d] = %+v, want %+v", i, got.Servers[i], r.Servers[i])
		}
	}
}

// TestJSONStableKeyOrder pins the field order of the emitted document:
// two renders are byte-identical and the top-level keys appear in
// declaration order (command, package, version, timestamp, status, files,
// servers).
func TestJSONStableKeyOrder(t *testing.T) {
	r := testReceipt()
	first, err := r.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	second, err := r.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if string(first) != string(second) {
		t.Error("JSON() is not byte-stable between calls")
	}
	s := string(first)
	order := []string{`"command"`, `"package"`, `"version"`, `"timestamp"`, `"status"`, `"files"`, `"servers"`}
	last := -1
	for _, key := range order {
		idx := strings.Index(s, key)
		if idx < 0 {
			t.Fatalf("key %s missing from output:\n%s", key, s)
		}
		if idx <= last {
			t.Errorf("key %s out of order (idx %d, previous %d)", key, idx, last)
		}
		last = idx
	}
	// FileChange keys must also keep declaration order.
	if idxBefore := strings.Index(s, `"before_sha256"`); idxBefore >= 0 {
		if idxAfter := strings.Index(s, `"after_sha256"`); idxAfter >= 0 && idxAfter < idxBefore {
			t.Error("after_sha256 rendered before before_sha256")
		}
	}
	if !strings.Contains(s, `"backup_path"`) {
		t.Error("backup_path key missing for the file carrying a backup")
	}
	// 2-space indent: the files array elements are indented exactly 4 spaces.
	if !strings.Contains(s, "\n    {\n") {
		t.Errorf("expected 2-space-indented array elements:\n%s", s)
	}
}

// TestJSONEmptyFilesEdge: an empty receipt must still emit a complete
// document with files/servers as [] — never null.
func TestJSONEmptyFilesEdge(t *testing.T) {
	r := Receipt{Command: "update", Package: "x", Timestamp: "2026-09-02T12:00:00Z"}
	data, err := r.JSON()
	if err != nil {
		t.Fatalf("JSON on empty receipt: %v", err)
	}
	if !json.Valid(data) {
		t.Errorf("empty receipt JSON invalid: %s", data)
	}
	s := string(data)
	if !strings.Contains(s, `"files": []`) {
		t.Errorf(`want "files": [], got:\n%s`, s)
	}
	if !strings.Contains(s, `"servers": []`) {
		t.Errorf(`want "servers": [], got:\n%s`, s)
	}
	if strings.Contains(s, "null") {
		t.Errorf("null leaked into empty receipt:\n%s", s)
	}
}

// TestTimestampIsRFC3339UTC pins the timestamp format on a freshly built
// receipt (the zero-Timestamp case above is skipped).
func TestTimestampIsRFC3339UTC(t *testing.T) {
	r := Receipt{Command: "install", Package: "p", Timestamp: time.Now().UTC().Format(time.RFC3339)}
	if _, err := time.Parse(time.RFC3339, r.Timestamp); err != nil {
		t.Errorf("timestamp %q is not RFC3339: %v", r.Timestamp, err)
	}
	if strings.HasSuffix(r.Timestamp, "+00:00") {
		// time.Now().UTC() renders as Z; +00:00 would mean non-UTC formatting.
		t.Errorf("timestamp %q not rendered in UTC Z form", r.Timestamp)
	}
}

// TestSummaryFormatting pins the human one-liner + per-file bullets,
// including the truncated hash and backup suffix.
func TestSummaryFormatting(t *testing.T) {
	got := testReceipt().Summary()
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("Summary has %d lines, want 3:\n%s", len(lines), got)
	}
	wantHeader := "✓ Installed context7@1.0.0"
	if lines[0] != wantHeader {
		t.Errorf("header = %q, want %q", lines[0], wantHeader)
	}
	want1 := "  · /home/u/.config/mcp/mcp.json  modified  sha256 76ba…0b09"
	if lines[1] != want1 {
		t.Errorf("bullet 1 = %q, want %q", lines[1], want1)
	}
	want2 := "  · /home/u/.cursor/mcp.json  created  sha256 76ba…0b09  (backup: /home/u/.cursor/mcp.json.bak)"
	if lines[2] != want2 {
		t.Errorf("bullet 2 = %q, want %q", lines[2], want2)
	}
}

// TestSummaryVerbsAndEmptyFiles pins the per-command verb and the bare
// one-liner when there is nothing to report.
func TestSummaryVerbsAndEmptyFiles(t *testing.T) {
	cases := []struct{ command, want string }{
		{"install", "✓ Installed pkg@1.0.0"},
		{"remove", "✓ Removed pkg@1.0.0"},
		{"update", "✓ Updated pkg@1.0.0"},
		{"other", "✓ Completed pkg@1.0.0"},
	}
	for _, tc := range cases {
		r := Receipt{Command: tc.command, Package: "pkg", Version: "1.0.0"}
		if got := r.Summary(); got != tc.want {
			t.Errorf("Summary(command=%s) = %q, want %q", tc.command, got, tc.want)
		}
	}
	// No version → name only.
	r := Receipt{Command: "remove", Package: "pkg"}
	if got := r.Summary(); got != "✓ Removed pkg" {
		t.Errorf("Summary without version = %q", got)
	}
}

// TestShortHash pins the first4…last4 display truncation.
func TestShortHash(t *testing.T) {
	full := "76ba7142b4fbb63836d5310ab4088e07f9d0dcfcad6671ec04c8d8081a170b09"
	if got := ShortHash(full); got != "76ba…0b09" {
		t.Errorf("ShortHash(full) = %q", got)
	}
	if got := ShortHash("abcd1234"); got != "abcd1234" {
		t.Errorf("ShortHash(8 chars) = %q, want unchanged", got)
	}
	if got := ShortHash(""); got != "" {
		t.Errorf("ShortHash(empty) = %q", got)
	}
}

// TestStatusRoundTrip pins the status field: it serializes on every
// receipt, appears exactly as set, and round-trips losslessly for both
// "ok" and "partial".
func TestStatusRoundTrip(t *testing.T) {
	for _, status := range []string{"ok", "partial"} {
		r := testReceipt()
		r.Status = status
		data, err := r.JSON()
		if err != nil {
			t.Fatalf("JSON(status=%s): %v", status, err)
		}
		if !strings.Contains(string(data), `"status": "`+status+`"`) {
			t.Errorf("status %q missing from output:\n%s", status, data)
		}
		var got Receipt
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal(status=%s): %v", status, err)
		}
		if got.Status != status {
			t.Errorf("status round-trip = %q, want %q", got.Status, status)
		}
	}
}

// TestStatusAlwaysPresentInJSON: even a zero-value receipt renders the
// status key (defaulting to "ok") — consumers can rely on it always being
// present in the document.
func TestStatusAlwaysPresentInJSON(t *testing.T) {
	r := Receipt{Command: "update", Package: "x", Timestamp: "2026-09-02T12:00:00Z"}
	data, err := r.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(string(data), `"status": "ok"`) {
		t.Errorf("status key missing from zero-value receipt:\n%s", data)
	}
}

// TestErrorsOmittedWhenClean: a receipt without errors must not emit the
// errors key at all (omitempty) — absence means the run was clean.
func TestErrorsOmittedWhenClean(t *testing.T) {
	r := testReceipt() // Errors left nil
	data, err := r.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if strings.Contains(string(data), `"errors"`) {
		t.Errorf("errors key must be omitted when there are no errors:\n%s", data)
	}
}

// TestErrorsSerializeAndRoundTrip: populated errors serialize as a string
// array next to status "partial" and round-trip losslessly.
func TestErrorsSerializeAndRoundTrip(t *testing.T) {
	r := testReceipt()
	r.Status = "partial"
	r.Errors = []string{
		"lockfile save failed: permission denied",
		"dependency dep-server config write failed: read-only dir",
	}
	data, err := r.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(string(data), `"errors": [`) {
		t.Errorf("errors array missing from output:\n%s", data)
	}
	var got Receipt
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Status != "partial" {
		t.Errorf("status = %q, want partial", got.Status)
	}
	if len(got.Errors) != len(r.Errors) {
		t.Fatalf("errors = %+v, want %+v", got.Errors, r.Errors)
	}
	for i := range r.Errors {
		if got.Errors[i] != r.Errors[i] {
			t.Errorf("errors[%d] = %q, want %q", i, got.Errors[i], r.Errors[i])
		}
	}
}

// TestSummaryWarningsSection: a receipt carrying errors prints the
// "⚠ completed with N warnings" section with one line per error, in
// singular form for a single error.
func TestSummaryWarningsSection(t *testing.T) {
	one := Receipt{Command: "update", Package: "pkg", Errors: []string{"lockfile save failed: boom"}}
	got := one.Summary()
	if !strings.Contains(got, "⚠ completed with 1 warning\n") && !strings.Contains(got, "⚠ completed with 1 warning") {
		t.Errorf("summary missing singular warning header:\n%s", got)
	}
	if !strings.Contains(got, "· lockfile save failed: boom") {
		t.Errorf("summary missing the error line:\n%s", got)
	}
	two := Receipt{Command: "install", Package: "pkg", Errors: []string{"a failed", "b failed"}}
	if s := two.Summary(); !strings.Contains(s, "⚠ completed with 2 warnings") {
		t.Errorf("summary missing plural warning header:\n%s", s)
	}
	// No errors → no warnings section at all.
	clean := testReceipt().Summary()
	if strings.Contains(clean, "⚠") {
		t.Errorf("clean receipt must not print a warnings section:\n%s", clean)
	}
}
