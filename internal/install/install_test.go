package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
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

// TestExtract verifies that Extract correctly unpacks a tarball.
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

// TestExtractPathTraversal verifies that path traversal is rejected.
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

// TestListEmpty verifies that List returns nil with no installed packages.
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

// TestIsInstalled verifies detection of installed packages.
func TestIsInstalled(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)
	if mgr.IsInstalled("test") {
		t.Error("should not be installed initially")
	}
	// Create metadata
	pkg := &InstalledPackage{Name: "test", Version: "1.0.0"}
	if err := mgr.saveMetadata("test", pkg); err != nil {
		t.Fatal(err)
	}
	if !mgr.IsInstalled("test") {
		t.Error("should be installed after metadata write")
	}
}

// TestListWithPackage verifies List finds installed packages.
func TestListWithPackage(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)
	pkg := &InstalledPackage{Name: "test", Version: "1.0.0", Location: "/some/path"}
	if err := mgr.saveMetadata("test", pkg); err != nil {
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
