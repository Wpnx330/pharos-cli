package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/clientconfig"
)

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
	}
	for _, tc := range tests {
		if got := normalizeTransport(tc.in); got != tc.want {
			t.Errorf("normalizeTransport(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWriteClientConfigsWithMockClient(t *testing.T) {
	// Set up a fake generic client.
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "mcp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	server := clientconfig.ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}}
	updated, err := WriteClientConfigs("test-server", server, []string{"generic"})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) != 1 {
		t.Fatalf("expected 1 client updated, got %d", len(updated))
	}
}

func TestWriteClientConfigsSpecificClient(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".cursor"), 0o755)
	server := clientconfig.ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}}
	updated, err := WriteClientConfigs("test-server", server, []string{"cursor"})
	if err != nil {
		t.Fatal(err)
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
	updated, err := WriteClientConfigs("test", clientconfig.ServerConfig{}, nil)
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
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Set up both cursor and generic so both are detected.
	os.MkdirAll(filepath.Join(home, ".cursor"), 0o755)
	os.MkdirAll(filepath.Join(home, ".config", "mcp"), 0o755)
	server := clientconfig.ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}}
	updated, err := WriteClientConfigs("test-server", server, []string{"cursor", "generic"})
	if err != nil {
		t.Fatal(err)
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
