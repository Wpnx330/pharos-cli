package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// TestCreateTarballPacksDirectory verifies that when a directory entry is
// passed in the files list, all files within it are recursively packed
// into the tarball (not silently skipped).
func TestCreateTarballPacksDirectory(t *testing.T) {
	srcDir := t.TempDir()

	// Create a subdirectory with nested files.
	subDir := filepath.Join(srcDir, "lib")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create lib/server.py
	if err := os.WriteFile(filepath.Join(subDir, "server.py"), []byte("print('hello')"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create lib/utils/helper.py (deeper nesting)
	utilsDir := filepath.Join(subDir, "utils")
	if err := os.MkdirAll(utilsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(utilsDir, "helper.py"), []byte("def help(): pass"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create a top-level file too.
	if err := os.WriteFile(filepath.Join(srcDir, "pharos.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pack: pharos.json + lib (a directory)
	tarballPath := filepath.Join(srcDir, "test.tgz")
	files := []string{"pharos.json", "lib"}
	if err := createTarball(tarballPath, srcDir, files); err != nil {
		t.Fatalf("createTarball failed: %v", err)
	}

	// Read back the tarball and verify contents.
	data, err := os.ReadFile(tarballPath)
	if err != nil {
		t.Fatal(err)
	}

	gzr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	found := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		found[hdr.Name] = true
	}

	// We expect pharos.json, lib/server.py, lib/utils/helper.py
	expected := []string{"pharos.json", "lib/server.py", "lib/utils/helper.py"}
	for _, e := range expected {
		if !found[e] {
			t.Errorf("expected %s in tarball, not found. Got: %v", e, found)
		}
	}
}

// TestCreateTarballPacksFiles verifies that regular (non-directory) files
// are still packed correctly.
func TestCreateTarballPacksFiles(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("aaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "b.txt"), []byte("bbb"), 0o644); err != nil {
		t.Fatal(err)
	}

	tarballPath := filepath.Join(srcDir, "test.tgz")
	files := []string{"a.txt", "b.txt"}
	if err := createTarball(tarballPath, srcDir, files); err != nil {
		t.Fatalf("createTarball failed: %v", err)
	}

	data, err := os.ReadFile(tarballPath)
	if err != nil {
		t.Fatal(err)
	}

	gzr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	found := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		found[hdr.Name] = true
	}

	if !found["a.txt"] || !found["b.txt"] {
		t.Errorf("expected a.txt and b.txt in tarball, got: %v", found)
	}
}
