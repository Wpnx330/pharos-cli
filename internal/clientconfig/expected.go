package clientconfig

import (
	"encoding/json"
	"fmt"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// ExpectedEntry returns the JSON raw message MergeServer writes for name
// under the client's format, in the exact shape ReadServersFormat returns
// on read-back (the entry's value under its name key). It is the read-only
// twin of the merge path — no file is read or written:
//
//   - object JSON formats (mcpServers / opencode / zed): buildEntry, the
//     same serializer MergeServer hands to the patch writers
//   - TOML (Codex/Grok): a minimal [mcp_servers.<name>] document marshaled
//     with BurntSushi/toml, read back through readTOMLServers — the same
//     value path mergeTOML + ReadServersFormat produce
//   - Hermes YAML: a minimal mcp_servers: document marshaled with the same
//     hermesServerEntry mergeHermes writes (enabled: true), read back
//     through readHermesServers
//   - Aider YAML: a minimal mcp-servers: list with buildAiderEntry, read
//     back through readAiderServers
//   - array: a one-element arrayEntry document, read back through
//     readArrayServers (which strips the name field)
//
// The round-trip through each format's real writer and reader guarantees
// the result compares equal to ReadServersFormat output on files that
// MergeServer wrote (modulo JSON key order, which is insignificant).
//
// Clients that cannot represent the server (Claude Desktop remotes, Aider
// remotes) return a *SkipError.
func ExpectedEntry(c Client, name string, server ServerConfig) (json.RawMessage, error) {
	format := c.Format
	if format == "" {
		format = FormatMcpServers
	}
	switch format {
	case FormatArray:
		data, err := json.Marshal([]arrayEntry{{
			Name:    name,
			Command: server.Command,
			Args:    server.Args,
			Env:     server.Env,
			URL:     server.URL,
			Type:    server.Type,
		}})
		if err != nil {
			return nil, fmt.Errorf("marshal array entry: %w", err)
		}
		return readBackEntry(data, readArrayServers, name, "array entry")

	case FormatHermes:
		root := map[string]any{
			"mcp_servers": map[string]any{
				name: hermesServerEntry{
					Command: server.Command,
					Args:    server.Args,
					Env:     server.Env,
					Enabled: true,
					URL:     server.URL,
				},
			},
		}
		data, err := yaml.Marshal(root)
		if err != nil {
			return nil, fmt.Errorf("marshal hermes entry: %w", err)
		}
		return readBackEntry(data, readHermesServers, name, "hermes entry")

	case FormatTOML:
		root := map[string]any{
			"mcp_servers": map[string]any{
				name: buildTOMLEntry(c.ID, server),
			},
		}
		data, err := toml.Marshal(root)
		if err != nil {
			return nil, fmt.Errorf("marshal toml entry: %w", err)
		}
		return readBackEntry(data, readTOMLServers, name, "toml entry")

	case FormatAider:
		root := map[string]any{
			"mcp-servers": []any{buildAiderEntry(name, server)},
		}
		data, err := yaml.Marshal(root)
		if err != nil {
			return nil, fmt.Errorf("marshal aider entry: %w", err)
		}
		return readBackEntry(data, readAiderServers, name, "aider entry")

	default:
		return buildEntry(c.ID, server)
	}
}

// readBackEntry parses a serialized document with the format's server
// reader and returns the named entry's raw JSON.
func readBackEntry(data []byte, read func([]byte) (map[string]json.RawMessage, error), name, label string) (json.RawMessage, error) {
	servers, err := read(data)
	if err != nil {
		return nil, fmt.Errorf("read back %s: %w", label, err)
	}
	raw, ok := servers[name]
	if !ok {
		return nil, fmt.Errorf("%s %q missing after serialization", label, name)
	}
	return raw, nil
}
