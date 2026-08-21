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

`go vet ./...` should stay clean. `gofmt -l .` should return no files.

## Pull requests

### Before you open a PR

1. **Open an issue first** for anything beyond a bug fix. This avoids wasted work on features we may not want.
2. **Keep the diff focused.** One change, one commit when you can. PRs over ~400 lines are harder to review and may be asked to split.
3. **Run the full test suite** — `go test ./...` — not just the tests for your change. Regressions block merge.

### PR requirements

Your PR must include:

- [ ] **Linked issue** (`Closes #N` in the description)
- [ ] **Description** of what and why
- [ ] **Tests** for any new behavior (feature PRs without tests will be requested to add them)
- [ ] **`go test ./...` passes** locally
- [ ] **`go vet ./...` clean**
- [ ] **`gofmt -l .` clean**
- [ ] **No breaking changes** unless explicitly documented (see below)
- [ ] **No new dependencies** unless justified (see below)

### What we will reject

- Changes to `.github/workflows/` without maintainer discussion
- Features that add interactive prompts or a TUI (Pharos is scriptable)
- New dependencies for functionality achievable in the Go stdlib
- Cosmetic refactors with no functional benefit
- Changes that replace client config files instead of patching them
- Any change claiming Claude Desktop support for remote HTTP servers (Desktop is stdio-only)

### Breaking changes

If your PR changes existing CLI flags, command output, API endpoints, or config formats:

1. Document the breakage in the PR description
2. Explain who is affected and how to migrate
3. The version number may need to bump — note this

### New dependencies

If you add any imports:

1. List them in the PR description
2. Explain why they're needed and why stdlib won't work
3. Pharos CLI is a single static binary — keep it lean

### Review process

All PRs are reviewed before merge. The review includes:

- **Security scan** of the diff (before the code is pulled locally)
- **Full test suite** run on the review branch
- **Binary build** and manual verification of the claimed behavior
- **Quality review**: design fit, complexity, correctness, backward compatibility, performance, scope

We squash-merge. Your commit history doesn't need to be perfect, but meaningful commit messages help.

### After merge

- Linked issues are closed automatically via `Closes #N`
- If your PR changes CLI behavior, docs are updated by the maintainer
- If you picked up a good-first-issue and want another, let us know

## Issues

Open an issue with the command you ran, the output, and OS. Starter tickets should be checkable against source, not padding.

## Terminology

- Call it **package ID**, not "display name"
- Call it **registry**, not "official" — mcp.io is synced, not official
- Do not call mcp.io "official" or "upstream." Synced catalogs are "synced from mcp.io / mcp.so / Smithery."

## License

By contributing, you agree that your contributions are licensed under the MIT license.
