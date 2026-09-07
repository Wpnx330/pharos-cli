// W5.1 A6 — `pharos pin` / `pharos unpin`.
//
// Pinning version-locks one server: `pharos update`'s apply path skips
// pinned servers (reported as "pinned"), while --check/--dry-run still
// show what is available. The pin lives in the lockfile entry's
// additive PinnedAt field, so pinning is per-project state, fully
// visible in pharos.lock, and removable with `pharos unpin`.
//
// Pinning an EXACT version must land that version through the normal
// registry path — pin delegates to the same machinery as
// `pharos install <name>@<version>` (semver.Resolve for the target,
// then runInstall); it never invents a side channel. When the resolved
// target equals the installed version there is nothing to install and
// only the pin is recorded.
//
// JSON contract: pin/unpin have no --json flag. Under PHAROS_JSON=1 the
// delegated install emits its receipt JSON as the single stdout
// document; pin's own confirmation lines go to stderr (progressf).
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/lockfile"
	"github.com/Wpnx330/pharos-cli/internal/semver"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

var pinCmd = &cobra.Command{
	Use:   "pin <name> [<version>]",
	Short: "Pin a server at its current (or given) version — update skips it",
	Long: ui.Label.Render("pharos pin") + ` locks a server at a version in pharos.lock.

With no version, pins at the currently installed version. With a
version (exact or range, same syntax as install), the target version is
installed through the normal registry path first, then pinned.

Pinned servers are skipped by 'pharos update' (reported as "pinned")
but still shown by 'pharos update --check' with the available version.
Reinstalling a pinned server at a new version moves the pin to that
version; 'pharos unpin <name>' removes it.

Examples:
  pharos pin echo-server          # pin at the installed version
  pharos pin echo-server 1.2.3    # install 1.2.3, then pin
  pharos pin echo-server ^1.0.0   # resolve the range, install, pin
  pharos unpin echo-server        # let update manage it again`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runPin,
}

var unpinCmd = &cobra.Command{
	Use:   "unpin <name>",
	Short: "Remove a version pin so update manages the server again",
	Args:  cobra.ExactArgs(1),
	RunE:  runUnpin,
}

func init() {
	rootCmd.AddCommand(pinCmd)
	rootCmd.AddCommand(unpinCmd)
}

func runPin(cmd *cobra.Command, args []string) error {
	name := args[0]

	lockPath, err := lockfile.DefaultPath()
	if err != nil {
		return fmt.Errorf("cannot determine lockfile path: %w", err)
	}
	lf, err := lockfile.Load(lockPath)
	if err != nil {
		return fmt.Errorf("load lockfile: %w", err)
	}
	entry, ok := lf.Get(name)
	if !ok {
		return fmt.Errorf("%s is not in pharos.lock — install it first with 'pharos install %s'", name, name)
	}

	if len(args) == 2 {
		target, err := resolvePinTarget(name, args[1])
		if err != nil {
			return err
		}
		if target != entry.Version {
			// Land the target version through the normal registry install
			// path (same machinery as `pharos install name@version`), then
			// verify the lockfile actually moved before recording the pin.
			progressf("%s  %s@%s\n", ui.Label.Render("Installing pinned version..."), name, target)
			runInstall(cmd, []string{name + "@" + target})

			lf, err = lockfile.Load(lockPath)
			if err != nil {
				return fmt.Errorf("reload lockfile: %w", err)
			}
			entry, ok = lf.Get(name)
			if !ok || entry.Version != target {
				return fmt.Errorf("pin failed: %s is at %s, not the requested %s — the install did not complete", name, pinVersionOf(entry, ok), target)
			}
		}
	}

	if entry.Version == "" {
		return fmt.Errorf("cannot pin %s: no installed version recorded (server was adopted unresolved) — pin an explicit version instead", name)
	}

	pinned := entry.Version
	entry.PinnedAt = &pinned
	lf.Set(name, entry)
	if err := lf.Save(lockPath); err != nil {
		return fmt.Errorf("save lockfile: %w", err)
	}

	progressf("%s  Pinned %s@%s — 'pharos update' will skip it\n", ui.Success.Render("✓"), name, pinned)
	progressf("%s  %s\n", ui.Muted.Render("To unpin:"), fmt.Sprintf("pharos unpin %s", name))
	return nil
}

// resolvePinTarget resolves a pin version spec through the registry the
// same way install does (GetPackage + semver.Resolve), so the version
// pin records is exactly the version the normal registry path would
// install — exact or range alike.
func resolvePinTarget(name, spec string) (string, error) {
	_, client := loadConfig()
	pkg, err := client.GetPackage(name)
	if err != nil {
		return "", fmt.Errorf("fetch package %s: %w", name, err)
	}
	resolved, err := semver.Resolve(spec, pkg.VersionStrings(), pkg.DistTags)
	if err != nil {
		return "", fmt.Errorf("resolve version %q for %s: %w", spec, name, err)
	}
	return resolved, nil
}

// pinVersionOf safely renders the entry's version for the pin-failure
// message (the entry may be absent after a failed install).
func pinVersionOf(entry lockfile.ServerEntry, ok bool) string {
	if !ok {
		return "<missing>"
	}
	if entry.Version == "" {
		return "<none>"
	}
	return entry.Version
}

func runUnpin(cmd *cobra.Command, args []string) error {
	name := args[0]

	lockPath, err := lockfile.DefaultPath()
	if err != nil {
		return fmt.Errorf("resolve lockfile path: %w", err)
	}
	lf, err := lockfile.Load(lockPath)
	if err != nil {
		return fmt.Errorf("load lockfile: %w", err)
	}
	entry, ok := lf.Get(name)
	if !ok {
		return fmt.Errorf("%s is not in pharos.lock", name)
	}
	if entry.PinnedAt == nil {
		return fmt.Errorf("%s is not pinned", name)
	}

	entry.PinnedAt = nil
	lf.Set(name, entry)
	if err := lf.Save(lockPath); err != nil {
		return fmt.Errorf("save lockfile: %w", err)
	}

	progressf("%s  Unpinned %s — 'pharos update' manages it again\n", ui.Success.Render("✓"), name)
	return nil
}
