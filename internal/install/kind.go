package install

import (
	"net/url"
	"os"
	"strings"

	"github.com/Wpnx330/pharos-cli/internal/api"
)

// Kind is the install classification from INSTALL_KINDS.md.
type Kind int

const (
	// KindNone is not installable (transport only, no launch data or endpoint).
	KindNone Kind = iota
	// KindRemoteHTTP is kind 1: publisher-hosted HTTP/SSE/streamable-http.
	KindRemoteHTTP
	// KindLocalHTTP is kind 2: we start HTTP/SSE/streamable-http locally.
	KindLocalHTTP
	// KindStdio is kind 3: local stdio child process.
	KindStdio
)

func (k Kind) String() string {
	switch k {
	case KindRemoteHTTP:
		return "1"
	case KindLocalHTTP:
		return "2"
	case KindStdio:
		return "3"
	default:
		return "0"
	}
}

// ClassifyInput is the manifest slice the classifier needs.
type ClassifyInput struct {
	Transport string
	Endpoint  string
	Bin       string
	Command   string
	Runtime   string
	Package   string
}

// ClassifyKind implements the shared F1–F7 classifier.
//
//	if endpoint is http(s):// → Kind 1
//	else if transport in {http, http-sse, http+sse, sse, streamable-http}
//	        and (bin or command or runtime+package) → Kind 2
//	else if transport is stdio (or empty defaulting to stdio)
//	        and (bin or command or runtime+package) → Kind 3
//	else → not installable
//
// Tie-break: endpoint + bin is Kind 1.
func ClassifyKind(in ClassifyInput) Kind {
	if IsHTTPEndpoint(in.Endpoint) {
		return KindRemoteHTTP
	}
	if IsHTTPFamily(in.Transport) && HasLaunchData(in) {
		return KindLocalHTTP
	}
	if isStdioTransport(in.Transport) && HasLaunchData(in) {
		return KindStdio
	}
	return KindNone
}

// ClassifyManifest classifies a registry manifest.
func ClassifyManifest(m api.Manifest) Kind {
	return ClassifyKind(ClassifyInput{
		Transport: m.Transport,
		Endpoint:  m.Endpoint,
		Bin:       m.Bin,
		Command:   m.Command,
		Runtime:   m.Runtime,
		Package:   m.Package,
	})
}

// RemoteOnlyRejected reports whether a classified kind must be refused
// when PHAROS_REMOTE_ONLY is active. Kind 1 is allowed; kinds 2 and 3
// are rejected. KindNone is not a remote-only rejection.
func RemoteOnlyRejected(kind Kind, remoteOnly bool) bool {
	if !remoteOnly {
		return false
	}
	return kind == KindLocalHTTP || kind == KindStdio
}

// EnvRemoteOnly is true when PHAROS_REMOTE_ONLY is true|1|yes (any case).
// PHAROS_MCP_APPS is a different flag and is never consulted here.
func EnvRemoteOnly() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("PHAROS_REMOTE_ONLY")))
	return v == "true" || v == "1" || v == "yes"
}

// IsHTTPEndpoint reports whether endpoint is an http(s) URL with a host.
func IsHTTPEndpoint(endpoint string) bool {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return false
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	return u.Host != ""
}

// IsHTTPFamily reports whether transport is HTTP/SSE/streamable-http.
// These must never be remapped to stdio/npx.
func IsHTTPFamily(transport string) bool {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "http", "http-sse", "http+sse", "sse", "https", "streamable-http":
		return true
	default:
		return false
	}
}

// HasLaunchData is true when bin, command, or a known runtime+package is set.
func HasLaunchData(in ClassifyInput) bool {
	if strings.TrimSpace(in.Bin) != "" || strings.TrimSpace(in.Command) != "" {
		return true
	}
	return isKnownRuntime(in.Runtime) && strings.TrimSpace(in.Package) != ""
}

func isStdioTransport(transport string) bool {
	t := strings.ToLower(strings.TrimSpace(transport))
	return t == "" || t == "stdio"
}

func isKnownRuntime(runtime string) bool {
	switch strings.ToLower(strings.TrimSpace(runtime)) {
	case "npx", "uvx", "docker", "python", "binary":
		return true
	default:
		return false
	}
}
