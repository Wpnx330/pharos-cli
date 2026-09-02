package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

///client config rewrite for `pharos update` (resolves issue #20).
//
// After an update lands, every client config that references the updated
// package must be re-pointed at the new binary/args — exactly what
// `pharos install` does via clientconfig.MergeServer. These helpers reuse
// that same write path; they never rewrite config files wholesale and
// never touch non-MCP keys.

// configReferencesServer reports whether the config at path (parsed as
// format) already contains an entry for the given package. Missing or
// unparsable files are simply "false" — callers decide whether that is
// actionable.
func configReferencesServer(path, format, pkgID string) bool {
	servers, err := clientconfig.ReadServersFormat(path, format)
	if err != nil {
		return false
	}
	_, ok := servers[pkgID]
	return ok
}

// backupConfigFile copies path to path+".bak" (a single generation: an
// existing .bak is overwritten, never suffixed). Best-effort semantics:
// the caller reports errors, but a backup failure is not fatal.
func backupConfigFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := os.WriteFile(path+".bak", data, 0o644); err != nil {
		return fmt.Errorf("write %s.bak: %w", path, err)
	}
	return nil
}

// rewriteClientsForUpdate rewrites every client config in clients that
// already references pkgID, so it points at the newly installed binary or
// endpoint (serverCfg). Exactly one .bak generation is left behind per
// rewritten file. Per-client errors are collected, not fatal: one bad
// config file must not abort the others. Returns the paths of rewritten
// configs and any per-client errors.
//
// When b is non-nil (W1.2), each successful rewrite is recorded on the
// receipt as a FileChange (with backup_path when .bak was taken) and a
// ServerChange("replaced").
func rewriteClientsForUpdate(pkgID string, serverCfg clientconfig.ServerConfig, clients []clientconfig.Client, b *receiptBuilder) ([]string, []error) {
	var updated []string
	var errs []error
	seen := make(map[string]bool) // by ID: one path per client

	for _, c := range clients {
		if c.ID != "" && seen[string(c.ID)] {
			continue
		}
		format := c.Format
		if format == "" {
			format = clientconfig.FormatMcpServers
		}
		servers, perr := clientconfig.ReadServersFormat(c.Path, format)
		if c.Existing && perr != nil {
			errs = append(errs, fmt.Errorf("%s: unparsable config: %w", c.Name, perr))
			continue // continue with the other clients
		}
		if _, ok := servers[pkgID]; !ok {
			continue // bystander: references another server or none
		}

		b.snapshotPath(c.Path)
		backupPath := ""
		if c.Existing {
			if b.backupTaken(c.Path) {
				// This run already settled the .bak generation for this
				// file (an earlier server's rewrite took it). Re-backing
				// up would capture the INTERMEDIATE content written by
				// that rewrite, while the receipt's before_sha256 claims
				// the pre-run generation — so keep the existing .bak.
				if b.backedUp[c.Path] {
					backupPath = c.Path + ".bak"
				}
			} else if err := backupConfigFile(c.Path); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", c.Name, err))
				continue
			} else {
				if b != nil {
					b.backedUp[c.Path] = true
				}
				backupPath = c.Path + ".bak"
			}
		}

		if err := clientconfig.MergeServer(c, pkgID, serverCfg); err != nil {
			var skip *clientconfig.SkipError
			_ = skip
			errs = append(errs, err)
			continue
		}

		if c.ID != "" {
			seen[string(c.ID)] = true
		}
		updated = append(updated, c.Path)
		b.touch(c.Path, c.Name, backupPath)
		b.server(c.Name, pkgID, "replaced")
	}
	return updated, errs
}

// printUpdateConfigResults mirrors install's per-client result reporting.
// Output routing (stdout vs stderr under JSON mode) lives in progressf so
// the function is safe for any caller.
func printUpdateConfigResults(updated []string, errs []error) {
	for _, p := range updated {
		progressf("  %s  %s\n", ui.Success.Render("✓"), p)
	}
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "  %s  %v\n", ui.Error.Render("✗"), e)
	}
}

// JSON-encode helper used in dry-run summaries (kept here so the map
// ordering stays stable in one place).
func mustMarshalIndent(v any) []byte {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return []byte{}
	}
	return data
}
