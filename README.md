# Pharos CLI

A Go CLI tool for the [PHAROS](https://getpharos.dev) MCP server package registry.

## Install

```bash
go install github.com/Wpnx330/pharos-cli@latest
```

Or build from source:

```bash
go build -o pharos .
```

## Commands

```bash
# Discovery
pharos search <query>          # Search the registry
pharos info <name>             # Show package details

# Package lifecycle
pharos init                    # Scaffold a new pharos.json (arrow-key TUI selectors)
pharos init --yes              # Non-interactive (use defaults)
pharos package [dir]           # Package a directory into a tarball (like npm pack)
pharos publish [dir]           # Package + upload + publish to the registry

# Local management
pharos install <name>          # Download and install a package
pharos list                    # List locally installed packages

# Auth
pharos login                   # GitHub OAuth login (opens browser)
pharos whoami                  # Show current authenticated user

# System
pharos config <key> [value]    # Get or set configuration
pharos health                  # Check registry health
pharos version                 # Print CLI version
```

## Flags

- `--json` — Output as JSON (search, info, health)
- `--limit` / `-n` — Number of search results (default: 10)
- `--page` / `-p` — Search page number
- `--version` / `-v` — Install a specific version
- `--global` / `-g` — Install system-wide
- `--token` / `-t` — Auth token for publishing
- `--dry-run` — Validate manifest without publishing
- `--yes` — Skip interactive prompts (init)

## Configuration

Config is stored at `~/.pharos/config.json`:

```bash
pharos config registry https://getpharos.dev
pharos config token <your-token>
```

Credentials are stored at `~/.pharos/credentials.json` after `pharos login`.

## Manifest (pharos.json)

```json
{
  "name": "my-mcp-server",
  "version": "1.0.0",
  "description": "An MCP server for X",
  "license": "MIT",
  "transport": "stdio",
  "runtime": "python",
  "command": "python server.py",
  "capabilities": ["tools"],
  "homepage": "https://github.com/user/repo",
  "repository": "https://github.com/user/repo",
  "files": ["server.py", "lib/"]
}
```

### Manifest fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Package name (unscoped or `@scope/name`) |
| `version` | yes | Semantic version |
| `description` | no | Short description shown in search results |
| `license` | no | SPDX license identifier |
| `transport` | yes | `stdio`, `http-sse`, or `http` |
| `runtime` | yes | `python`, `node`, `docker` |
| `command` | yes | Run command (e.g. `python server.py`, `node server.js`) |
| `entrypoint` | no | Alternative to `command` (Docker entrypoint) |
| `capabilities` | yes | MCP capability types: `tools`, `resources`, `prompts`, `logging` |
| `files` | no | Explicit file list to package (auto-detected if omitted) |
| `homepage` | no | Homepage URL |
| `repository` | no | Repository URL |

## Publishing

The `pharos publish` command runs a 4-phase flow automatically:

1. **Get user** — `GET /v1/auth/me` to determine your namespace
2. **Create package** — `POST /v1/packages` (creates the package row; 409 = already exists, OK)
3. **Upload** — `POST /v1/uploads` → PUT tarball bytes to presigned URL
4. **Publish** — `PUT /v1/packages/{name}` with version, manifest, blob reference, integrity hash

```bash
pharos publish ./my-mcp-server
```

You can also package separately:

```bash
pharos package ./my-mcp-server    # creates my-mcp-server-1.0.0.tgz
```

## Development

```bash
go test ./... -v -count=1
go vet ./...
go build .
```
