// Package receipt builds deterministic, machine-checkable receipts of
// the side effects of the mutating commands (install / remove / update):
// which config files changed — with before/after SHA-256 — and which MCP
// servers were added, replaced, or removed, per client.
//
// The same Receipt renders as stable JSON (under PHAROS_JSON=1 / --json)
// or as a short human summary. Stdlib only; hashing and capture happen at
// the command call sites, never inside the atomic write itself.
package receipt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// FileChange records one file touched by the command.
type FileChange struct {
	Path      string `json:"path"`
	Client    string `json:"client"`
	Action    string `json:"action"`        // "created" | "modified" | "unchanged"
	BeforeSHA string `json:"before_sha256"` // "" if the file did not exist
	AfterSHA  string `json:"after_sha256"`
	Backup    string `json:"backup_path,omitempty"` // .bak path when one was taken
}

// ServerChange records one MCP server entry mutation in one client.
type ServerChange struct {
	Client string `json:"client"`
	Name   string `json:"name"`
	Action string `json:"action"` // "added" | "replaced" | "removed"
}

// Receipt is the deterministic record of what a mutation command changed.
// Status is "ok" or "partial"; "partial" means one or more non-fatal
// failures were recorded in Errors while the itemized side effects below
// still happened as listed.
type Receipt struct {
	Command   string         `json:"command"` // "install" | "remove" | "update"
	Package   string         `json:"package"`
	Version   string         `json:"version,omitempty"`
	Timestamp string         `json:"timestamp"` // RFC3339 UTC
	Status    string         `json:"status"`    // "ok" | "partial"
	Files     []FileChange   `json:"files"`
	Servers   []ServerChange `json:"servers"`
	Errors    []string       `json:"errors,omitempty"` // non-fatal failures; non-empty ⇒ status "partial"
}

// JSON renders the receipt as stable JSON: fixed field order (struct
// declaration order), 2-space indent, and empty arrays instead of null
// so the document is always a complete, parseable receipt. Status is
// always present: an unset status renders as "ok".
func (r Receipt) JSON() ([]byte, error) {
	if r.Status == "" {
		r.Status = "ok"
	}
	if r.Files == nil {
		r.Files = []FileChange{}
	}
	if r.Servers == nil {
		r.Servers = []ServerChange{}
	}
	return json.MarshalIndent(r, "", "  ")
}

// Summary renders the human form: a one-liner plus one bullet per file.
func (r Receipt) Summary() string {
	var b strings.Builder
	b.WriteString("✓ ")
	b.WriteString(summaryVerb(r.Command))
	b.WriteString(" ")
	b.WriteString(r.Package)
	if r.Version != "" {
		b.WriteString("@")
		b.WriteString(r.Version)
	}
	for _, f := range r.Files {
		b.WriteString("\n  · ")
		b.WriteString(f.Path)
		b.WriteString("  ")
		b.WriteString(f.Action)
		if f.AfterSHA != "" {
			b.WriteString("  sha256 ")
			b.WriteString(ShortHash(f.AfterSHA))
		}
		if f.Backup != "" {
			b.WriteString("  (backup: ")
			b.WriteString(f.Backup)
			b.WriteString(")")
		}
	}
	if len(r.Errors) > 0 {
		unit := "warnings"
		if len(r.Errors) == 1 {
			unit = "warning"
		}
		fmt.Fprintf(&b, "\n⚠ completed with %d %s", len(r.Errors), unit)
		for _, e := range r.Errors {
			b.WriteString("\n    · ")
			b.WriteString(e)
		}
	}
	return b.String()
}

func summaryVerb(command string) string {
	switch command {
	case "install":
		return "Installed"
	case "remove":
		return "Removed"
	case "update":
		return "Updated"
	default:
		return "Completed"
	}
}

// ShortHash truncates a hex digest to first4…last4 for display.
func ShortHash(hash string) string {
	if len(hash) <= 8 {
		return hash
	}
	return hash[:4] + "…" + hash[len(hash)-4:]
}

// FileHash returns the hex SHA-256 of the file at path. A missing file is
// not an error: it returns "" and nil, which receipts represent as "the
// file did not exist before". Callers hash before issuing a write and
// again after it — beside the atomic write, never inside it.
func FileHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
