package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/lockfile"
)

// --- Test helpers ---

// setupStore creates a temporary store directory and lockfile for testing.
// It returns the store directory path, lockfile path, and a cleanup function.
func setupStore(t *testing.T) (storeDir, lockPath string) {
	t.Helper()
	tmp := t.TempDir()
	storeDir = filepath.Join(tmp, "store")
	lockPath = filepath.Join(tmp, "pharos.lock")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatalf("mkdir store: %v", err)
	}
	return storeDir, lockPath
}

// writeLockfile writes a lockfile with the given server entries.
func writeLockfile(t *testing.T, lockPath string, servers map[string]lockfile.ServerEntry) {
	t.Helper()
	lf := lockfile.New()
	for name, entry := range servers {
		lf.Set(name, entry)
	}
	if err := lf.Save(lockPath); err != nil {
		t.Fatalf("save lockfile: %v", err)
	}
}

// writeInstalledMeta writes a .pharos-installed.json file in the package's
// store directory, optionally including dependencies.
type depInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type installedMetaForTest struct {
	Name         string    `json:"name"`
	Version      string    `json:"version"`
	Transport    string    `json:"transport"`
	Installed    string    `json:"installed"`
	Location     string    `json:"location"`
	Integrity    string    `json:"integrity"`
	Dependencies []depInfo `json:"dependencies,omitempty"`
}

func writeInstalledMeta(t *testing.T, storeDir, name, version string, deps []depInfo) {
	t.Helper()
	pkgDir := filepath.Join(storeDir, name, version)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	meta := installedMetaForTest{
		Name:         name,
		Version:      version,
		Transport:    "stdio",
		Dependencies: deps,
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	path := filepath.Join(pkgDir, ".pharos-installed.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
}

// writeManifestToStore writes a pharos.json manifest in the package's store
// directory with the given dependencies.
func writeManifestToStore(t *testing.T, storeDir, name, version string, deps []depInfo) {
	t.Helper()
	pkgDir := filepath.Join(storeDir, name, version)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	type manifestForTest struct {
		Name         string    `json:"name"`
		Version      string    `json:"version"`
		Transport    string    `json:"transport"`
		Dependencies []depInfo `json:"dependencies,omitempty"`
	}
	m := manifestForTest{
		Name:         name,
		Version:      version,
		Transport:    "stdio",
		Dependencies: deps,
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	path := filepath.Join(pkgDir, "pharos.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func entry(version string) lockfile.ServerEntry {
	return lockfile.ServerEntry{Version: version}
}

// --- Tests ---

func TestFindDependents(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		lockfile   map[string]lockfile.ServerEntry
		setupPkgs  func(t *testing.T, storeDir string)
		want       []string
		wantErr    bool
	}{
		{
			name:   "no dependents — removal proceeds",
			target: "test-echo-server",
			lockfile: map[string]lockfile.ServerEntry{
				"test-echo-server":  entry("1.0.0"),
				"standalone-server": entry("2.0.0"),
			},
			setupPkgs: func(t *testing.T, storeDir string) {
				// standalone-server has no dependencies
				writeInstalledMeta(t, storeDir, "standalone-server", "2.0.0", nil)
			},
			want:    []string{},
			wantErr: false,
		},
		{
			name:   "one dependent via .pharos-installed.json",
			target: "test-echo-server",
			lockfile: map[string]lockfile.ServerEntry{
				"test-echo-server":   entry("1.0.0"),
				"test-shout-server":  entry("1.2.0"),
				"unrelated-server":   entry("3.0.0"),
			},
			setupPkgs: func(t *testing.T, storeDir string) {
				writeInstalledMeta(t, storeDir, "test-shout-server", "1.2.0",
					[]depInfo{{Name: "test-echo-server", Version: "^1.0.0"}})
				writeInstalledMeta(t, storeDir, "unrelated-server", "3.0.0", nil)
			},
			want:    []string{"test-shout-server"},
			wantErr: false,
		},
		{
			name:   "one dependent via pharos.json manifest",
			target: "test-echo-server",
			lockfile: map[string]lockfile.ServerEntry{
				"test-echo-server":  entry("1.0.0"),
				"test-shout-server": entry("1.2.0"),
			},
			setupPkgs: func(t *testing.T, storeDir string) {
				// No .pharos-installed.json deps, but pharos.json has them
				writeInstalledMeta(t, storeDir, "test-shout-server", "1.2.0", nil)
				writeManifestToStore(t, storeDir, "test-shout-server", "1.2.0",
					[]depInfo{{Name: "test-echo-server", Version: "^1.0.0"}})
			},
			want:    []string{"test-shout-server"},
			wantErr: false,
		},
		{
			name:   "multiple dependents — sorted output",
			target: "shared-lib",
			lockfile: map[string]lockfile.ServerEntry{
				"shared-lib":    entry("1.0.0"),
				"zebra-app":     entry("1.0.0"),
				"alpha-app":     entry("1.0.0"),
				"midnight-app":  entry("1.0.0"),
			},
			setupPkgs: func(t *testing.T, storeDir string) {
				writeInstalledMeta(t, storeDir, "zebra-app", "1.0.0",
					[]depInfo{{Name: "shared-lib", Version: "^1.0.0"}})
				writeInstalledMeta(t, storeDir, "alpha-app", "1.0.0",
					[]depInfo{{Name: "shared-lib", Version: "^1.0.0"}})
				// midnight-app does NOT depend on shared-lib
				writeInstalledMeta(t, storeDir, "midnight-app", "1.0.0",
					[]depInfo{{Name: "other-lib", Version: "^2.0.0"}})
			},
			want:    []string{"alpha-app", "zebra-app"},
			wantErr: false,
		},
		{
			name:   "empty lockfile — no dependents",
			target: "anything",
			lockfile: map[string]lockfile.ServerEntry{},
			setupPkgs: func(t *testing.T, storeDir string) {},
			want:    []string{},
			wantErr: false,
		},
		{
			name:   "missing lockfile — no dependents, no error",
			target: "anything",
			lockfile: nil,
			setupPkgs: func(t *testing.T, storeDir string) {},
			want:    []string{},
			wantErr: false,
		},
		{
			name:   "target not in lockfile — no dependents",
			target: "ghost-package",
			lockfile: map[string]lockfile.ServerEntry{
				"pkg-a": entry("1.0.0"),
			},
			setupPkgs: func(t *testing.T, storeDir string) {
				writeInstalledMeta(t, storeDir, "pkg-a", "1.0.0",
					[]depInfo{{Name: "ghost-package", Version: "^1.0.0"}})
			},
			want:    []string{"pkg-a"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			storeDir, lockPath := setupStore(t)
			if tt.lockfile != nil {
				writeLockfile(t, lockPath, tt.lockfile)
			}
			if tt.setupPkgs != nil {
				tt.setupPkgs(t, storeDir)
			}

			// Act
			got, err := findDependents(storeDir, lockPath, tt.target)

			// Assert
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("dependents count = %d, want %d (got %v)", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("dependents[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestCheckDependencies(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		force    bool
		lockfile map[string]lockfile.ServerEntry
		setupPkgs func(t *testing.T, storeDir string)
		wantErr  bool
		errContains string
	}{
		{
			name:   "no dependents — no error",
			target: "test-echo-server",
			force:  false,
			lockfile: map[string]lockfile.ServerEntry{
				"test-echo-server": entry("1.0.0"),
				"other-server":     entry("2.0.0"),
			},
			setupPkgs: func(t *testing.T, storeDir string) {
				writeInstalledMeta(t, storeDir, "other-server", "2.0.0", nil)
			},
			wantErr: false,
		},
		{
			name:   "has dependents without force — blocked",
			target: "test-echo-server",
			force:  false,
			lockfile: map[string]lockfile.ServerEntry{
				"test-echo-server":  entry("1.0.0"),
				"test-shout-server": entry("1.2.0"),
			},
			setupPkgs: func(t *testing.T, storeDir string) {
				writeInstalledMeta(t, storeDir, "test-shout-server", "1.2.0",
					[]depInfo{{Name: "test-echo-server", Version: "^1.0.0"}})
			},
			wantErr:     true,
			errContains: "required dependency of test-shout-server",
		},
		{
			name:   "has dependents with --force — bypassed",
			target: "test-echo-server",
			force:  true,
			lockfile: map[string]lockfile.ServerEntry{
				"test-echo-server":  entry("1.0.0"),
				"test-shout-server": entry("1.2.0"),
			},
			setupPkgs: func(t *testing.T, storeDir string) {
				writeInstalledMeta(t, storeDir, "test-shout-server", "1.2.0",
					[]depInfo{{Name: "test-echo-server", Version: "^1.0.0"}})
			},
			wantErr: false,
		},
		{
			name:   "missing lockfile — no error (don't block)",
			target: "anything",
			force:  false,
			lockfile: nil,
			setupPkgs: func(t *testing.T, storeDir string) {},
			wantErr: false,
		},
		{
			name:   "multiple dependents without force — blocked with all names",
			target: "shared-lib",
			force:  false,
			lockfile: map[string]lockfile.ServerEntry{
				"shared-lib": entry("1.0.0"),
				"alpha-app":  entry("1.0.0"),
				"zebra-app":  entry("1.0.0"),
			},
			setupPkgs: func(t *testing.T, storeDir string) {
				writeInstalledMeta(t, storeDir, "alpha-app", "1.0.0",
					[]depInfo{{Name: "shared-lib", Version: "^1.0.0"}})
				writeInstalledMeta(t, storeDir, "zebra-app", "1.0.0",
					[]depInfo{{Name: "shared-lib", Version: "^1.0.0"}})
			},
			wantErr:     true,
			errContains: "alpha-app, zebra-app",
		},
		{
			name:   "multiple dependents with --force — bypassed",
			target: "shared-lib",
			force:  true,
			lockfile: map[string]lockfile.ServerEntry{
				"shared-lib": entry("1.0.0"),
				"alpha-app":  entry("1.0.0"),
				"zebra-app":  entry("1.0.0"),
			},
			setupPkgs: func(t *testing.T, storeDir string) {
				writeInstalledMeta(t, storeDir, "alpha-app", "1.0.0",
					[]depInfo{{Name: "shared-lib", Version: "^1.0.0"}})
				writeInstalledMeta(t, storeDir, "zebra-app", "1.0.0",
					[]depInfo{{Name: "shared-lib", Version: "^1.0.0"}})
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			storeDir, lockPath := setupStore(t)
			if tt.lockfile != nil {
				writeLockfile(t, lockPath, tt.lockfile)
			}
			if tt.setupPkgs != nil {
				tt.setupPkgs(t, storeDir)
			}

			// Act
			err := checkDependencies(storeDir, lockPath, tt.target, tt.force)

			// Assert
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr && tt.errContains != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errContains)
				}
				if !contains(err.Error(), tt.errContains) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), tt.errContains)
				}
			}
		})
	}
}

func TestPackageDependsOn(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		pkgName string
		version string
		setup   func(t *testing.T, storeDir string)
		want    bool
	}{
		{
			name:    "depends via .pharos-installed.json",
			target:  "test-echo-server",
			pkgName: "test-shout-server",
			version: "1.0.0",
			setup: func(t *testing.T, storeDir string) {
				writeInstalledMeta(t, storeDir, "test-shout-server", "1.0.0",
					[]depInfo{{Name: "test-echo-server", Version: "^1.0.0"}})
			},
			want: true,
		},
		{
			name:    "depends via pharos.json only",
			target:  "test-echo-server",
			pkgName: "test-shout-server",
			version: "1.0.0",
			setup: func(t *testing.T, storeDir string) {
				writeManifestToStore(t, storeDir, "test-shout-server", "1.0.0",
					[]depInfo{{Name: "test-echo-server", Version: "^1.0.0"}})
			},
			want: true,
		},
		{
			name:    "does not depend — different dep",
			target:  "test-echo-server",
			pkgName: "other-app",
			version: "1.0.0",
			setup: func(t *testing.T, storeDir string) {
				writeInstalledMeta(t, storeDir, "other-app", "1.0.0",
					[]depInfo{{Name: "different-lib", Version: "^1.0.0"}})
			},
			want: false,
		},
		{
			name:    "no metadata files — not dependent",
			target:  "test-echo-server",
			pkgName: "bare-package",
			version: "1.0.0",
			setup: func(t *testing.T, storeDir string) {
				// Create the dir but no metadata files
				dir := filepath.Join(storeDir, "bare-package", "1.0.0")
				os.MkdirAll(dir, 0o755)
			},
			want: false,
		},
		{
			name:    "multiple deps — one matches",
			target:  "lib-c",
			pkgName: "multi-dep-app",
			version: "2.0.0",
			setup: func(t *testing.T, storeDir string) {
				writeInstalledMeta(t, storeDir, "multi-dep-app", "2.0.0",
					[]depInfo{
						{Name: "lib-a", Version: "^1.0.0"},
						{Name: "lib-c", Version: "^2.0.0"},
						{Name: "lib-b", Version: "^1.0.0"},
					})
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			storeDir, _ := setupStore(t)
			tt.setup(t, storeDir)

			// Act
			got := packageDependsOn(storeDir, tt.pkgName, tt.version, tt.target)

			// Assert
			if got != tt.want {
				t.Errorf("packageDependsOn(%q, %q, %q, %q) = %v, want %v",
					storeDir, tt.pkgName, tt.version, tt.target, got, tt.want)
			}
		})
	}
}

// contains is a simple substring check (avoids importing strings in test).
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
