# Contributing to Pharos CLI

Thanks for your interest. This repo is the Go CLI for the PHAROS MCP package registry.

## Prerequisites

- Go 1.25 (see `go.mod`)

## Build

```bash
go build -o pharos .
```

## Test

```bash
go test ./...
```

`go vet ./...` and `gofmt -l .` should be clean.

## Before You Open a PR

1. **Open an issue first** for anything beyond a bug fix. This avoids wasted work on features we may not want.
2. **Keep the diff focused.** One change, one commit when you can. Large PRs are harder to review — we may ask you to split.
3. **Run `go test ./...`** before submitting, not just the tests for your change.

## PR Checklist

Your PR should include:

- [ ] Linked issue (`Closes #N` in the description)
- [ ] Description of what and why
- [ ] Tests for any new behavior
- [ ] `go test ./...` passes
- [ ] `go vet ./...` clean
- [ ] `gofmt -l .` clean
- [ ] No new dependencies (or explain why they're needed)
- [ ] No breaking changes (or document them)

## Breaking Changes

If your PR changes existing CLI flags, command output, or behavior:

1. Document what breaks in the PR description
2. Explain who is affected and how to migrate

## What We Won't Accept

- Interactive prompts or a TUI (Pharos is scriptable)
- New dependencies for functionality achievable in the Go stdlib
- Cosmetic refactors with no functional benefit
- Changes to `.github/workflows/` (ask first)

## Issues

Open an issue with the command you ran, the output, and your OS.

## Terminology

- Say **package ID**, not "display name"
- Say **registry**, not "official" — catalogs are synced from mcp.io / mcp.so / Smithery

## License

By contributing, you agree that your contributions are licensed under the MIT license.
