# PHAROS install kinds (contract)

See also `/mnt/c/Users/chris/Documents/TRON/temp/Pharos/INSTALL_KINDS.md`.

Shared by CLI (Go) and MCP (Python). Fixtures F1–F7 must match in both test suites.

## Classifier

```
if endpoint is http(s):// → Kind 1
else if transport in {http, http-sse, http+sse, sse, streamable-http}
        and (bin or command or runtime+package) → Kind 2
else if transport is stdio (or empty defaulting to stdio)
        and (bin or command or runtime+package) → Kind 3
else → not installable
```

**Tie-break:** endpoint + bin (test-echo 0.2.5) is **Kind 1**.

## Kinds

| Kind | Name | Process lives | Install writes |
|---|---|---|---|
| 1 | Remote HTTP/SSE/streamable-http | Publisher URL | Bookmark + client URL. No tarball. |
| 2 | Local HTTP/SSE/streamable-http | We start on this machine | Tarball or launch line; spawn uses **`bin`** (`strings.Fields`) even when `runtime=python` — never `python3 <package>`. Clients get `http://127.0.0.1:<port>` with type **`http`** for `http-sse` / `streamable-http`. Type `sse` only if transport is exactly `sse`. Desktop skip. |
| 3 | Local stdio | Child process | Tarball **or** npx/uvx/docker/python line (no Pharos tarball required) |

## Surfaces

CLI, MCP no-Apps, and MCP Apps all support kinds 1–3. `PHAROS_REMOTE_ONLY=true` is mobile: kind 1 only. `PHAROS_MCP_APPS` is iframe UI, not remote-only.

## Search

Installable if endpoint URL **OR** command **OR** bin **OR** (runtime in {npx,uvx,docker,python,binary} AND package). Do **not** hide packages only because they lack an endpoint.

## Fixtures

| id | fixture | kind |
|---|---|---|
| F1 | streamable-http + endpoint, no bin | 1 |
| F2 | http-sse + endpoint + bin (0.2.5) | 1 |
| F3 | http-sse + bin, no endpoint (0.2.6) | 2 |
| F4 | stdio + native tarball | 3 |
| F5 | stdio + `npx …` / runtime+package, no tarball | 3 |
| F6 | transport only, no launch data | not installable |
| F7 | REMOTE_ONLY + F3 or F4 | rejected |

## List / status

- Kind 1: `registered`/`remote`. Endpoint shown. SIZE/MEMORY/UPTIME/PORT = `—`.
- Kind 2: running/stopped + port/size/memory/uptime.
- Kind 3: idle unless a child is up. `remove` for npx-style drops metadata + client config only.

## Env / persist

- `PHAROS_REMOTE_ONLY=true|1|yes`
- `name@version` pin required for kind 2 tests (`test-echo-server@0.2.6`)
- Persist: `~/.pharos/store`
