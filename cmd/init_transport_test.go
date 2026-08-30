package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/manifest"
)

// TestInitTransportChoices verifies the init transport selector offers every
// transport variant the install/kind pipeline accepts (see
// 终结 internal/install/kind.go:121); regression guard for issue #19.
func TestInitTransportChoices(t *testing.T) {
	want := []string{"stdio", "http-sse", "streamable-http"}
	if !reflect.DeepEqual(initTransportChoices, want) {
		t.Errorf("initTransportChoices = %v, want %v", initTransportChoices, want)
	}
}

// TestInitYesDefaultTransportUnchanged ensures --yes still yields stdio.
func TestInitYesDefaultTransportUnchanged(t *testing.T) {
	m := buildManifestYesDefaults()
	if m.Transport != "stdio" {
		t.Errorf("--yes Transport = %q, want stdio", m.Transport)
	}
}

// TestInitStreamableHTTPFilePersist writes a streamable-http manifest via
// writeManifest and proves it lands in pharos.json (360° round trip).
func TestInitStreamableHTTPFilePersist(t *testing.T) {
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(prev); err != nil {
			t.Fatal(err)
		}
	}()

	m := &manifest.Manifest{
		Name:      "x",
		Version:   "0.1.0",
		Transport: "streamable-http",
	}
	writeManifest(m)

	data, err := os.ReadFile(filepath.Join(dir, "pharos.json"))
	if err != nil {
		t.Fatalf("read pharos.json: %v", err)
	}
	if !containsTransport(data, "streamable-http") {
		t.Errorf("pharos.json = %s", data)
	}
}

// containsTransport decodes JSON and reports whether "transport" == want.
func containsTransport(data []byte, want string) bool {
	var kv struct {
		Transport string `json:"transport"`
	}
	if err := json.Unmarshal(data, &kv); err != nil {
		return false
	}
	return kv.Transport == want
}
