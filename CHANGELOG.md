# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- MIT licence file, code of conduct, contributing guide, and issue templates.
- README badges, a complete command list, and `curl` as the lead install method.
- Auto-configuration support for 5 new MCP clients: VS Code (GitHub Copilot),
  Windsurf, Gemini CLI, Amazon Q Developer, and Roo Code. All use the
  `{"mcpServers": {}}` JSON format with safe patching that preserves existing
  config keys. Remote entries default to `type: "http"` for modern clients.
- Auto-configuration support for 4 more MCP clients using new config formats:
  Codex CLI and Grok Build (TOML `[mcp_servers]`), Zed (JSON `context_servers`),
  and Aider (YAML `mcp-servers` list). Aider supports stdio only; the other
  three support all 3 install kinds (remote HTTP, local HTTP, stdio).
- `pharos doctor` now validates client config files by format: JSON
  (Claude Desktop, Cursor, VS Code, Windsurf, Gemini, Amazon Q, Roo Code,
  OpenCode, Zed), YAML (Hermes Agent, Aider), and TOML (Codex CLI, Grok
  Build). Previously only JSON and Hermes YAML were validated; TOML and
  Aider configs were incorrectly parsed as JSON.

### Fixed

- API client now retries on HTTP 429 (Too Many Requests) with exponential
  backoff (2s, 4s, 8s) up to 3 times, honoring the Retry-After header.

## [1.0.0] - 2026-08-18

First stable release. Search, install and remove MCP servers, and configure
clients automatically.

### Added

- Claude Code as a supported client.
- Install kinds, so a package is installed the way its transport actually
  requires rather than one path for everything.
- `--transport` and `--registry` filters on `pharos search`.
- The Pharos daemon: just-in-time loading of stdio servers and auto-unload when
  idle, plus `pharos daemon` log and restart subcommands and integration with
  `pharos stop`.
- Autostart on boot, and starting the daemon as part of `pharos install`.
- Windows amd64 support. Not end-to-end tested in this release.
- `tags` in the package manifest, prompted for during `pharos init`.

### Changed

- Client configuration is patched in place instead of rewritten, so settings
  Pharos does not own survive an install.

### Fixed

- The default registry URL now points at `api.getpharos.dev`. It previously hit
  the web frontend and returned 404.
- `Endpoint` was missing from the `Manifest` struct, so remote packages lost
  their endpoint on parse.

### Security

- The server name in a stop request is sanitised, closing a path traversal.

## [0.0.9] - 2026-08-12

Initial pre-release binary, for testing rather than public use.

### Added

- 26 CLI commands, including `search`, `install`, `publish`, `init`, `lock`,
  `list`, `remove` and `login`.
- Semantic versioning with recursive dependency resolution, `pharos lock`, and
  circular dependency detection.
- Multi-client support: Claude Desktop, Cursor, Cline, OpenCode, Hermes and a
  generic MCP format, plus custom client registration.
- GitHub OAuth authentication.
- Safe config writes using a copy, validate and atomic swap.
- Runtime requirement checks in `install`, `info` and `doctor`, with a clear
  message when the runtime executable is not on `PATH`.
- Download counts, package size and GitHub stars in `pharos info`.
- WSL2 detection.
- Prebuilt binaries for linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64.

### Fixed

- `pharos remove` no longer corrupts a Hermes `config.yaml` by writing JSON
  into it.
- `findDependents` scans the store directory rather than the lockfile, so it
  sees packages the lockfile does not list.
- Nested error responses from the registry are parsed correctly instead of
  surfacing as an opaque failure.
- Installing a dependency preserves its transport type.
- HTTP and SSE packages that declare a `bin` field download the tarball, so
  `pharos start` can run them locally.

### Security

- Path traversal check in `createTarball`.

[Unreleased]: https://github.com/Wpnx330/pharos-cli/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/Wpnx330/pharos-cli/compare/v0.0.9...v1.0.0
[0.0.9]: https://github.com/Wpnx330/pharos-cli/releases/tag/v0.0.9
