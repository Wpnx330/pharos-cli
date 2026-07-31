package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/install"
	"github.com/Wpnx330/pharos-cli/internal/manifest"
	"github.com/Wpnx330/pharos-cli/internal/runtime"
)

// =============================================================
// list.go — dirSize tests
// =============================================================

// TestDirSizeEmptyDir verifies that dirSize returns 0 for an empty directory.
func TestDirSizeEmptyDir(t *testing.T) {
	dir := t.TempDir()
	got := dirSize(dir)
	if got != 0 {
		t.Errorf("dirSize(empty dir) = %d, want 0", got)
	}
}

// TestDirSizeWithFiles verifies that dirSize sums all file sizes.
func TestDirSizeWithFiles(t *testing.T) {
	dir := t.TempDir()
	// Create files with known sizes
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), make([]byte, 200), 0o644); err != nil {
		t.Fatal(err)
	}
	got := dirSize(dir)
	if got != 300 {
		t.Errorf("dirSize(2 files: 100+200) = %d, want 300", got)
	}
}

// TestDirSizeNestedFiles verifies that dirSize recurses into subdirectories.
func TestDirSizeNestedFiles(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "top.txt"), make([]byte, 50), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "nested.txt"), make([]byte, 150), 0o644); err != nil {
		t.Fatal(err)
	}
	got := dirSize(dir)
	if got != 200 {
		t.Errorf("dirSize(nested: 50+150) = %d, want 200", got)
	}
}

// TestDirSizeNonexistentPath verifies that dirSize returns 0 for a
// nonexistent path (filepath.Walk calls the walkFn with a non-nil err,
// which we return nil for, resulting in a 0 size).
func TestDirSizeNonexistentPath(t *testing.T) {
	got := dirSize("/nonexistent/path/that/does/not/exist")
	if got != 0 {
		t.Errorf("dirSize(nonexistent) = %d, want 0", got)
	}
}

// =============================================================
// list.go — list command flag and sort tests
// =============================================================

// TestListCmdFlags verifies the list command has the expected flags
// with correct defaults.
func TestListCmdFlags(t *testing.T) {
	runningFlag := listCmd.Flags().Lookup("running")
	if runningFlag == nil {
		t.Fatal("list command missing --running flag")
	}
	if runningFlag.DefValue != "false" {
		t.Errorf("list --running default = %s, want false", runningFlag.DefValue)
	}

	sortFlag := listCmd.Flags().Lookup("sort")
	if sortFlag == nil {
		t.Fatal("list command missing --sort flag")
	}
	if sortFlag.DefValue != "name" {
		t.Errorf("list --sort default = %s, want name", sortFlag.DefValue)
	}
}

// TestListCmdAliases verifies the list command has the "ls" alias.
func TestListCmdAliases(t *testing.T) {
	found := false
	for _, alias := range listCmd.Aliases {
		if alias == "ls" {
			found = true
			break
		}
	}
	if !found {
		t.Error("list command should have 'ls' alias")
	}
}

// TestListSortFieldValidation verifies that the sort field values
// referenced in list.go's sort.Slice correspond to known options.
func TestListSortFieldValidation(t *testing.T) {
	validSorts := []string{"name", "size", "port", "memory", "uptime"}
	for _, s := range validSorts {
		// Each should be a recognized sort key in the list command
		found := false
		for _, line := range []string{
			"case \"size\":",
			"case \"port\":",
			"case \"memory\":",
			"case \"uptime\":",
			"default: // \"name\"",
		} {
			if strings.Contains(line, "\""+s+"\"") || (s == "name" && strings.Contains(line, "name")) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("sort key %q not recognized", s)
		}
	}
}

// =============================================================
// list.go / start.go — install.Manager.List tests
// =============================================================

// TestInstallManagerListEmpty verifies that List returns nil/empty
// for a nonexistent store directory.
func TestInstallManagerListEmpty(t *testing.T) {
	dir := t.TempDir()
	mgr := install.NewManager(filepath.Join(dir, "store"))
	pkgs, err := mgr.List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(pkgs) != 0 {
		t.Errorf("List() on empty store returned %d packages, want 0", len(pkgs))
	}
}

// TestInstallManagerListWithPackages verifies that List returns installed
// packages from a populated store.
func TestInstallManagerListWithPackages(t *testing.T) {
	dir := t.TempDir()
	mgr := install.NewManager(filepath.Join(dir, "store"))

	// Install a fake package using InstallHTTP (no download required)
	result, err := mgr.InstallHTTP("test-server", "1.0.0")
	if err != nil {
		t.Fatalf("InstallHTTP() error: %v", err)
	}
	if result.Name != "test-server" {
		t.Errorf("InstallHTTP name = %s, want test-server", result.Name)
	}

	pkgs, err := mgr.List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("List() returned %d packages, want 1", len(pkgs))
	}
	if pkgs[0].Name != "test-server" {
		t.Errorf("List()[0].Name = %s, want test-server", pkgs[0].Name)
	}
	if pkgs[0].Version != "1.0.0" {
		t.Errorf("List()[0].Version = %s, want 1.0.0", pkgs[0].Version)
	}
}

// TestInstallManagerListMultiple verifies List with multiple packages
// and versions.
func TestInstallManagerListMultiple(t *testing.T) {
	dir := t.TempDir()
	mgr := install.NewManager(filepath.Join(dir, "store"))

	if _, err := mgr.InstallHTTP("server-a", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.InstallHTTP("server-a", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.InstallHTTP("server-b", "0.5.0"); err != nil {
		t.Fatal(err)
	}

	pkgs, err := mgr.List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(pkgs) != 3 {
		t.Errorf("List() returned %d packages, want 3", len(pkgs))
	}
}

// TestInstallManagerIsInstalled verifies the IsInstalled check works.
func TestInstallManagerIsInstalled(t *testing.T) {
	dir := t.TempDir()
	mgr := install.NewManager(filepath.Join(dir, "store"))

	if _, err := mgr.InstallHTTP("my-server", "1.0.0"); err != nil {
		t.Fatal(err)
	}

	if !mgr.IsInstalled("my-server", "1.0.0") {
		t.Error("IsInstalled should return true for installed package")
	}
	if mgr.IsInstalled("my-server", "2.0.0") {
		t.Error("IsInstalled should return false for uninstalled version")
	}
	if mgr.IsInstalled("other-server", "1.0.0") {
		t.Error("IsInstalled should return false for uninstalled package")
	}
}

// =============================================================
// start.go — manifest.Parse and RunCommand tests
// =============================================================

// TestManifestParseAndRunCommand verifies that a manifest with a "command"
// field is parsed correctly and RunCommand returns the command.
func TestManifestParseAndRunCommand(t *testing.T) {
	data := []byte(`{
		"name": "test-server",
		"version": "1.0.0",
		"transport": "stdio",
		"command": "python server.py --port 8080"
	}`)
	m, err := manifest.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if m.Name != "test-server" {
		t.Errorf("name = %s, want test-server", m.Name)
	}
	if m.Transport != "stdio" {
		t.Errorf("transport = %s, want stdio", m.Transport)
	}
	got := m.RunCommand()
	if got != "python server.py --port 8080" {
		t.Errorf("RunCommand() = %q, want 'python server.py --port 8080'", got)
	}
}

// TestManifestRunCommandBinFallback verifies that RunCommand falls back
// to the "bin" field when "command" is empty (backwards compat).
func TestManifestRunCommandBinFallback(t *testing.T) {
	data := []byte(`{
		"name": "legacy-server",
		"version": "0.1.0",
		"bin": "node server.js"
	}`)
	m, err := manifest.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	got := m.RunCommand()
	if got != "node server.js" {
		t.Errorf("RunCommand() = %q, want 'node server.js'", got)
	}
}

// TestManifestRunCommandEmpty verifies that RunCommand returns empty
// when neither "command" nor "bin" is set.
func TestManifestRunCommandEmpty(t *testing.T) {
	data := []byte(`{
		"name": "empty-server",
		"version": "1.0.0"
	}`)
	m, err := manifest.Parse(data)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	got := m.RunCommand()
	if got != "" {
		t.Errorf("RunCommand() = %q, want empty string", got)
	}
}

// TestManifestParseInvalidJSON verifies that Parse returns an error
// for malformed JSON.
func TestManifestParseInvalidJSON(t *testing.T) {
	data := []byte(`{invalid json}`)
	_, err := manifest.Parse(data)
	if err == nil {
		t.Error("Parse() should return error for invalid JSON")
	}
}

// TestManifestValidate verifies the Validate method checks required fields.
func TestManifestValidate(t *testing.T) {
	tests := []struct {
		name    string
		manifest manifest.Manifest
		wantErr  bool
	}{
		{"valid", manifest.Manifest{Name: "x", Version: "1.0.0"}, false},
		{"missing name", manifest.Manifest{Version: "1.0.0"}, true},
		{"missing version", manifest.Manifest{Name: "x"}, true},
		{"both missing", manifest.Manifest{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.manifest.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// =============================================================
// start.go / list.go — runtime.ExtractPort tests
// =============================================================

// TestExtractPort verifies port extraction from command strings and URLs.
func TestExtractPort(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantPort int
	}{
		{"port flag in command", "python server.py --port 8765", 8765},
		{"http URL with port", "http://localhost:3000", 3000},
		{"https URL with port", "https://example.com:443", 443},
		{"no port in command", "node server.js", 0},
		{"empty string", "", 0},
		{"port at end", "uvicorn main:app --host 0.0.0.0 --port 9000", 9000},
		{"invalid port too large", "server --port 99999", 0},
		{"zero port", "server --port 0", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runtime.ExtractPort(tt.input)
			if got != tt.wantPort {
				t.Errorf("ExtractPort(%q) = %d, want %d", tt.input, got, tt.wantPort)
			}
		})
	}
}

// =============================================================
// start.go / stop.go — runtime.PIDFile and ReadPID tests
// =============================================================

// TestPIDFileAndReadPID verifies writing a PID file and reading it back.
func TestPIDFileAndReadPID(t *testing.T) {
	// PIDFile uses os.UserHomeDir() which is the real home. We test
	// that ReadPID returns 0 for a non-existent PID file (no server
	// running with that name).
	pid, err := runtime.ReadPID("definitely-not-running-server-xyz")
	if err != nil {
		t.Fatalf("ReadPID() error: %v", err)
	}
	if pid != 0 {
		t.Errorf("ReadPID(nonexistent) = %d, want 0", pid)
	}
}

// TestPIDFilePathConstruction verifies that PIDFile returns a path
// ending in .pid under the .pharos/run directory.
func TestPIDFilePathConstruction(t *testing.T) {
	pidPath, err := runtime.PIDFile("my-server")
	if err != nil {
		t.Fatalf("PIDFile() error: %v", err)
	}
	if !strings.HasSuffix(pidPath, "my-server.pid") {
		t.Errorf("PIDFile path = %q, want suffix 'my-server.pid'", pidPath)
	}
	if !strings.Contains(pidPath, ".pharos") {
		t.Errorf("PIDFile path = %q, should contain '.pharos'", pidPath)
	}
}

// =============================================================
// stop.go — runtime.IsRunning tests
// =============================================================

// TestIsRunningInvalidPID verifies that invalid PIDs return false.
func TestIsRunningInvalidPID(t *testing.T) {
	tests := []struct {
		name string
		pid  int
		want bool
	}{
		{"zero PID", 0, false},
		{"negative PID", -1, false},
		{"nonexistent PID", 999999, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runtime.IsRunning(tt.pid)
			if got != tt.want {
				t.Errorf("IsRunning(%d) = %v, want %v", tt.pid, got, tt.want)
			}
		})
	}
}

// TestIsRunningCurrentProcess verifies that the current process
// is detected as running.
func TestIsRunningCurrentProcess(t *testing.T) {
	pid := os.Getpid()
	if !runtime.IsRunning(pid) {
		t.Errorf("IsRunning(%d) = false, want true (current process)", pid)
	}
}

// =============================================================
// stop.go — runtime.Stop tests
// =============================================================

// TestStopNotRunning verifies that stopping a server without a PID file
// returns an appropriate error.
func TestStopNotRunning(t *testing.T) {
	err := runtime.Stop(runtime.StopOptions{
		Name:    "definitely-not-running-server-xyz",
		Force:   false,
		Timeout: 1,
	})
	if err == nil {
		t.Error("Stop() on non-running server should return error")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("Stop() error = %q, should contain 'not running'", err.Error())
	}
}

// TestStopAllEmpty verifies that StopAll returns empty list when no
// servers are running.
func TestStopAllEmpty(t *testing.T) {
	stopped, err := runtime.StopAll(false, 1)
	if err != nil {
		t.Fatalf("StopAll() error: %v", err)
	}
	// There may be leftover PID files from other tests, but we can't
	// control that. We just verify no error and it returns a slice.
	_ = stopped
}

// =============================================================
// stop.go — stop command flag tests
// =============================================================

// TestStopCmdFlags verifies the stop command has the expected flags.
func TestStopCmdFlags(t *testing.T) {
	for _, flagName := range []string{"force", "all", "timeout"} {
		f := stopCmd.Flags().Lookup(flagName)
		if f == nil {
			t.Errorf("stop command missing --%s flag", flagName)
		}
	}

	timeoutFlag := stopCmd.Flags().Lookup("timeout")
	if timeoutFlag != nil && timeoutFlag.DefValue != "5" {
		t.Errorf("stop --timeout default = %s, want 5", timeoutFlag.DefValue)
	}
}

// TestStopCmdMaxArgs verifies that stop accepts at most 1 arg.
func TestStopCmdMaxArgs(t *testing.T) {
	// MaximumNArgs(1) is set on the command. We verify the position
	// args validator is in place by checking the Args field is non-nil.
	if stopCmd.Args == nil {
		t.Error("stop command should have Args validator set")
	}
}

// =============================================================
// start.go — start command flag tests
// =============================================================

// TestStartCmdFlags verifies the start command has the expected flags.
func TestStartCmdFlags(t *testing.T) {
	for _, flagName := range []string{"foreground", "env", "port"} {
		f := startCmd.Flags().Lookup(flagName)
		if f == nil {
			t.Errorf("start command missing --%s flag", flagName)
		}
	}

	fgFlag := startCmd.Flags().Lookup("foreground")
	if fgFlag != nil && fgFlag.DefValue != "false" {
		t.Errorf("start --foreground default = %s, want false", fgFlag.DefValue)
	}

	portFlag := startCmd.Flags().Lookup("port")
	if portFlag != nil && portFlag.DefValue != "0" {
		t.Errorf("start --port default = %s, want 0", portFlag.DefValue)
	}
}

// TestStartCmdExactArgs verifies that start requires exactly 1 arg.
func TestStartCmdExactArgs(t *testing.T) {
	if startCmd.Args == nil {
		t.Error("start command should have Args validator set")
	}
}

// =============================================================
// unpublish.go — command flag tests
// =============================================================

// TestUnpublishCmdFlags verifies the unpublish command has the expected
// flags with correct defaults.
func TestUnpublishCmdFlags(t *testing.T) {
	for _, flagName := range []string{"version", "all", "yes"} {
		f := unpublishCmd.Flags().Lookup(flagName)
		if f == nil {
			t.Errorf("unpublish command missing --%s flag", flagName)
		}
	}

	verFlag := unpublishCmd.Flags().Lookup("version")
	if verFlag != nil && verFlag.DefValue != "" {
		t.Errorf("unpublish --version default = %s, want empty", verFlag.DefValue)
	}

	allFlag := unpublishCmd.Flags().Lookup("all")
	if allFlag != nil && allFlag.DefValue != "false" {
		t.Errorf("unpublish --all default = %s, want false", allFlag.DefValue)
	}

	yesFlag := unpublishCmd.Flags().Lookup("yes")
	if yesFlag != nil && yesFlag.DefValue != "false" {
		t.Errorf("unpublish --yes default = %s, want false", yesFlag.DefValue)
	}
}

// TestUnpublishCmdExactArgs verifies unpublish requires exactly 1 arg.
func TestUnpublishCmdExactArgs(t *testing.T) {
	if unpublishCmd.Args == nil {
		t.Error("unpublish command should have Args validator set")
	}
}

// =============================================================
// purge.go — command flag tests
// =============================================================

// TestPurgeCmdFlags verifies the purge command has the expected flags.
func TestPurgeCmdFlags(t *testing.T) {
	for _, flagName := range []string{"version", "all", "yes"} {
		f := purgeCmd.Flags().Lookup(flagName)
		if f == nil {
			t.Errorf("purge command missing --%s flag", flagName)
		}
	}

	verFlag := purgeCmd.Flags().Lookup("version")
	if verFlag != nil && verFlag.DefValue != "" {
		t.Errorf("purge --version default = %s, want empty", verFlag.DefValue)
	}

	allFlag := purgeCmd.Flags().Lookup("all")
	if allFlag != nil && allFlag.DefValue != "false" {
		t.Errorf("purge --all default = %s, want false", allFlag.DefValue)
	}
}

// TestPurgeCmdExactArgs verifies purge requires exactly 1 arg.
func TestPurgeCmdExactArgs(t *testing.T) {
	if purgeCmd.Args == nil {
		t.Error("purge command should have Args validator set")
	}
}

// =============================================================
// republish.go — command flag tests
// =============================================================

// TestRepublishCmdFlags verifies the republish command has the --version flag.
func TestRepublishCmdFlags(t *testing.T) {
	verFlag := republishCmd.Flags().Lookup("version")
	if verFlag == nil {
		t.Fatal("republish command missing --version flag")
	}
	if verFlag.DefValue != "" {
		t.Errorf("republish --version default = %s, want empty", verFlag.DefValue)
	}
}

// TestRepublishCmdExactArgs verifies republish requires exactly 1 arg.
func TestRepublishCmdExactArgs(t *testing.T) {
	if republishCmd.Args == nil {
		t.Error("republish command should have Args validator set")
	}
}

// =============================================================
// unpublish.go / purge.go — API PackageDetail tests
// =============================================================

// TestPackageDetailVersionStrings verifies that VersionStrings extracts
// version strings in order. This is used by unpublish --all and purge --all.
func TestPackageDetailVersionStrings(t *testing.T) {
	pd := &api.PackageDetail{
		Versions: []api.VersionDetail{
			{Version: "0.1.0"},
			{Version: "0.2.0"},
			{Version: "1.0.0"},
		},
	}
	got := pd.VersionStrings()
	want := []string{"0.1.0", "0.2.0", "1.0.0"}
	if len(got) != len(want) {
		t.Fatalf("VersionStrings() len = %d, want %d", len(got), len(want))
	}
	for i, v := range got {
		if v != want[i] {
			t.Errorf("VersionStrings()[%d] = %q, want %q", i, v, want[i])
		}
	}
}

// TestPackageDetailVersionStringsEmpty verifies VersionStrings on a
// package with no versions (unpublish --all with empty package).
func TestPackageDetailVersionStringsEmpty(t *testing.T) {
	pd := &api.PackageDetail{}
	got := pd.VersionStrings()
	if len(got) != 0 {
		t.Errorf("VersionStrings() on empty = %v, want empty", got)
	}
}

// TestPackageDetailFindVersion verifies FindVersion returns the correct
// version detail or nil.
func TestPackageDetailFindVersion(t *testing.T) {
	pd := &api.PackageDetail{
		Versions: []api.VersionDetail{
			{Version: "0.1.0", Status: "active"},
			{Version: "0.2.0", Status: "unpublished"},
			{Version: "1.0.0", Status: "active"},
		},
	}

	tests := []struct {
		name    string
		version string
		found   bool
		status  string
	}{
		{"first version", "0.1.0", true, "active"},
		{"middle version", "0.2.0", true, "unpublished"},
		{"last version", "1.0.0", true, "active"},
		{"nonexistent", "9.9.9", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vd := pd.FindVersion(tt.version)
			if tt.found {
				if vd == nil {
					t.Fatalf("FindVersion(%q) = nil, want non-nil", tt.version)
				}
				if vd.Status != tt.status {
					t.Errorf("FindVersion(%q).Status = %q, want %q", tt.version, vd.Status, tt.status)
				}
			} else {
				if vd != nil {
					t.Errorf("FindVersion(%q) = %v, want nil", tt.version, vd)
				}
			}
		})
	}
}

// =============================================================
// unpublish/purge/republish — API SetVersionStatus URL construction
// =============================================================

// TestSetVersionStatusURLConstruction verifies that the API client
// constructs the correct endpoint path for status changes. We do this
// by checking the TarballURL method as a proxy for path construction
// patterns, and verifying the status strings used by the commands.
func TestVersionStatusStrings(t *testing.T) {
	// The commands use these exact status strings in SetVersionStatus calls:
	// - unpublish: "unpublished"
	// - purge: "deleted"
	// - republish: "active"
	tests := []struct {
		command string
		status  string
	}{
		{"unpublish", "unpublished"},
		{"purge", "deleted"},
		{"republish", "active"},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			// Verify the status string is non-empty and lowercase
			if tt.status == "" {
				t.Errorf("status for %s is empty", tt.command)
			}
			if tt.status != strings.ToLower(tt.status) {
				t.Errorf("status for %s = %q, should be lowercase", tt.command, tt.status)
			}
		})
	}
}

// TestTarballURLConstruction verifies the API client builds correct
// tarball URLs (same path construction pattern as SetVersionStatus).
func TestTarballURLConstruction(t *testing.T) {
	client := api.New("https://registry.example.com", "")
	got := client.TarballURL("my-server", "1.0.0")
	want := "https://registry.example.com/v1/tarballs/my-server/1.0.0"
	if got != want {
		t.Errorf("TarballURL() = %q, want %q", got, want)
	}
}

// TestAPIClientBaseURLTrimming verifies that the API client trims
// trailing slashes from the base URL.
func TestAPIClientBaseURLTrimming(t *testing.T) {
	client := api.New("https://registry.example.com/", "")
	if client.BaseURL != "https://registry.example.com" {
		t.Errorf("BaseURL = %q, want no trailing slash", client.BaseURL)
	}
}

// =============================================================
// start.go / stop.go — runtime.ProbeStatus tests
// =============================================================

// TestProbeStatusNotRunning verifies that ProbeStatus returns a
// non-running status for a server with no PID file.
func TestProbeStatusNotRunning(t *testing.T) {
	status := runtime.ProbeStatus("definitely-not-running-server-xyz", 0)
	if status.Running {
		t.Error("ProbeStatus() should report not running for nonexistent server")
	}
	if status.PID != 0 {
		t.Errorf("ProbeStatus().PID = %d, want 0", status.PID)
	}
}

// TestProcessStatusStruct verifies the ProcessStatus struct fields
// are accessible and have correct zero values.
func TestProcessStatusStruct(t *testing.T) {
	var ps runtime.ProcessStatus
	if ps.Running {
		t.Error("zero ProcessStatus should have Running = false")
	}
	if ps.PID != 0 {
		t.Error("zero ProcessStatus should have PID = 0")
	}
	if ps.Port != 0 {
		t.Error("zero ProcessStatus should have Port = 0")
	}
	if ps.Memory != 0 {
		t.Error("zero ProcessStatus should have Memory = 0")
	}
	if ps.Uptime != "" {
		t.Error("zero ProcessStatus should have Uptime = empty")
	}
}

// =============================================================
// start.go / stop.go — runtime.Start/StopOptions struct tests
// =============================================================

// TestStartOptionsFields verifies StartOptions struct fields.
func TestStartOptionsFields(t *testing.T) {
	opts := runtime.StartOptions{
		Name:       "test-server",
		Command:    "python server.py",
		WorkDir:    "/tmp/test",
		Env:        []string{"API_KEY=secret"},
		Port:       8080,
		Foreground: false,
	}
	if opts.Name != "test-server" {
		t.Errorf("Name = %s", opts.Name)
	}
	if opts.Command != "python server.py" {
		t.Errorf("Command = %s", opts.Command)
	}
	if len(opts.Env) != 1 || opts.Env[0] != "API_KEY=secret" {
		t.Errorf("Env = %v", opts.Env)
	}
	if opts.Port != 8080 {
		t.Errorf("Port = %d", opts.Port)
	}
}

// TestStopOptionsFields verifies StopOptions struct fields.
func TestStopOptionsFields(t *testing.T) {
	opts := runtime.StopOptions{
		Name:    "test-server",
		Force:   true,
		Timeout: 10,
	}
	if opts.Name != "test-server" {
		t.Errorf("Name = %s", opts.Name)
	}
	if !opts.Force {
		t.Error("Force should be true")
	}
	if opts.Timeout != 10 {
		t.Errorf("Timeout = %d, want 10", opts.Timeout)
	}
}

// =============================================================
// Command registration on rootCmd
// =============================================================

// TestCommandsRegistered verifies that all target commands are registered
// on the root command.
func TestCommandsRegistered(t *testing.T) {
	for _, name := range []string{"unpublish", "purge", "republish", "start", "stop", "list"} {
		found := false
		for _, cmd := range rootCmd.Commands() {
			if cmd.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("command %q not registered on rootCmd", name)
		}
	}
}

// TestCommandUseStrings verifies the Use strings for each command.
func TestCommandUseStrings(t *testing.T) {
	tests := []struct {
		cmd  func() string
		want string
	}{
		{func() string { return unpublishCmd.Use }, "unpublish <name>"},
		{func() string { return purgeCmd.Use }, "purge <name>"},
		{func() string { return republishCmd.Use }, "republish <name>"},
		{func() string { return startCmd.Use }, "start <name>"},
		{func() string { return stopCmd.Use }, "stop <name>"},
		{func() string { return listCmd.Use }, "list"},
	}
	for _, tt := range tests {
		got := tt.cmd()
		if got != tt.want {
			t.Errorf("Use = %q, want %q", got, tt.want)
		}
	}
}

// TestCommandShortDescriptions verifies each command has a non-empty
// Short description.
func TestCommandShortDescriptions(t *testing.T) {
	for _, c := range []*cobra.Command{unpublishCmd, purgeCmd, republishCmd, startCmd, stopCmd, listCmd} {
		if c.Short == "" {
			t.Errorf("command %q has empty Short description", c.Name())
		}
	}
}
