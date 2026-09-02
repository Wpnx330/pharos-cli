package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/receipt"
)

// ── W1.2 deterministic install receipts ─────────────────────────────────────
//
// Every mutation command (install / remove / update) records what it changed
// into a receipt.Receipt: per-file before/after SHA-256 and per-client server
// entry changes. Under PHAROS_JSON=1 (or --json) the receipt JSON is the ONLY
// stdout output; in the default human mode the receipt summary prints after
// the command's usual progress lines.
//
// Hashes are captured at the call sites — before issuing a write and again
// after — never inside the atomic write itself, so SafeWriteConfig and the
// clientconfig APIs keep their signatures.

// progressf prints human progress to stdout, or to stderr when JSON output
// was requested, keeping stdout a single pure JSON document (the receipt).
func progressf(format string, a ...any) {
	if JSONRequested() {
		fmt.Fprintf(os.Stderr, format, a...)
		return
	}
	fmt.Printf(format, a...)
}

// fileTouch is one file the command wrote. The after-hash is computed once
// in finish(), so later writes to the same file during the run (e.g. a
// dependency install after the primary package) are reflected truthfully in
// the final receipt instead of capturing an intermediate state.
type fileTouch struct {
	path   string
	client string
	backup string
}

// receiptBuilder accumulates a receipt while a mutation command runs.
// All methods are nil-safe so tests and optional paths can pass a nil
// builder to mean "record nothing".
type receiptBuilder struct {
	rec       receipt.Receipt
	before    map[string]string // path → sha256 before the run ("" = absent)
	hasServer map[string]bool   // path NUL server → present at snapshot time
	backedUp  map[string]bool   // path → a pre-run .bak generation was taken this run
	touched   []fileTouch
	seenPath  map[string]bool
	lockPath  string
	canonPath string
}

// serverStateKey keys the pre-write server-presence map by path and
// server name, so dependency installs (which resolve after the primary)
// can snapshot their own name without clobbering the primary's state.
func serverStateKey(path, server string) string {
	return path + "\x00" + server
}

// newReceiptBuilder starts a receipt for one command run. The timestamp is
// stamped in UTC RFC3339 at creation.
func newReceiptBuilder(command, pkg, version string) *receiptBuilder {
	return &receiptBuilder{
		rec: receipt.Receipt{
			Command:   command,
			Package:   pkg,
			Version:   version,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Status:    "ok",
			Files:     []receipt.FileChange{},
			Servers:   []receipt.ServerChange{},
		},
		before:    map[string]string{},
		hasServer: map[string]bool{},
		backedUp:  map[string]bool{},
		seenPath:  map[string]bool{},
	}
}

// setPackage sets the package identity late, for commands that only know it
// after the mutation loop (update with multiple servers joins their names).
func (b *receiptBuilder) setPackage(pkg string) {
	if b == nil {
		return
	}
	b.rec.Package = pkg
}

// setVersion records the resolved version, ignoring empty values so
// best-effort lookups never blank out a known version.
func (b *receiptBuilder) setVersion(v string) {
	if b == nil || v == "" {
		return
	}
	b.rec.Version = v
}

// snapshotPath records the pre-run hash of one file. The first snapshot of a
// path wins so multi-write flows keep the original before-state.
func (b *receiptBuilder) snapshotPath(path string) {
	if b == nil || path == "" {
		return
	}
	if _, ok := b.before[path]; ok {
		return
	}
	hash, _ := receipt.FileHash(path)
	b.before[path] = hash
}

// snapshotClient records the pre-run hash of a client config plus whether
// it already references serverName, which later decides "added" vs
// "replaced".
func (b *receiptBuilder) snapshotClient(c clientconfig.Client, serverName string) {
	if b == nil {
		return
	}
	b.snapshotPath(c.Path)
	key := serverStateKey(c.Path, serverName)
	if _, ok := b.hasServer[key]; ok {
		return
	}
	servers, err := clientconfig.ReadServersFormat(c.Path, c.Format)
	_, has := servers[serverName] // nil map reads are fine
	b.hasServer[key] = err == nil && has
}

// serverWasPresent reports whether the named server existed in the client
// config when it was last snapshotted — the pre-write state that decides
// "added" vs "replaced".
func (b *receiptBuilder) serverWasPresent(path, serverName string) bool {
	if b == nil {
		return false
	}
	return b.hasServer[serverStateKey(path, serverName)]
}

// snapshotAllClients records pre-run state for every known client path —
// detected plus built-in candidates — so install can diff whichever clients
// WriteClientConfigs ends up touching (auto mode or explicit --client).
func (b *receiptBuilder) snapshotAllClients(serverName string) {
	if b == nil {
		return
	}
	for _, c := range clientconfig.Detect() {
		b.snapshotClient(c, serverName)
	}
	for _, c := range clientconfig.CandidatePaths() {
		b.snapshotClient(c, serverName)
	}
}

// touch records a file the command wrote (dedup by path: the first touch's
// client label and backup win, matching the primary package's write).
func (b *receiptBuilder) touch(path, clientName, backup string) {
	if b == nil || path == "" {
		return
	}
	if b.seenPath[path] {
		return
	}
	b.seenPath[path] = true
	b.touched = append(b.touched, fileTouch{path: path, client: clientName, backup: backup})
}

// server records one server-entry change for one client.
func (b *receiptBuilder) server(clientName, name, action string) {
	if b == nil {
		return
	}
	b.rec.Servers = append(b.rec.Servers, receipt.ServerChange{
		Client: clientName,
		Name:   name,
		Action: action,
	})
}

// addError records a non-fatal failure. Any recorded error flips the
// receipt status to "partial" at finish() — the itemized side effects
// still happened as listed, but the run did not fully succeed.
func (b *receiptBuilder) addError(format string, a ...any) {
	if b == nil {
		return
	}
	b.rec.Errors = append(b.rec.Errors, fmt.Sprintf(format, a...))
}

// backupTaken reports whether path already has its pre-run generation
// settled this run — either an explicit .bak was taken, or the file was
// written (created) by this run. A later rewrite in the same run must not
// re-backup: the .bak would capture intermediate content instead of the
// pre-run generation the receipt's before_sha256 claims.
func (b *receiptBuilder) backupTaken(path string) bool {
	if b == nil {
		return false
	}
	return b.backedUp[path] || b.seenPath[path]
}

// noteLock records the lockfile path and its pre-run hash.
func (b *receiptBuilder) noteLock(path string) {
	if b == nil || path == "" {
		return
	}
	b.lockPath = path
	b.snapshotPath(path)
}

// touchLock adds the lockfile row; call after a successful save. The row is
// appended last so Files order mirrors the command's write order.
func (b *receiptBuilder) touchLock() {
	if b == nil {
		return
	}
	b.touch(b.lockPath, "lockfile", "")
}

// noteCanonical records the canonical config path and its pre-run hash
// (noteLock-style).
func (b *receiptBuilder) noteCanonical(path string) {
	if b == nil || path == "" {
		return
	}
	b.canonPath = path
	b.snapshotPath(path)
}

// touchCanonical adds the canonical config row; call after a successful
// canonical.AddServer / RemoveServer. Absence means the canonical file
// was not written (absence = untouched).
func (b *receiptBuilder) touchCanonical() {
	if b == nil {
		return
	}
	b.touch(b.canonPath, "canonical", "")
}

// finish computes after-hashes and assembles the final Receipt.
func (b *receiptBuilder) finish() *receipt.Receipt {
	if b == nil {
		return nil
	}
	for _, ft := range b.touched {
		after, _ := receipt.FileHash(ft.path)
		before := b.before[ft.path]
		action := "modified"
		switch {
		case before == "" && after != "":
			action = "created"
		case before == after:
			action = "unchanged"
		}
		b.rec.Files = append(b.rec.Files, receipt.FileChange{
			Path:      ft.path,
			Client:    ft.client,
			Action:    action,
			BeforeSHA: before,
			AfterSHA:  after,
			Backup:    ft.backup,
		})
	}
	b.rec.Status = "ok"
	if len(b.rec.Errors) > 0 {
		b.rec.Status = "partial"
	}
	return &b.rec
}

// emit prints the receipt: under PHAROS_JSON / --json the JSON document is
// the only stdout output; otherwise the human summary prints.
func (b *receiptBuilder) emit() {
	if b == nil {
		return
	}
	r := b.finish()
	if JSONRequested() {
		data, err := r.JSON()
		if err != nil {
			fmt.Fprintf(os.Stderr, "receipt marshal: %v\n", err)
			return
		}
		fmt.Println(string(data))
		return
	}
	fmt.Println(r.Summary())
}
