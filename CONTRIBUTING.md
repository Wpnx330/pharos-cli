# Contributing to Pharos CLI

Thanks for looking. This repo is the Go CLI for the PHAROS MCP package registry.

Full command reference (site): https://discoverpharos.dev/cli/docs

Command names and flags live in `cmd/*.go` (`Use:` + `rootCmd.AddCommand`). If README, the site, and `pharos --help` disagree, the Go source is the source of truth. Do not invent flags.

## Prerequisites

- Go 1.25 (see `go.mod`)

## Build

```bash
go build -o pharos .
```

The binary prints `pharos version` from `cmd.Version` (overridable with `-ldflags`).

## Test

```bash
go test ./...
```

One package:

```bash
go test ./cmd -count=1
go test ./internal/install -count=1
```

`go vet ./...` should stay clean.

## Pull requests

- Keep the diff focused. One change, one commit when you can.
- Add or update tests for behavior you change.
- Do not commit secrets, `.env`, lockfiles from a local install (`pharos.lock`), or built binaries (`pharos`, `*.exe`).
- Do not call mcp.io "official" or "upstream." Synced catalogs are "synced from mcp.io / mcp.so / Smithery."
- If you add a command, add one line to the README `## Commands` block and keep `/cli/docs` in sync in `pharos-web`.

## Issues

Open an issue with the command you ran, the output, and OS. Starter tickets should be checkable against source, not padding.

## License

By contributing, you agree that your contributions are licensed under the MIT license.
