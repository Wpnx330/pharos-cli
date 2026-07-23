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
pharos search <query>          # Search the registry
pharos install <name>           # Download and install a package
pharos info <name>              # Show package details
pharos list                     # List locally installed packages
pharos publish [dir]            # Publish a package to the registry
pharos config <key> [value]     # Get or set configuration
pharos health                   # Check registry health
pharos version                  # Print CLI version
```

## Flags

- `--json` — Output as JSON (search, info, health)
- `--limit` / `-n` — Number of search results (default: 10)
- `--page` / `-p` — Search page number
- `--version` / `-v` — Install a specific version
- `--global` / `-g` — Install system-wide
- `--token` / `-t` — Auth token for publishing
- `--dry-run` — Validate manifest without publishing

## Configuration

Config is stored at `~/.pharos/config.json`:

```bash
pharos config registry https://getpharos.dev
pharos config token <your-token>
```

## Publishing

Create a `pharos.json` in your package directory:

```json
{
  "name": "my-mcp-server",
  "version": "1.0.0",
  "description": "An MCP server for X",
  "license": "MIT",
  "homepage": "https://github.com/user/repo",
  "repository": "https://github.com/user/repo",
  "bin": "./server.js",
  "files": ["./server.js", "./lib/", "./README.md"]
}
```

Then publish:

```bash
pharos publish ./my-mcp-server
```

## Development

```bash
go test ./... -v -count=1
go vet ./...
go build .
```
