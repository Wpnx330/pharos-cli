package install

import (
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/api"
)

// Fixtures F1–F7 must match docs/INSTALL_KINDS.md and the shared contract.
func TestClassifyKindFixturesF1ToF7(t *testing.T) {
	f1 := ClassifyInput{
		Transport: "streamable-http",
		Endpoint:  "https://world-time.example/mcp",
	}
	f2 := ClassifyInput{
		Transport: "http-sse",
		Endpoint:  "https://echo.example/sse",
		Bin:       "test-echo-server",
	}
	f3 := ClassifyInput{
		Transport: "http-sse",
		Bin:       "test-echo-server",
	}
	f4 := ClassifyInput{
		Transport: "stdio",
		Bin:       "bin/server",
	}
	f5npx := ClassifyInput{
		Transport: "stdio",
		Command:   "npx -y @scope/mcp-server",
	}
	f5runtime := ClassifyInput{
		Transport: "stdio",
		Runtime:   "npx",
		Package:   "@scope/mcp-server",
	}
	f6stdio := ClassifyInput{Transport: "stdio"}
	f6http := ClassifyInput{Transport: "http-sse"}

	tests := []struct {
		id   string
		in   ClassifyInput
		want Kind
	}{
		{"F1 streamable-http + endpoint, no bin", f1, KindRemoteHTTP},
		{"F2 http-sse + endpoint + bin (0.2.5)", f2, KindRemoteHTTP},
		{"F3 http-sse + bin, no endpoint (0.2.4)", f3, KindLocalHTTP},
		{"F4 stdio + native tarball (bin)", f4, KindStdio},
		{"F5 stdio + npx command, no tarball", f5npx, KindStdio},
		{"F5 stdio + runtime+package, no tarball", f5runtime, KindStdio},
		{"F6 transport stdio only, no launch data", f6stdio, KindNone},
		{"F6 transport http-sse only, no launch data", f6http, KindNone},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			got := ClassifyKind(tc.in)
			if got != tc.want {
				t.Fatalf("ClassifyKind() = %v (%d), want %v (%d)", got, got, tc.want, tc.want)
			}
		})
	}

	t.Run("F7 REMOTE_ONLY + F3 rejected", func(t *testing.T) {
		kind := ClassifyKind(f3)
		if kind != KindLocalHTTP {
			t.Fatalf("F3 kind = %v, want KindLocalHTTP", kind)
		}
		if !RemoteOnlyRejected(kind, true) {
			t.Fatal("F7: REMOTE_ONLY + F3 must be rejected")
		}
		if RemoteOnlyRejected(kind, false) {
			t.Fatal("F3 must not be rejected when REMOTE_ONLY is off")
		}
	})

	t.Run("F7 REMOTE_ONLY + F4 rejected", func(t *testing.T) {
		kind := ClassifyKind(f4)
		if kind != KindStdio {
			t.Fatalf("F4 kind = %v, want KindStdio", kind)
		}
		if !RemoteOnlyRejected(kind, true) {
			t.Fatal("F7: REMOTE_ONLY + F4 must be rejected")
		}
	})

	t.Run("F7 REMOTE_ONLY does not reject F1", func(t *testing.T) {
		if RemoteOnlyRejected(ClassifyKind(f1), true) {
			t.Fatal("kind 1 must be allowed under REMOTE_ONLY")
		}
	})
}

func TestClassifyKindEndpointWinsTieBreak(t *testing.T) {
	// test-echo 0.2.5: endpoint + bin is Kind 1, not Kind 2.
	got := ClassifyKind(ClassifyInput{
		Transport: "http-sse",
		Endpoint:  "https://example.com/mcp",
		Bin:       "test-echo-server",
		Runtime:   "binary",
		Package:   "test-echo-server",
	})
	if got != KindRemoteHTTP {
		t.Fatalf("endpoint+bin = %v, want KindRemoteHTTP (kind 1)", got)
	}
}

func TestClassifyKindHTTPFamilyTransports(t *testing.T) {
	for _, tr := range []string{"http", "http-sse", "http+sse", "sse", "streamable-http"} {
		got := ClassifyKind(ClassifyInput{Transport: tr, Bin: "server"})
		if got != KindLocalHTTP {
			t.Errorf("transport %q + bin = %v, want KindLocalHTTP", tr, got)
		}
	}
}

func TestClassifyKindEmptyTransportDefaultsToStdio(t *testing.T) {
	got := ClassifyKind(ClassifyInput{Command: "uvx mcp-server-git"})
	if got != KindStdio {
		t.Fatalf("empty transport + command = %v, want KindStdio", got)
	}
}

func TestClassifyKindRuntimePackageRequiresKnownRuntime(t *testing.T) {
	got := ClassifyKind(ClassifyInput{
		Transport: "stdio",
		Package:   "some-pkg",
	})
	if got != KindNone {
		t.Fatalf("package without known runtime = %v, want KindNone", got)
	}

	for _, rt := range []string{"npx", "uvx", "docker", "python", "binary"} {
		got := ClassifyKind(ClassifyInput{Transport: "stdio", Runtime: rt, Package: "pkg"})
		if got != KindStdio {
			t.Errorf("runtime %q + package = %v, want KindStdio", rt, got)
		}
	}
}

func TestClassifyManifest(t *testing.T) {
	got := ClassifyManifest(api.Manifest{
		Transport: "streamable-http",
		Endpoint:  "https://world-time.example/mcp",
	})
	if got != KindRemoteHTTP {
		t.Fatalf("ClassifyManifest(F1) = %v, want KindRemoteHTTP", got)
	}
}

func TestEnvRemoteOnly(t *testing.T) {
	t.Setenv("PHAROS_REMOTE_ONLY", "")
	if EnvRemoteOnly() {
		t.Fatal("empty env must be false")
	}
	for _, v := range []string{"true", "TRUE", "1", "yes", "YES"} {
		t.Setenv("PHAROS_REMOTE_ONLY", v)
		if !EnvRemoteOnly() {
			t.Errorf("PHAROS_REMOTE_ONLY=%q must be true", v)
		}
	}
	for _, v := range []string{"false", "0", "no", "mcp-apps"} {
		t.Setenv("PHAROS_REMOTE_ONLY", v)
		if EnvRemoteOnly() {
			t.Errorf("PHAROS_REMOTE_ONLY=%q must be false", v)
		}
	}
}

func TestRemoteOnlyRejectedNoneAndUnknown(t *testing.T) {
	if RemoteOnlyRejected(KindNone, true) {
		t.Fatal("KindNone is not a remote-only rejection of a local install")
	}
	if RemoteOnlyRejected(KindRemoteHTTP, true) {
		t.Fatal("kind 1 must not be rejected")
	}
}

func TestKindString(t *testing.T) {
	if KindRemoteHTTP.String() != "1" && KindRemoteHTTP.String() == "" {
		t.Fatal("Kind.String must be non-empty")
	}
	if KindNone.String() == KindRemoteHTTP.String() {
		t.Fatal("kind strings must differ")
	}
}

func TestIsHTTPEndpoint(t *testing.T) {
	yes := []string{"https://example.com/mcp", "http://127.0.0.1:8080/sse", "  HTTPS://Example.COM/x  "}
	for _, ep := range yes {
		if !IsHTTPEndpoint(ep) {
			t.Errorf("IsHTTPEndpoint(%q) = false, want true", ep)
		}
	}
	no := []string{"", "stdio", "npx -y foo", "file:///tmp/x", "javascript:alert(1)", "http://"}
	for _, ep := range no {
		if IsHTTPEndpoint(ep) {
			t.Errorf("IsHTTPEndpoint(%q) = true, want false", ep)
		}
	}
}

func TestHasLaunchData(t *testing.T) {
	if !HasLaunchData(ClassifyInput{Bin: "server"}) {
		t.Fatal("bin is launch data")
	}
	if !HasLaunchData(ClassifyInput{Command: "npx -y pkg"}) {
		t.Fatal("command is launch data")
	}
	if !HasLaunchData(ClassifyInput{Runtime: "uvx", Package: "mcp-server-git"}) {
		t.Fatal("runtime+package is launch data")
	}
	if HasLaunchData(ClassifyInput{Runtime: "npx"}) {
		t.Fatal("runtime without package is not launch data")
	}
	if HasLaunchData(ClassifyInput{Package: "pkg"}) {
		t.Fatal("package without known runtime is not launch data")
	}
}

func TestIsHTTPFamily(t *testing.T) {
	for _, tr := range []string{"http", "HTTP", "http-sse", "http+sse", "sse", "streamable-http"} {
		if !IsHTTPFamily(tr) {
			t.Errorf("IsHTTPFamily(%q) = false", tr)
		}
	}
	if IsHTTPFamily("stdio") || IsHTTPFamily("npx") || IsHTTPFamily("") {
		t.Fatal("stdio/npx/empty must not be http-family")
	}
}
