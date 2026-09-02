package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
	"github.com/Wpnx330/pharos-cli/internal/lockfile"
)

func newTarballServer(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		if len(body) > 0 {
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// makeTarball creates a gzipped tar archive containing a single file.
func makeTarball(t *testing.T, filename, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	hdr := &tar.Header{
		Name: filename,
		Mode: 0o644,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte(content))
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func TestExtract(t *testing.T) {
	dest := t.TempDir()
	data := makeTarball(t, "test.txt", "hello world")
	if err := Extract(data, dest); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(dest, "test.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello world" {
		t.Errorf("content = %s", string(content))
	}
}

func TestExtractPathTraversal(t *testing.T) {
	dest := t.TempDir()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	hdr := &tar.Header{
		Name: "../../../etc/passwd",
		Mode: 0o644,
		Size: 5,
	}
	tw.WriteHeader(hdr)
	tw.Write([]byte("hello"))
	tw.Close()
	gw.Close()

	err := Extract(buf.Bytes(), dest)
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestComputeIntegrity(t *testing.T) {
	data := []byte("hello")
	got := ComputeIntegrity(data)
	if !strings.HasPrefix(got, "sha512-") {
		t.Errorf("integrity should start with sha512-, got %s", got)
	}
	// Verify it matches manual computation.
	h := sha512.Sum512(data)
	expected := "sha512-" + base64.StdEncoding.EncodeToString(h[:])
	if got != expected {
		t.Errorf("integrity = %s, want %s", got, expected)
	}
}

func TestVerifyIntegrityMatch(t *testing.T) {
	data := []byte("hello")
	integrity := ComputeIntegrity(data)
	if err := VerifyIntegrity(data, integrity); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestVerifyIntegrityMismatch(t *testing.T) {
	data := []byte("hello")
	if err := VerifyIntegrity(data, "sha512-badbadbad"); err == nil {
		t.Fatal("expected error for mismatch")
	}
}

func TestVerifyIntegrityEmptySkips(t *testing.T) {
	if err := VerifyIntegrity([]byte("anything"), ""); err != nil {
		t.Errorf("expected nil for empty integrity, got %v", err)
	}
}

func TestIsInstalled(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)
	if mgr.IsInstalled("test", "1.0.0") {
		t.Error("should not be installed initially")
	}
	pkg := &InstalledPackage{Name: "test", Version: "1.0.0"}
	if err := mgr.saveMetadata("test", "1.0.0", pkg); err != nil {
		t.Fatal(err)
	}
	if !mgr.IsInstalled("test", "1.0.0") {
		t.Error("should be installed after metadata write")
	}
}

func TestIsAnyVersionInstalled(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)
	if mgr.IsAnyVersionInstalled("test") {
		t.Error("should not be installed initially")
	}
	mgr.saveMetadata("test", "1.0.0", &InstalledPackage{Name: "test", Version: "1.0.0"})
	if !mgr.IsAnyVersionInstalled("test") {
		t.Error("should be installed after metadata write")
	}
}

func TestListEmpty(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)
	pkgs, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	if pkgs != nil {
		t.Errorf("expected nil, got %v", pkgs)
	}
}

func TestListWithPackage(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)
	pkg := &InstalledPackage{Name: "test", Version: "1.0.0", Location: "/some/path"}
	if err := mgr.saveMetadata("test", "1.0.0", pkg); err != nil {
		t.Fatal(err)
	}
	pkgs, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 package, got %d", len(pkgs))
	}
	if pkgs[0].Name != "test" {
		t.Errorf("name = %s", pkgs[0].Name)
	}
	if pkgs[0].Version != "1.0.0" {
		t.Errorf("version = %s", pkgs[0].Version)
	}
}

func TestInstallStdioIntegrityMismatch(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)
	// We can't easily test the full HTTP download path without a server,
	// but we can test the integrity check path.
	_, err := mgr.InstallStdio("test", "1.0.0", "http://localhost:0/nope", "sha512-badbadbad")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildServerConfigNpx(t *testing.T) {
	m := api.Manifest{
		Transport: "stdio",
		Runtime:   "npx",
		Package:   "@scope/server",
		Args:      []string{"--flag"},
		Env:       map[string]string{"API_KEY": "xxx"},
	}
	cfg := BuildServerConfig(m, "/store")
	if cfg.Command != "npx" {
		t.Errorf("command = %s", cfg.Command)
	}
	if len(cfg.Args) != 3 || cfg.Args[0] != "-y" || cfg.Args[1] != "@scope/server" || cfg.Args[2] != "--flag" {
		t.Errorf("args = %v", cfg.Args)
	}
	if cfg.Env["API_KEY"] != "xxx" {
		t.Errorf("env not set")
	}
}

func TestBuildServerConfigUvx(t *testing.T) {
	m := api.Manifest{Transport: "stdio", Runtime: "uvx", Package: "mcp-server-git"}
	cfg := BuildServerConfig(m, "/store")
	if cfg.Command != "uvx" {
		t.Errorf("command = %s", cfg.Command)
	}
	if len(cfg.Args) != 1 || cfg.Args[0] != "mcp-server-git" {
		t.Errorf("args = %v", cfg.Args)
	}
}

func TestBuildServerConfigDocker(t *testing.T) {
	m := api.Manifest{Transport: "stdio", Runtime: "docker", Package: "myimg:latest"}
	cfg := BuildServerConfig(m, "/store")
	if cfg.Command != "docker" {
		t.Errorf("command = %s", cfg.Command)
	}
	if cfg.Args[0] != "run" || cfg.Args[1] != "-i" || cfg.Args[2] != "--rm" {
		t.Errorf("args = %v", cfg.Args)
	}
}

func TestBuildServerConfigBinary(t *testing.T) {
	m := api.Manifest{
		Name:      "test-server",
		Version:   "1.0.0",
		Transport: "stdio",
		Runtime:   "binary",
		Bin:       "bin/server",
	}
	cfg := BuildServerConfig(m, "/store")
	expected := filepath.Join("/store", "test-server", "1.0.0", "bin/server")
	if cfg.Command != expected {
		t.Errorf("command = %s, want %s", cfg.Command, expected)
	}
}

func TestBuildServerConfigHTTP(t *testing.T) {
	m := api.Manifest{Transport: "http", Endpoint: "https://example.com/mcp"}
	cfg := BuildServerConfig(m, "/store")
	if cfg.URL != "https://example.com/mcp" {
		t.Errorf("url = %s", cfg.URL)
	}
	if cfg.Type != "http" {
		t.Errorf("type = %s", cfg.Type)
	}
}

func TestBuildServerConfigSSE(t *testing.T) {
	m := api.Manifest{Transport: "sse", Endpoint: "https://example.com/sse"}
	cfg := BuildServerConfig(m, "/store")
	if cfg.Type != "sse" {
		t.Errorf("type = %s", cfg.Type)
	}
}

func TestBuildServerConfigDefaultFallback(t *testing.T) {
	m := api.Manifest{Transport: "stdio", Package: "some-pkg"}
	cfg := BuildServerConfig(m, "/store")
	if cfg.Command != "npx" {
		t.Errorf("expected npx fallback, got %s", cfg.Command)
	}
}

func TestBuildServerConfigCommand(t *testing.T) {
	m := api.Manifest{
		Transport: "stdio",
		Command:   "python -m src.server",
		Args:      []string{"--debug"},
		Env:       map[string]string{"API_KEY": "xxx"},
	}
	cfg := BuildServerConfig(m, "/store")
	if cfg.Command != "python" {
		t.Errorf("command = %s, want python", cfg.Command)
	}
	// Args should be ["-m", "src.server", "--debug"]
	if len(cfg.Args) != 3 || cfg.Args[0] != "-m" || cfg.Args[1] != "src.server" || cfg.Args[2] != "--debug" {
		t.Errorf("args = %v, want [-m src.server --debug]", cfg.Args)
	}
	if cfg.Env["API_KEY"] != "xxx" {
		t.Errorf("env not set")
	}
}

func TestBuildServerConfigCommandNoArgs(t *testing.T) {
	m := api.Manifest{
		Transport: "stdio",
		Command:   "my-binary",
	}
	cfg := BuildServerConfig(m, "/store")
	if cfg.Command != "my-binary" {
		t.Errorf("command = %s, want my-binary", cfg.Command)
	}
	if len(cfg.Args) != 0 {
		t.Errorf("args = %v, want empty", cfg.Args)
	}
}

func TestBuildServerConfigPython(t *testing.T) {
	m := api.Manifest{
		Transport: "stdio",
		Runtime:   "python",
		Package:   "src.server",
		Args:      []string{"--port", "8080"},
	}
	cfg := BuildServerConfig(m, "/store")
	if cfg.Command != "python3" {
		t.Errorf("command = %s, want python3", cfg.Command)
	}
	if len(cfg.Args) != 3 || cfg.Args[0] != "src.server" || cfg.Args[1] != "--port" || cfg.Args[2] != "8080" {
		t.Errorf("args = %v, want [src.server --port 8080]", cfg.Args)
	}
}

func kind2PythonBinManifest() api.Manifest {
	return api.Manifest{
		Name:      "test-echo-server",
		Version:   "0.2.6",
		Transport: "http-sse",
		Runtime:   "python",
		Bin:       "python server.py",
		Package:   "test-echo-server",
	}
}

func TestBuildServerConfigKind2PythonRuntimeUsesBinNotPackage(t *testing.T) {
	m := kind2PythonBinManifest()
	cfg := BuildServerConfig(m, "/store")
	if cfg.URL != "" {
		t.Errorf("kind 2 launch must not set a URL, got %q", cfg.URL)
	}
	if cfg.Command != "python" {
		t.Fatalf("command = %q, want python from bin (not python3 + package)", cfg.Command)
	}
	if len(cfg.Args) != 1 || cfg.Args[0] != "server.py" {
		t.Fatalf("args = %v, want [server.py] from bin", cfg.Args)
	}
	if cfg.Command == "python3" {
		t.Fatal("must not rewrite bin to python3 <package name>")
	}
	if len(cfg.Args) > 0 && cfg.Args[0] == "test-echo-server" {
		t.Fatal("must not spawn python3 test-echo-server")
	}
}

func TestBuildServerConfigKind2Python3BinTokenPreserved(t *testing.T) {
	m := kind2PythonBinManifest()
	m.Bin = "python3 server.py"
	cfg := BuildServerConfig(m, "/store")
	if cfg.Command != "python3" || len(cfg.Args) != 1 || cfg.Args[0] != "server.py" {
		t.Fatalf("command/args = %s %v, want python3 [server.py]", cfg.Command, cfg.Args)
	}
}

func TestBuildServerConfigKind3NpxNoBinUnchanged(t *testing.T) {
	m := api.Manifest{
		Transport: "stdio",
		Runtime:   "npx",
		Package:   "@j0hanz/filesystem-mcp",
	}
	cfg := BuildServerConfig(m, "/store")
	if cfg.URL != "" {
		t.Errorf("kind 3 must not have URL, got %q", cfg.URL)
	}
	if cfg.Command != "npx" {
		t.Errorf("command = %q, want npx", cfg.Command)
	}
	if len(cfg.Args) != 2 || cfg.Args[0] != "-y" || cfg.Args[1] != "@j0hanz/filesystem-mcp" {
		t.Errorf("args = %v, want [-y @j0hanz/filesystem-mcp]", cfg.Args)
	}
}

func TestNormalizeTransport(t *testing.T) {
	tests := []struct{ in, want string }{
		{"stdio", "stdio"},
		{"STDIO", "stdio"},
		{"http", "http"},
		{"HTTP", "http"},
		{"sse", "sse"},
		{"SSE", "sse"},
		{"https", "sse"},
		{"", "stdio"},
		{"http-sse", "sse"},
		{"http+sse", "sse"},
		{"streamable-http", "http"},
		{"STREAMABLE-HTTP", "http"},
	}
	for _, tc := range tests {
		if got := normalizeTransport(tc.in); got != tc.want {
			t.Errorf("normalizeTransport(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeTransportNeverMapsHTTPFamilyToStdio(t *testing.T) {
	for _, in := range []string{"http-sse", "http+sse", "streamable-http", "sse", "http"} {
		got := normalizeTransport(in)
		if got == "stdio" || got == "npx" {
			t.Errorf("normalizeTransport(%q) = %q; http-family must not become stdio/npx", in, got)
		}
	}
}

func TestBuildServerConfigHTTPFamilyNeverFallsToNpx(t *testing.T) {
	cases := []api.Manifest{
		{Transport: "http-sse", Endpoint: "https://example.com/sse"},
		{Transport: "http+sse", Endpoint: "https://example.com/sse"},
		{Transport: "streamable-http", Endpoint: "https://example.com/mcp"},
	}
	for _, m := range cases {
		cfg := BuildServerConfig(m, "/store")
		if cfg.URL == "" {
			t.Errorf("transport %q: expected client URL, got command=%q args=%v", m.Transport, cfg.Command, cfg.Args)
		}
		if cfg.Command == "npx" {
			t.Errorf("transport %q must not fall through to npx", m.Transport)
		}
		if cfg.Type == "stdio" {
			t.Errorf("transport %q client type must stay http-family, got stdio", m.Transport)
		}
	}
}

func TestBuildServerConfigKind2LaunchLineNoEndpoint(t *testing.T) {
	m := api.Manifest{
		Name:      "test-echo-server",
		Version:   "0.2.4",
		Transport: "http-sse",
		Bin:       "test-echo-server --port 8765",
	}
	cfg := BuildServerConfig(m, "/store")
	if cfg.URL != "" {
		t.Errorf("kind 2 has no publisher URL, got %q", cfg.URL)
	}
	if cfg.Command == "npx" {
		t.Fatal("kind 2 must persist launch line, not npx fallback")
	}
	if cfg.Command == "" && len(cfg.Args) == 0 {
		t.Fatal("kind 2 must have a launch command from bin")
	}
}

func TestInstallHTTPBookmarkNoTarball(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)
	res, err := mgr.InstallHTTPBookmark("com.invokera/world-time", "1.0.0", "streamable-http", "https://world-time.example/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if res.Transport == "stdio" {
		t.Fatal("kind 1 transport must stay http-family")
	}
	if res.Endpoint != "https://world-time.example/mcp" {
		t.Errorf("endpoint = %q", res.Endpoint)
	}
	if res.Location != "" {
		t.Errorf("kind 1 must not extract a tarball, location=%q", res.Location)
	}
	if res.Kind != KindRemoteHTTP {
		t.Errorf("kind = %v, want KindRemoteHTTP", res.Kind)
	}
	if !mgr.IsInstalled("com.invokera/world-time", "1.0.0") {
		t.Fatal("bookmark metadata missing")
	}
	pkgs, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0].Endpoint != "https://world-time.example/mcp" {
		t.Fatalf("persisted bookmark = %+v", pkgs)
	}
}

func TestInstallByKindRemoteHTTPNoTarballFetch(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)
	// A tarball URL that would 404 if fetched — kind 1 must not touch it.
	res, err := mgr.InstallByKind(InstallOptions{
		Name:       "com.invokera/world-time",
		Version:    "1.2.0",
		TarballURL: "http://127.0.0.1:1/v1/tarballs/com.invokera/world-time/1.2.0",
		Manifest: api.Manifest{
			Name:      "com.invokera/world-time",
			Version:   "1.2.0",
			Transport: "streamable-http",
			Endpoint:  "https://world-time.example/mcp",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != KindRemoteHTTP {
		t.Fatalf("kind = %v", res.Kind)
	}
	if res.Location != "" {
		t.Fatal("kind 1 must not download/extract")
	}
}

func TestInstallByKindLocalHTTPDownloadsWhen200(t *testing.T) {
	tarball := makeTarball(t, "bin/server", "echo")
	srv := newTarballServer(t, 200, tarball)
	dir := t.TempDir()
	mgr := NewManager(dir)
	res, err := mgr.InstallByKind(InstallOptions{
		Name:              "test-echo-server",
		Version:           "0.2.4",
		TarballURL:        srv.URL + "/pkg.tgz",
		ExpectedIntegrity: "",
		Manifest: api.Manifest{
			Name:      "test-echo-server",
			Version:   "0.2.4",
			Transport: "http-sse",
			Bin:       "bin/server",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != KindLocalHTTP {
		t.Fatalf("kind = %v, want KindLocalHTTP", res.Kind)
	}
	if res.Location == "" {
		t.Fatal("kind 2 with tarball 200 must extract")
	}
	if res.Transport == "stdio" {
		t.Fatal("kind 2 must keep http-family transport")
	}
	if _, err := os.Stat(filepath.Join(res.Location, "bin/server")); err != nil {
		t.Fatalf("extracted file missing: %v", err)
	}
}

func TestInstallByKindLocalHTTPNoTarballPersistsLaunch(t *testing.T) {
	srv := newTarballServer(t, 404, nil)
	dir := t.TempDir()
	mgr := NewManager(dir)
	res, err := mgr.InstallByKind(InstallOptions{
		Name:       "test-echo-server",
		Version:    "0.2.4",
		TarballURL: srv.URL + "/missing.tgz",
		Manifest: api.Manifest{
			Name:      "test-echo-server",
			Version:   "0.2.4",
			Transport: "http-sse",
			Command:   "test-echo-server --port 8765",
			Bin:       "test-echo-server",
		},
	})
	if err != nil {
		t.Fatalf("kind 2 without tarball must not 404-fail: %v", err)
	}
	if res.Kind != KindLocalHTTP {
		t.Fatalf("kind = %v", res.Kind)
	}
	if res.Command == "" && res.Bin == "" {
		t.Fatal("expected persisted launch line")
	}
	if res.Transport == "stdio" {
		t.Fatal("kind 2 transport must stay http-family")
	}
}

func TestInstallByKindStdioNativeTarball(t *testing.T) {
	tarball := makeTarball(t, "pharos.json", `{"name":"ev4nv-models"}`)
	integrity := ComputeIntegrity(tarball)
	srv := newTarballServer(t, 200, tarball)
	dir := t.TempDir()
	mgr := NewManager(dir)
	res, err := mgr.InstallByKind(InstallOptions{
		Name:              "ev4nv-models",
		Version:           "1.0.0",
		TarballURL:        srv.URL + "/pkg.tgz",
		ExpectedIntegrity: integrity,
		Manifest: api.Manifest{
			Name:      "ev4nv-models",
			Version:   "1.0.0",
			Transport: "stdio",
			Bin:       "bin/server",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != KindStdio {
		t.Fatalf("kind = %v, want KindStdio", res.Kind)
	}
	if res.Location == "" {
		t.Fatal("kind 3 native tarball must extract")
	}
}

func TestInstallByKindStdioMcpIONoTarballRequired(t *testing.T) {
	srv := newTarballServer(t, 404, nil)
	dir := t.TempDir()
	mgr := NewManager(dir)
	res, err := mgr.InstallByKind(InstallOptions{
		Name:       "io.modelcontextprotocol/server-everything",
		Version:    "0.6.2",
		TarballURL: srv.URL + "/v1/tarballs/missing",
		Manifest: api.Manifest{
			Name:      "io.modelcontextprotocol/server-everything",
			Version:   "0.6.2",
			Transport: "stdio",
			Runtime:   "npx",
			Package:   "@modelcontextprotocol/server-everything",
			Command:   "npx -y @modelcontextprotocol/server-everything",
		},
	})
	if err != nil {
		t.Fatalf("kind 3 mcp.io must not require tarball: %v", err)
	}
	if res.Kind != KindStdio {
		t.Fatalf("kind = %v", res.Kind)
	}
	if res.Command == "" && res.Runtime == "" {
		t.Fatal("must persist command / runtime+package")
	}
}

func TestInstallByKindNotInstallable(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)
	_, err := mgr.InstallByKind(InstallOptions{
		Name:    "empty",
		Version: "1.0.0",
		Manifest: api.Manifest{
			Transport: "http-sse",
		},
	})
	if err == nil {
		t.Fatal("F6 must fail as not installable")
	}
}

func TestInstallByKindRemoteOnlyRejectsLocal(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)
	t.Setenv("PHAROS_REMOTE_ONLY", "true")
	_, err := mgr.InstallByKind(InstallOptions{
		Name:    "test-echo-server",
		Version: "0.2.4",
		Manifest: api.Manifest{
			Transport: "http-sse",
			Bin:       "test-echo-server",
		},
	})
	if err == nil {
		t.Fatal("F7 must reject kind 2 under REMOTE_ONLY")
	}
}

func TestInstallByKindSkipsIntegrityWhenEmpty(t *testing.T) {
	tarball := makeTarball(t, "bin/server", "ok")
	srv := newTarballServer(t, 200, tarball)
	dir := t.TempDir()
	mgr := NewManager(dir)
	_, err := mgr.InstallByKind(InstallOptions{
		Name:              "pkg",
		Version:           "1.0.0",
		TarballURL:        srv.URL + "/pkg.tgz",
		ExpectedIntegrity: "",
		Manifest: api.Manifest{
			Transport: "stdio",
			Bin:       "bin/server",
		},
	})
	if err != nil {
		t.Fatalf("empty integrity must skip check: %v", err)
	}
}

func TestValidateInstallIdentityRejectsTraversal(t *testing.T) {
	err := ValidateInstallIdentity("../etc", "passwd")
	if err == nil {
		t.Fatal("expected rejection of path traversal in name")
	}
	err = ValidateInstallIdentity("pkg", "..")
	if err == nil {
		t.Fatal("expected rejection of path traversal in version")
	}
	if err := ValidateInstallIdentity("com.invokera/world-time", "1.0.0"); err != nil {
		t.Fatalf("scoped name must be allowed: %v", err)
	}
}

func isolateWindowsUsers(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "win-users")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PHAROS_WINDOWS_USERS_ROOT", root)
	return root
}

func TestWriteClientConfigsWithMockClient(t *testing.T) {
	// Set up a fake generic client.
	isolateWindowsUsers(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "mcp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	server := clientconfig.ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}}
	updated, skipped, err := WriteClientConfigs("test-server", server, []string{"generic"})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("expected 0 skips, got %d", len(skipped))
	}
	if len(updated) != 1 {
		t.Fatalf("expected 1 client updated, got %d", len(updated))
	}
}

func TestWriteClientConfigsSpecificClient(t *testing.T) {
	isolateWindowsUsers(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".cursor"), 0o755)
	server := clientconfig.ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}}
	updated, skipped, err := WriteClientConfigs("test-server", server, []string{"cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("expected 0 skips, got %d", len(skipped))
	}
	if updated[0].ID != clientconfig.ClientCursor {
		t.Errorf("expected cursor, got %s", updated[0].ID)
	}
}

func TestWriteClientConfigsNoneDetected(t *testing.T) {
	// Use a temp HOME that has no client config directories. On WSL2,
	// Windows-side paths (e.g. /mnt/c/Users/...) may still be detected
	// because they exist on the real filesystem. This test verifies
	// that when NO clients are found at all, WriteClientConfigs errors.
	// If a client IS detected (WSL2 environment), we skip the assertion.
	t.Setenv("HOME", t.TempDir())
	isolateWindowsUsers(t)
	updated, _, err := WriteClientConfigs("test", clientconfig.ServerConfig{}, nil)
	if err != nil {
		// Good — no clients detected, got an error as expected.
		return
	}
	if len(updated) == 0 && err == nil {
		t.Fatal("expected error when no clients detected, got nil error with 0 updates")
	}
	// If clients were detected (WSL2 Windows-side configs), the test
	// environment can't isolate those — skip rather than fail.
	t.Skip("client detected via WSL2 Windows-side path; cannot isolate in this env")
}

func TestWriteClientConfigsMultiSelect(t *testing.T) {
	isolateWindowsUsers(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Set up both cursor and generic so both are detected.
	os.MkdirAll(filepath.Join(home, ".cursor"), 0o755)
	os.MkdirAll(filepath.Join(home, ".config", "mcp"), 0o755)
	server := clientconfig.ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}}
	updated, skipped, err := WriteClientConfigs("test-server", server, []string{"cursor", "generic"})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("expected 0 skips, got %d", len(skipped))
	}
	if len(updated) != 2 {
		t.Fatalf("expected 2 clients updated, got %d", len(updated))
	}
	// Verify both IDs are present
	ids := map[string]bool{}
	for _, c := range updated {
		ids[string(c.ID)] = true
	}
	if !ids["cursor"] || !ids["generic"] {
		t.Errorf("expected cursor+generic, got %v", ids)
	}
}

func TestWriteClientConfigsDesktopRemoteSkipped(t *testing.T) {
	isolateWindowsUsers(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "Claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "claude_desktop_config.json")
	orig := []byte(`{
  "preferences": {"quickEntryShortcut": "off"},
  "coworkUserFilesPath": "/tmp/cowork",
  "mcpServers": {
    "keep-me": {"command": "npx"}
  }
}
`)
	if err := os.WriteFile(path, orig, 0o644); err != nil {
		t.Fatal(err)
	}

	updated, skipped, err := WriteClientConfigs("com.invokera/world-time", clientconfig.ServerConfig{
		URL:  "https://invokera.com/r/world-time",
		Type: "http",
	}, []string{"claude-desktop"})
	if err != nil {
		t.Fatalf("skip must not be a write error: %v", err)
	}
	if len(updated) != 0 {
		t.Fatalf("Desktop remote must not be in updated, got %v", updated)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skip, got %d", len(skipped))
	}
	if skipped[0].Client.ID != clientconfig.ClientClaudeDesktop {
		t.Errorf("skipped id = %s", skipped[0].Client.ID)
	}
	if !strings.Contains(skipped[0].Reason, "Connectors") {
		t.Errorf("reason = %q", skipped[0].Reason)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(orig) {
		t.Fatal("Desktop file changed on remote skip")
	}
	if strings.Contains(string(after), "mcp-remote") {
		t.Fatal("Desktop JSON must not contain mcp-remote")
	}
}

func TestWriteClientConfigsDesktopStdioWrites(t *testing.T) {
	isolateWindowsUsers(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "Claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	updated, skipped, err := WriteClientConfigs("local-one", clientconfig.ServerConfig{
		Command: "python3",
		Args:    []string{"server.py"},
	}, []string{"claude-desktop"})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("stdio Desktop must not skip, got %v", skipped)
	}
	if len(updated) != 1 {
		t.Fatalf("updated = %d, want 1", len(updated))
	}
	servers, err := clientconfig.ReadServers(updated[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := servers["local-one"]; !ok {
		t.Fatal("stdio Desktop entry missing")
	}
}

func TestWriteClientConfigsCursorBothHomesAutoAndExplicit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}

	winRoot := isolateWindowsUsers(t)
	winPath := filepath.Join(winRoot, "chris", ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(winPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(winPath, []byte(`{"mcpServers":{"MCP_DOCKER":{"command":"docker","args":["mcp","gateway","run"]}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := clientconfig.ServerConfig{URL: "https://invokera.com/r/world-time", Type: "http"}
	updated, skipped, err := WriteClientConfigs("com.invokera/world-time", server, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Aider is stdio-only and will skip remote servers. Filter it out
	// from the skip check; every other client should accept remotes.
	var nonAiderSkipped []clientconfig.SkippedClient
	for _, s := range skipped {
		if s.Client.ID != clientconfig.ClientAider {
			nonAiderSkipped = append(nonAiderSkipped, s)
		}
	}
	if len(nonAiderSkipped) != 0 {
		t.Fatalf("cursor remotes must not skip: %v", nonAiderSkipped)
	}
	cursorHits := 0
	for _, c := range updated {
		if c.ID == clientconfig.ClientCursor {
			cursorHits++
		}
	}
	if cursorHits != 2 {
		t.Fatalf("auto mode cursor hits = %d, want 2 (%v)", cursorHits, updated)
	}

	updated, skipped, err = WriteClientConfigs("com.invokera/world-time", server, []string{"cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatal(skipped)
	}
	if len(updated) != 2 {
		t.Fatalf("--client cursor updated %d, want 2", len(updated))
	}
	names := map[string]bool{}
	for _, c := range updated {
		names[c.Name] = true
	}
	if !names["Cursor"] || !names["Cursor (Windows via WSL2)"] {
		t.Errorf("names = %v", names)
	}

	winServers, err := clientconfig.ReadServers(winPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := winServers["MCP_DOCKER"]; !ok {
		t.Fatal("Windows Cursor lost MCP_DOCKER")
	}
	if _, ok := winServers["com.invokera/world-time"]; !ok {
		t.Fatal("Windows Cursor missing world-time")
	}
}

func TestWriteClientConfigsClaudeCodeRemoteAndStdio(t *testing.T) {
	isolateWindowsUsers(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte(`{"userID":"u1","machineID":"m1"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	updated, skipped, err := WriteClientConfigs("com.invokera/world-time", clientconfig.ServerConfig{
		URL:  "https://invokera.com/r/world-time",
		Type: "http",
	}, []string{"claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 || len(updated) != 1 {
		t.Fatalf("updated=%d skipped=%d", len(updated), len(skipped))
	}
	servers, err := clientconfig.ReadServers(path)
	if err != nil {
		t.Fatal(err)
	}
	var entry map[string]any
	if err := json.Unmarshal(servers["com.invokera/world-time"], &entry); err != nil {
		t.Fatal(err)
	}
	if entry["type"] != "http" || entry["url"] != "https://invokera.com/r/world-time" {
		t.Errorf("remote entry = %v", entry)
	}

	if _, _, err := WriteClientConfigs("local-one", clientconfig.ServerConfig{
		Command: "python3",
		Args:    []string{"server.py"},
	}, []string{"claude-code"}); err != nil {
		t.Fatal(err)
	}
	servers, err = clientconfig.ReadServers(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(servers["local-one"], &entry); err != nil {
		t.Fatal(err)
	}
	if entry["type"] != "stdio" || entry["command"] != "python3" {
		t.Errorf("stdio entry = %v", entry)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root["userID"]; !ok {
		t.Fatal("userID dropped")
	}
}

func kind2EchoManifest() api.Manifest {
	return api.Manifest{
		Name:      "test-echo-server",
		Version:   "0.2.6",
		Transport: "http-sse",
		Bin:       "python server.py",
	}
}

func TestBuildClientConfigKind2LocalhostURLNoCommand(t *testing.T) {
	m := kind2EchoManifest()
	cfg := BuildClientConfig(m, "/store")
	if cfg.URL != "http://127.0.0.1:8765" {
		t.Errorf("kind 2 client URL = %q, want http://127.0.0.1:8765", cfg.URL)
	}
	if cfg.Type != "http" {
		t.Errorf("kind 2 client type = %q, want http (http-sse is not EventSource)", cfg.Type)
	}
	if cfg.Command != "" {
		t.Errorf("kind 2 client must not have command, got %q", cfg.Command)
	}
	if len(cfg.Args) != 0 {
		t.Errorf("kind 2 client must not have args, got %v", cfg.Args)
	}
}

func TestBuildServerConfigKind2LaunchKeepsCommandNotURL(t *testing.T) {
	m := kind2EchoManifest()
	cfg := BuildServerConfig(m, "/store")
	if cfg.URL != "" {
		t.Errorf("kind 2 launch config must not set publisher/localhost URL, got %q", cfg.URL)
	}
	if cfg.Command == "" && len(cfg.Args) == 0 {
		t.Fatal("kind 2 launch config must keep command/args so the daemon can spawn")
	}
	if cfg.Command == "npx" {
		t.Fatal("kind 2 launch must not fall back to npx")
	}
}

func TestBuildClientConfigKind1KeepsPublisherURL(t *testing.T) {
	m := api.Manifest{
		Name:      "com.invokera/world-time",
		Version:   "1.0.0",
		Transport: "streamable-http",
		Endpoint:  "https://invokera.com/r/world-time",
		Bin:       "should-not-become-localhost",
	}
	cfg := BuildClientConfig(m, "/store")
	if cfg.URL != "https://invokera.com/r/world-time" {
		t.Errorf("kind 1 client URL = %q, want publisher endpoint", cfg.URL)
	}
	if strings.Contains(cfg.URL, "127.0.0.1") {
		t.Fatal("kind 1 must never be rewritten to localhost")
	}
	if cfg.Command != "" {
		t.Errorf("kind 1 client must be URL-only, command=%q", cfg.Command)
	}
}

func TestBuildClientConfigKind3StdioNoURL(t *testing.T) {
	m := api.Manifest{
		Transport: "stdio",
		Runtime:   "npx",
		Package:   "@scope/server",
	}
	cfg := BuildClientConfig(m, "/store")
	if cfg.URL != "" {
		t.Errorf("kind 3 client must not have URL, got %q", cfg.URL)
	}
	if cfg.Command != "npx" {
		t.Errorf("kind 3 client command = %q, want npx", cfg.Command)
	}
}

func TestBuildClientConfigKind2PythonBinIsHTTPNotSSE(t *testing.T) {
	m := kind2PythonBinManifest()
	cfg := BuildClientConfig(m, "/store")
	if cfg.URL != "http://127.0.0.1:8765" {
		t.Errorf("kind 2 client URL = %q, want http://127.0.0.1:8765", cfg.URL)
	}
	if cfg.Type != "http" {
		t.Errorf("kind 2 http-sse client type = %q, want http", cfg.Type)
	}
	if cfg.Command != "" {
		t.Errorf("kind 2 client must not have command, got %q", cfg.Command)
	}
	if len(cfg.Args) != 0 {
		t.Errorf("kind 2 client must not have args, got %v", cfg.Args)
	}
}

func TestBuildClientConfigHTTPSseNeverWritesTypeSSE(t *testing.T) {
	for _, tr := range []string{"http-sse", "http+sse", "streamable-http", "http"} {
		m := api.Manifest{
			Name:      "local-http",
			Version:   "1.0.0",
			Transport: tr,
			Bin:       "python server.py",
		}
		cfg := BuildClientConfig(m, "/store")
		if cfg.Type == "sse" {
			t.Errorf("transport %q must not produce client Type sse", tr)
		}
		if cfg.Type != "http" {
			t.Errorf("transport %q client type = %q, want http", tr, cfg.Type)
		}
	}
}

func TestBuildClientConfigKind2ExactSSEStaysSSE(t *testing.T) {
	m := api.Manifest{
		Name:      "local-sse",
		Version:   "1.0.0",
		Transport: "sse",
		Bin:       "python server.py",
	}
	cfg := BuildClientConfig(m, "/store")
	if cfg.Type != "sse" {
		t.Errorf("exact transport sse client type = %q, want sse", cfg.Type)
	}
	if cfg.URL != "http://127.0.0.1:8765" {
		t.Errorf("kind 2 sse URL = %q", cfg.URL)
	}
}

func TestBuildClientConfigKind3NpxNoURL(t *testing.T) {
	m := api.Manifest{
		Transport: "stdio",
		Runtime:   "npx",
		Package:   "@j0hanz/filesystem-mcp",
	}
	cfg := BuildClientConfig(m, "/store")
	if cfg.URL != "" {
		t.Errorf("kind 3 npx must not have URL, got %q", cfg.URL)
	}
	if cfg.Command != "npx" {
		t.Errorf("kind 3 command = %q, want npx", cfg.Command)
	}
	if len(cfg.Args) != 2 || cfg.Args[0] != "-y" || cfg.Args[1] != "@j0hanz/filesystem-mcp" {
		t.Errorf("kind 3 args = %v", cfg.Args)
	}
}

func TestBuildClientConfigKind2ExtractsListenPort(t *testing.T) {
	m := api.Manifest{
		Name:      "local-http",
		Version:   "1.0.0",
		Transport: "http",
		Bin:       "python server.py --port 9001",
	}
	cfg := BuildClientConfig(m, "/store")
	if cfg.URL != "http://127.0.0.1:9001" {
		t.Errorf("kind 2 client URL = %q, want port extracted from bin", cfg.URL)
	}
}

func TestWriteClientConfigsDesktopKind2URLSkipped(t *testing.T) {
	isolateWindowsUsers(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "Claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "claude_desktop_config.json")
	orig := []byte(`{"mcpServers":{"keep-me":{"command":"npx"}}}` + "\n")
	if err := os.WriteFile(path, orig, 0o644); err != nil {
		t.Fatal(err)
	}

	clientCfg := BuildClientConfig(kind2EchoManifest(), "/store")
	updated, skipped, err := WriteClientConfigs("test-echo-server", clientCfg, []string{"claude-desktop"})
	if err != nil {
		t.Fatalf("Desktop kind 2 skip must not be a write error: %v", err)
	}
	if len(updated) != 0 {
		t.Fatalf("Desktop + kind 2 URL must not be in updated, got %v", updated)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skip, got %d", len(skipped))
	}
	if skipped[0].Client.ID != clientconfig.ClientClaudeDesktop {
		t.Errorf("skipped id = %s", skipped[0].Client.ID)
	}
	if !strings.Contains(skipped[0].Reason, "Connectors") {
		t.Errorf("reason = %q", skipped[0].Reason)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(orig) {
		t.Fatal("Desktop file changed on kind 2 URL skip")
	}
	if strings.Contains(string(after), "127.0.0.1") || strings.Contains(string(after), "python") {
		t.Fatal("Desktop JSON must not contain localhost URL or python launch line")
	}
}

func TestWriteClientConfigsCursorKind2LocalhostURL(t *testing.T) {
	isolateWindowsUsers(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}

	clientCfg := BuildClientConfig(kind2EchoManifest(), "/store")
	updated, skipped, err := WriteClientConfigs("test-echo-server", clientCfg, []string{"cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("Cursor kind 2 must not skip: %v", skipped)
	}
	if len(updated) != 1 {
		t.Fatalf("updated = %d, want 1", len(updated))
	}

	servers, err := clientconfig.ReadServers(updated[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := servers["test-echo-server"]
	if !ok {
		t.Fatal("Cursor missing test-echo-server")
	}
	var entry map[string]any
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatal(err)
	}
	if entry["url"] != "http://127.0.0.1:8765" {
		t.Errorf("Cursor url = %v, want http://127.0.0.1:8765", entry["url"])
	}
	if entry["type"] != "http" {
		t.Errorf("Cursor type = %v, want http (http-sse must not write sse)", entry["type"])
	}
	if _, hasCmd := entry["command"]; hasCmd {
		t.Errorf("Cursor kind 2 must not write command, entry=%v", entry)
	}
}

// TestUpdateLockfileRecordsWrittenClients pins the per-server Clients
// record (additive lockfile field): fresh installs record the clients
// actually written to, re-installs merge the set without duplicates or
// loss, empty write lists keep the previous record, and legacy entries
// (no Clients) stay legacy when nothing new is written.
func TestUpdateLockfileRecordsWrittenClients(t *testing.T) {
	isolateWindowsUsers(t)
	t.Setenv("HOME", t.TempDir())
	lockPath := filepath.Join(t.TempDir(), "pharos.lock")
	res := &InstallResult{Name: "srv", Version: "1.0.0", Transport: "stdio"}

	// Fresh install to two clients — record both, sorted.
	if err := UpdateLockfile(lockPath, res, "https://x/srv.tgz", []string{"generic", "cursor"}); err != nil {
		t.Fatal(err)
	}
	lf, err := lockfile.Load(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := lf.Get("srv")
	if !ok {
		t.Fatal("srv missing from lockfile after install")
	}
	if want := []string{"cursor", "generic"}; !reflect.DeepEqual(entry.Clients, want) {
		t.Errorf("Clients = %v, want %v", entry.Clients, want)
	}

	// Re-install to a subset must merge (not lose the other client).
	if err := UpdateLockfile(lockPath, res, "https://x/srv.tgz", []string{"cursor"}); err != nil {
		t.Fatal(err)
	}
	lf, _ = lockfile.Load(lockPath)
	entry, _ = lf.Get("srv")
	if want := []string{"cursor", "generic"}; !reflect.DeepEqual(entry.Clients, want) {
		t.Errorf("Clients after subset re-install = %v, want merged %v", entry.Clients, want)
	}

	// Re-install adding a new client extends the set, deduped: previous
	// order preserved, new IDs appended sorted.
	if err := UpdateLockfile(lockPath, res, "https://x/srv.tgz", []string{"cursor", "aider", "cursor"}); err != nil {
		t.Fatal(err)
	}
	lf, _ = lockfile.Load(lockPath)
	entry, _ = lf.Get("srv")
	if want := []string{"cursor", "generic", "aider"}; !reflect.DeepEqual(entry.Clients, want) {
		t.Errorf("Clients after extending re-install = %v, want %v", entry.Clients, want)
	}

	// An update with no client writes keeps the previous record.
	if err := UpdateLockfile(lockPath, res, "https://x/srv.tgz", nil); err != nil {
		t.Fatal(err)
	}
	lf, _ = lockfile.Load(lockPath)
	entry, _ = lf.Get("srv")
	if want := []string{"cursor", "generic", "aider"}; !reflect.DeepEqual(entry.Clients, want) {
		t.Errorf("Clients after no-write update = %v, want preserved %v", entry.Clients, want)
	}

	// Legacy entry without a Clients record stays legacy on update.
	legacy := lockfile.New()
	legacy.Set("old", lockfile.ServerEntry{Version: "0.9.0", InstalledAt: time.Now().UTC()})
	if err := legacy.Save(lockPath); err != nil {
		t.Fatal(err)
	}
	if err := UpdateLockfile(lockPath, &InstallResult{Name: "old", Version: "0.9.0", Transport: "stdio"}, "", nil); err != nil {
		t.Fatal(err)
	}
	lf, _ = lockfile.Load(lockPath)
	entry, _ = lf.Get("old")
	if len(entry.Clients) != 0 {
		t.Errorf("legacy entry gained Clients %v, want none", entry.Clients)
	}
}
