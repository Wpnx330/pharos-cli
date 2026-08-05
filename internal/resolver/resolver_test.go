package resolver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/api"
)

// --- Mock registry helpers ---

type mockVersion struct {
	Version   string
	Manifest  api.Manifest
	CreatedAt string
	Status    string
}

func mockPackageJSON(versions []mockVersion, distTags map[string]string) string {
	type versionJSON struct {
		Version    string        `json:"version"`
		Manifest   api.Manifest  `json:"manifest"`
		CreatedAt  string        `json:"created_at"`
		Status     string        `json:"status"`
		Deprecated bool          `json:"deprecated"`
	}
	type packageJSON struct {
		Name      string         `json:"name"`
		Versions  []versionJSON  `json:"versions"`
		Latest    string         `json:"latest"`
		DistTags  map[string]string `json:"dist_tags,omitempty"`
	}

	vers := make([]versionJSON, len(versions))
	for i, v := range versions {
		vers[i] = versionJSON{
			Version:   v.Version,
			Manifest:  v.Manifest,
			CreatedAt: v.CreatedAt,
			Status:    v.Status,
		}
	}
	pkg := packageJSON{
		Name:     versions[0].Manifest.Name,
		Versions: vers,
		Latest:   versions[len(versions)-1].Version,
		DistTags: distTags,
	}
	b, _ := json.Marshal(pkg)
	return string(b)
}

func newMockRegistry(t *testing.T, packages map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for name, body := range packages {
		path := "/v1/packages/" + name
		mux.HandleFunc(path, func(name, body string) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(body))
			}
		}(name, body))
	}
	// Fallback 404
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	return httptest.NewServer(mux)
}

// --- Tests ---

func TestResolveSimpleDependency(t *testing.T) {
	// pkg-a@1.0.0 depends on pkg-b@^1.0.0
	pkgA := mockPackageJSON([]mockVersion{{
		Version: "1.0.0",
		Manifest: api.Manifest{
			Name:         "pkg-a",
			Version:      "1.0.0",
			Dependencies: []api.Dependency{{Name: "pkg-b", Version: "^1.0.0"}},
		},
	}}, nil)

	pkgB := mockPackageJSON([]mockVersion{
		{Version: "1.0.0", Manifest: api.Manifest{Name: "pkg-b", Version: "1.0.0"}},
		{Version: "1.1.0", Manifest: api.Manifest{Name: "pkg-b", Version: "1.1.0"}},
		{Version: "1.2.0", Manifest: api.Manifest{Name: "pkg-b", Version: "1.2.0"}},
	}, nil)

	srv := newMockRegistry(t, map[string]string{
		"pkg-a": pkgA,
		"pkg-b": pkgB,
	})
	defer srv.Close()

	client := api.New(srv.URL, "")
	r := New(client)
	result, err := r.Resolve("pkg-a", "1.0.0")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := result.Flat["pkg-a"]; got != "1.0.0" {
		t.Errorf("pkg-a resolved to %s, want 1.0.0", got)
	}
	if got := result.Flat["pkg-b"]; got != "1.2.0" {
		t.Errorf("pkg-b resolved to %s, want 1.2.0 (highest ^1.0.0)", got)
	}
	if len(result.Conflicts) != 0 {
		t.Errorf("expected 0 conflicts, got %d", len(result.Conflicts))
	}
	if len(result.Circular) != 0 {
		t.Errorf("expected 0 circular, got %d", len(result.Circular))
	}
}

func TestResolveTransitiveDependencies(t *testing.T) {
	// pkg-a → pkg-b → pkg-c
	pkgA := mockPackageJSON([]mockVersion{{
		Version: "1.0.0",
		Manifest: api.Manifest{
			Name:         "pkg-a",
			Version:      "1.0.0",
			Dependencies: []api.Dependency{{Name: "pkg-b", Version: "^1.0.0"}},
		},
	}}, nil)

	pkgB := mockPackageJSON([]mockVersion{{
		Version: "1.0.0",
		Manifest: api.Manifest{
			Name:         "pkg-b",
			Version:      "1.0.0",
			Dependencies: []api.Dependency{{Name: "pkg-c", Version: "^0.5.0"}},
		},
	}}, nil)

	pkgC := mockPackageJSON([]mockVersion{{
		Version: "0.5.2",
		Manifest: api.Manifest{Name: "pkg-c", Version: "0.5.2"},
	}}, nil)

	srv := newMockRegistry(t, map[string]string{
		"pkg-a": pkgA,
		"pkg-b": pkgB,
		"pkg-c": pkgC,
	})
	defer srv.Close()

	client := api.New(srv.URL, "")
	r := New(client)
	result, err := r.Resolve("pkg-a", "1.0.0")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := map[string]string{
		"pkg-a": "1.0.0",
		"pkg-b": "1.0.0",
		"pkg-c": "0.5.2",
	}
	for name, want := range expected {
		if got := result.Flat[name]; got != want {
			t.Errorf("%s resolved to %s, want %s", name, got, want)
		}
	}
}

func TestResolveCircularDependency(t *testing.T) {
	// pkg-a → pkg-b → pkg-a (circular)
	pkgA := mockPackageJSON([]mockVersion{{
		Version: "1.0.0",
		Manifest: api.Manifest{
			Name:         "pkg-a",
			Version:      "1.0.0",
			Dependencies: []api.Dependency{{Name: "pkg-b", Version: "^1.0.0"}},
		},
	}}, nil)

	pkgB := mockPackageJSON([]mockVersion{{
		Version: "1.0.0",
		Manifest: api.Manifest{
			Name:         "pkg-b",
			Version:      "1.0.0",
			Dependencies: []api.Dependency{{Name: "pkg-a", Version: "^1.0.0"}},
		},
	}}, nil)

	srv := newMockRegistry(t, map[string]string{
		"pkg-a": pkgA,
		"pkg-b": pkgB,
	})
	defer srv.Close()

	client := api.New(srv.URL, "")
	r := New(client)
	result, err := r.Resolve("pkg-a", "1.0.0")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Circular) == 0 {
		t.Error("expected circular dependency detection, got none")
	}
	// Both packages should still be resolved
	if got := result.Flat["pkg-a"]; got != "1.0.0" {
		t.Errorf("pkg-a resolved to %s, want 1.0.0", got)
	}
	if got := result.Flat["pkg-b"]; got != "1.0.0" {
		t.Errorf("pkg-b resolved to %s, want 1.0.0", got)
	}
}

func TestResolveVersionConflict(t *testing.T) {
	// pkg-a depends on pkg-c@^1.0.0, pkg-b depends on pkg-c@^1.1.0
	// Available: 1.0.0, 1.1.0, 1.2.0
	// ^1.0.0 → 1.2.0, ^1.1.0 → 1.2.0 — actually these resolve to the same version
	// Let's make a real conflict: pkg-a depends on pkg-c@~1.0.0, pkg-b depends on pkg-c@~1.1.0
	pkgA := mockPackageJSON([]mockVersion{{
		Version: "1.0.0",
		Manifest: api.Manifest{
			Name: "pkg-a", Version: "1.0.0",
			Dependencies: []api.Dependency{
				{Name: "pkg-b", Version: "^1.0.0"},
				{Name: "pkg-c", Version: "~1.0.0"},
			},
		},
	}}, nil)

	pkgB := mockPackageJSON([]mockVersion{{
		Version: "1.0.0",
		Manifest: api.Manifest{
			Name: "pkg-b", Version: "1.0.0",
			Dependencies: []api.Dependency{
				{Name: "pkg-c", Version: "~1.1.0"},
			},
		},
	}}, nil)

	pkgC := mockPackageJSON([]mockVersion{
		{Version: "1.0.0", Manifest: api.Manifest{Name: "pkg-c", Version: "1.0.0"}},
		{Version: "1.0.1", Manifest: api.Manifest{Name: "pkg-c", Version: "1.0.1"}},
		{Version: "1.1.0", Manifest: api.Manifest{Name: "pkg-c", Version: "1.1.0"}},
	}, nil)

	srv := newMockRegistry(t, map[string]string{
		"pkg-a": pkgA,
		"pkg-b": pkgB,
		"pkg-c": pkgC,
	})
	defer srv.Close()

	client := api.New(srv.URL, "")
	r := New(client)
	result, err := r.Resolve("pkg-a", "1.0.0")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Conflicts) == 0 {
		t.Error("expected version conflict, got none")
	}
	// The higher version (1.1.0) should win
	if got := result.Flat["pkg-c"]; got != "1.1.0" {
		t.Errorf("pkg-c resolved to %s, want 1.1.0 (higher)", got)
	}
}

func TestResolveNoDependencies(t *testing.T) {
	pkgA := mockPackageJSON([]mockVersion{{
		Version:  "1.0.0",
		Manifest: api.Manifest{Name: "pkg-a", Version: "1.0.0"},
	}}, nil)

	srv := newMockRegistry(t, map[string]string{"pkg-a": pkgA})
	defer srv.Close()

	client := api.New(srv.URL, "")
	r := New(client)
	result, err := r.Resolve("pkg-a", "latest")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Flat) != 1 {
		t.Errorf("expected 1 package in flat map, got %d", len(result.Flat))
	}
	if got := result.Flat["pkg-a"]; got != "1.0.0" {
		t.Errorf("pkg-a resolved to %s, want 1.0.0", got)
	}
	if len(result.Tree) != 1 {
		t.Errorf("expected 1 tree node, got %d", len(result.Tree))
	}
	if len(result.Tree[0].Dependencies) != 0 {
		t.Errorf("expected 0 child deps, got %d", len(result.Tree[0].Dependencies))
	}
}

func TestResolveAllMultipleTopLevel(t *testing.T) {
	pkgA := mockPackageJSON([]mockVersion{{
		Version:  "1.0.0",
		Manifest: api.Manifest{Name: "pkg-a", Version: "1.0.0"},
	}}, nil)

	pkgB := mockPackageJSON([]mockVersion{{
		Version:  "2.0.0",
		Manifest: api.Manifest{Name: "pkg-b", Version: "2.0.0"},
	}}, nil)

	srv := newMockRegistry(t, map[string]string{
		"pkg-a": pkgA,
		"pkg-b": pkgB,
	})
	defer srv.Close()

	client := api.New(srv.URL, "")
	r := New(client)
	result, err := r.ResolveAll([]api.Dependency{
		{Name: "pkg-a", Version: "^1.0.0"},
		{Name: "pkg-b", Version: "^2.0.0"},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := result.Flat["pkg-a"]; got != "1.0.0" {
		t.Errorf("pkg-a resolved to %s, want 1.0.0", got)
	}
	if got := result.Flat["pkg-b"]; got != "2.0.0" {
		t.Errorf("pkg-b resolved to %s, want 2.0.0", got)
	}
	if len(result.Tree) != 2 {
		t.Errorf("expected 2 tree nodes, got %d", len(result.Tree))
	}
}

func TestResolveNilClient(t *testing.T) {
	r := New(nil)
	_, err := r.Resolve("anything", "latest")
	if err == nil {
		t.Error("expected error for nil client, got nil")
	}
}

func TestResolveNonExistentPackage(t *testing.T) {
	srv := newMockRegistry(t, map[string]string{})
	defer srv.Close()

	client := api.New(srv.URL, "")
	r := New(client)
	_, err := r.Resolve("ghost", "latest")
	if err == nil {
		t.Error("expected error for non-existent package, got nil")
	}
}

func TestFlatList(t *testing.T) {
	flat := map[string]string{
		"pkg-c": "1.1.0",
		"pkg-a": "1.0.0",
		"pkg-b": "2.0.0",
	}
	list := FlatList(flat)

	expected := []string{"pkg-a@1.0.0", "pkg-b@2.0.0", "pkg-c@1.1.0"}
	if len(list) != len(expected) {
		t.Fatalf("expected %d entries, got %d", len(expected), len(list))
	}
	for i, want := range expected {
		if list[i] != want {
			t.Errorf("list[%d] = %s, want %s", i, list[i], want)
		}
	}
}

func TestHigherVersion(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want string
	}{
		{"semver_higher", "1.0.0", "1.1.0", "1.1.0"},
		{"semver_lower", "1.2.0", "1.0.0", "1.2.0"},
		{"equal", "1.0.0", "1.0.0", "1.0.0"},
		{"major_bump", "1.0.0", "2.0.0", "2.0.0"},
		{"invalid_fallback", "abc", "def", "def"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := higherVersion(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("higherVersion(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
