<!-- ═══════════════════════════════════════════════════════════════
     STATE TRACKER — update the checkbox + date line when a wave ships.
     App dev (CLI/registry/SDK) AND web dev both mirror progress here.
     Handoffs referencing this spec: TRON/temp/handoff-app-dev-v1.1.md,
     TRON/temp/handoff-web-dev-v1.1.md
     Last updated: 2026-09-02 08:55 ET by TRON (W1.3 shipped)
════════════════════════════════════════════════════════════════ -->

# PHAROS v1.1 Spec — Parity Plus — STATE TRACKER

| Wave | Items | State | Owner | Shipped in |
|------|-------|-------|-------|------------|
| W1.1 | A7 agent env contract + llm.txt | 🟢 DONE (Sep 1) — PHAROS_NON_INTERACTIVE/ASSUME_YES/JSON contract, 11 cmds gained --json, 926-line test harness incl. command-matrix + llm.txt golden, docs/llm.txt generated | app dev | pharos-cli db23a59 |
| W1.2 | C5 install receipts | 🟢 DONE (Sep 2) — receipts v2: status/errors partial-failure honesty, dep rows, canonical row, .bak H0 provenance; adversarial review 4 MAJORs → fixed 6f1b3cf; 737 tests green; live-binary QA'd | app dev | pharos-cli 835560e + 6f1b3cf |
| W1.3 | B2 search signals (CLI) | 🟢 DONE (Sep 2) — OWNER/CATEGORY cols + humanized downloads + (stale) suffix; --json carries publisher/category/tools_count/version_status; info Owner/Category guarded; +19 tests (suite green); live-binary QA'd | app dev | pharos-cli c244862 |
| W1.4 | B3 doctor --diff | 🟢 DONE (Sep 2) — b15d358 + adversarial-review remediation 4f13e10 (F1-F8: aider skip-merge, per-client lockfile record, case-fold relevance, test-honesty pins, numeric env, Grok headers, docs, read-only probe); +9 tests; live QA: subset install clean, case-rename surfaces MISSING+EXTRA | app dev | pharos-cli b15d358 + 4f13e10 |
| W1.4.1 | MSIX Claude Desktop (Store) detection | 🟢 DONE (Sep 3, d651bff) — additive Claude_* MSIX probe (Win+WSL2), classic-first union, never-launched skip; review F1-F3 fixed (README premise hedged, order asserted, windows CI matrix); live QA on sandboxed MSIX tree | app dev | pharos-cli d651bff |
| W1.4.2 | Windows CI portability | 🟢 DONE (Sep 4, 8a78697) — 38 POSIX-assumption test fixes across 7 pkgs exposed by new windows matrix; 3-line prod guard ReloadDaemon(pid<=0); external review APPROVE; CI green BOTH jobs (ubuntu+windows) | app dev | pharos-cli 8a78697 |
| RELEASE | v1.1.0 shipped | 🟢 LIVE (Sep 4) — tag v1.1.0, 11 assets (both naming variants), installers resolve latest→v1.1.0; full Wave-1 train: A7 + C5 + B2 + B3 + MSIX + windows CI; release notes list all features | users | [v1.1.0](https://github.com/Wpnx330/pharos-cli/releases/tag/v1.1.0) |
| W2.1 | pharos import --adopt (A2 onboarding) | 🟢 DONE (Sep 4, 2eae99a) — read all clients, dedupe, managed baseline; conflict UX (pick/use-everywhere/skip, --yes, dry-run); W1.1 contract (JSON purity, env contract, exit codes); orphans preserved; external review F1-F5 fixed (README truthful drift framing, 3 contract tests, known-edges disclosed, prompt nit, dry-run probe); live QA: dedupe+conflict+adopt→doctor clean | app dev | pharos-cli 2eae99a |
| site | Wave D: v1.1.0 docs parity (doctor --diff, receipts v2, trust signals, agent contract, MSIX FAQ) + getPackage retry/timeout (crawler 404 fix) | 🟢 BUILT+QA (Sep 4) | web dev | reviewer PASS 6/6; pending deploy |

### Wave-1 shipped summary (for web-dev parity — v1.1.0 LIVE Sep 4)

New user-facing surfaces in v1.1.0 (docs/pages should reflect ALL of these):
1. **`pharos doctor --diff`** — per-client drift report vs lockfile baseline; MISSING/MODIFIED/EXTRA; JSON via `PHAROS_JSON=1`; fully read-only. Docs: README "Doctor drift" section.
2. **Install receipts v2** — every install emits a receipt with per-client results, dependency rows, `status: ok|partial`, errors[] on partial. Surface in docs where install output is described.
3. **Search trust signals** — OWNER + CATEGORY columns, humanized download counts, `(stale)` version suffix; `info` shows Owner/Category. Docs: README "Search Results" section.
4. **Agent contract** — `PHAROS_JSON=1` + `PHAROS_NON_INTERACTIVE=1` + `PHAROS_ASSUME_YES=1`; `docs/llm.txt` machine-readable command surface. If the site has an "automate pharos" or agent page, point it at llm.txt.
5. **MSIX Claude Desktop** — Store-installed Claude Desktop now detected/configured; never-launched = skipped (launch once first). Worth a FAQ/troubleshooting entry.
6. **`mcp.directory` sixth registry source** — 1,700+ extra packages; update any "sources" list on the site.
7. **Version** — everything above is live in v1.1.0 binaries (installers resolve it automatically). Windows CI green.

| W2.1 | A2 pharos adopt | ⚪ pending | app dev | — |
| W2.2 | A1 profiles (opt-in) | ⚪ pending | app dev | — |
| W3.1 | A3 brew/scoop | ⚪ pending (signing Q open) | app dev | — |
| W3.2 | A4 pharos try | ⚪ pending | app dev | — |
| W4.1 | B1 security scorecard | ⚪ pending | app dev | — |
| W4.2 | D1 boost plumbing | ⚪ pending | app dev | — |
| W5   | A6 / C2 / A5 | ⚪ parked | app dev | — |
| W6   | metrics + monetization | ⚪ parked (no selling before W4.1 + volume) | chris+app dev | — |
| site | homepage registry copy (2 strings) | 🟢 DONE (Sep 1) — full 7-registry list live, 133,000+ counts, Badge/FilterBar label maps cover all sources, "Available on undefined" bug fixed | web dev | pharos-web e752bc3, deployed |
| site | llm.txt serving (after W1.1) | 🟢 UNBLOCKED — docs/llm.txt is in pharos-cli repo @ db23a59; serve at /llm.txt + link from agent docs | web dev | — |
| site | Sprint 1 DoD (star CTA, canonicals, sitemap+100 pkg URLs, schema SWA/FAQ/HowTo, llms.txt current, CWV spot) | 🟢 DONE (Sep 1) | web dev | pharos-web 252372b..cc9639d, deployed; GSC verify + clean-Windows install.ps1 test = Chris |
| site | hero carousel overflow fix (CTA pushed slide 0 past min-h; OpenCode agent fix + reviewer pass, 3-viewport verified) | 🟢 DONE (Sep 1) | web dev | pending commit+deploy |
| site | /search instant-nav (shell+skeleton streams, was 11-20s cold freeze) | 🟢 DONE (Sep 2) | web dev | pharos-web 20a2c1e deployed; nav commit 53ms, skeleton at 30ms, results ~400ms |
| site | search follow-ups: param-aware SSR, no cached failures, error boundaries, 8s timeouts, CLS | 🟢 DONE (Sep 2) | web dev | pharos-web b43317f deployed; deep-link SSR verified live; typed-search refetch wired |
| site | Sprint 2 Wave A: /servers hub + 13 category pages, SoftwareApplication/Breadcrumb/ItemList/FAQPage JSON-LD, sitemap 1027 URLs, install.ps1 PS5.1 fix | 🟢 BUILT+QA (Sep 2) | web dev | reviewer PASS 7/7; pending deploy |
| site | Sprint 2 Wave B: cornerstone Best MCP Servers 2026 + PHAROS vs mcpm.sh comparison, FAQ JSON-LD, footer/llms.txt wiring | 🟢 BUILT+QA (Sep 2) | web dev | reviewer PASS 6/6, follow-ups closed; pending deploy |
| site | Sprint 2 Wave C: client install guides (hub + Cursor/Claude Desktop/Claude Code/VS Code), GEO pass llms-full, cross-links, footer Guides group | 🟢 BUILT+QA (Sep 2) | web dev | reviewer PASS 7/7, follow-ups closed; pending deploy |


---
# PHAROS v1.1 Feature Spec — Parity Plus
> Created: Sep 1, 2026 | Status: DRAFT for Chris review + debate
> Trigger: mcpm.sh deep-dive (995 stars). Parity features are reframed, not copied.
> Three sources: (A) competitor parity done better, (B) features from other MCP repos worth absorbing, (C) greenfield features nobody has built
> Ground rules from Chris: understand each deeply first, beat them on UX not just code. PHAROS's existing edges (federated multi-registry search, Go single binary, 16-client coverage, daemon JIT supervisor, publishing flow, pharos-discovery agent server) are the baseline to build ON, not features to match.

---

## Part A: Parity Features (mcpm.sh has, we don't — done BETTER)

### A1. Context Profiles  `HIGH`
**What mcpm does:** named virtual tags grouping servers (`mcpm profile create work`); enable/execute a whole profile into a client at once.

**The user problem:** people separate MCP servers into real contexts: work vs personal, client A vs client B, "heavy servers I only need sometimes." Today pharos writes every installed server into every target client config, so switching contexts means manual config surgery.

**How we do it better (UX first):**
- Profiles map to CLIENTS, not just tags. `pharos profile create work --client cursor` means "this is my Cursor context." mcpm's profiles are manager-internal abstractions; ours become actual client state. One mental model, no mapping step.
- Natural names + emoji-friendly: `pharos profile use work` makes Cursor contain exactly the work servers. Switching contexts is one command, not editing JSON.
- `pharos install <name> --profile work` at install time (prompt offers it interactively instead of "write to all clients?").
- Inheritance: a `base` profile every profile inherits, so common servers aren't duplicated.
- Agent hook: `pharos profile ls --json` gives agents the context map. pharos-discovery can later expose "switch my client to profile X" as an approval-gated tool.
- Daemon synergy: `pharos profile run work` starts the profile's daemon-managed servers and idles the rest. Context switching also saves memory.
**Effort:** M (state model + config write orchestration). **Risk:** interaction with existing `mcp.json` state needs a clean migration (detect unprofiled servers, offer `pharos profile adopt`).

### A2. Client Config Import (one-command onboarding)  `HIGH`
**What mcpm does:** `mcpm client import cursor` adopts existing client config into the manager.

**The user problem:** the #1 adoption barrier for ANY MCP manager is "I already hand-configured 8 servers in Claude and Cursor; starting over sucks."

**We already have `pharos import`** (imports client configs into a pharos.lock) — but it's dependency-resolution-oriented, not onboarding-oriented. The better version:
- `pharos import --adopt` reads every detected client config (we have all 16 paths), dedupes by name, builds managed state, and reports: "Found 9 servers across 4 clients. 2 conflicts (different versions in Cursor vs Claude Desktop). Adopt all / review conflicts / skip?"
- Conflict UX is the differentiator: side-by-side diff of the two entries, one keystroke to pick, "use this one everywhere" option. mcpm just imports; we resolve.
- Post-adopt, pharos becomes the source of truth with zero re-work by the user. This is the single best conversion feature on this list.
- Per-client import too: `pharos import --from cursor` to just adopt Cursor's set.
**Effort:** M (reuse the client-format readers; new adopt/merge state layer). **Risk:** LOW.

### A3. Homebrew Distribution  `HIGH`
**What mcpm does:** `brew install mcpm`.

**Why it matters:** the default install path for macOS CLI users; "brew install X" is muscle memory. PHAROS has go install + curl script + release binaries. Brew tap closes the loop for the biggest CLI audience.

**How we do it better:**
- `homebrew-pharos` tap repo + formula with version bumps automated in the release workflow (GoReleaser handles this natively, one config block).
- Stretch: once downloads are consistent, submit to homebrew-core for tapless `brew install pharos`.
- Also add Scoop bucket for Windows (same GoReleaser config, near-zero extra cost) — mcpm has no Windows package manager story at all; Windows is 5 of our 8 release downloads today.
**Effort:** S (GoReleaser) + M (homebrew-core polish later).

### A4. Try-Before-Wire Execution  `MEDIUM`
**What mcpm does:** `mcpm run` executes a server over stdio/HTTP directly; `mcpm inspect` launches MCP Inspector against it for interactive debugging.

**The user problem:** before writing a server into your agent's config, you want to know it works, see its tools, test a call.

**How we do it better:**
- `pharos try <name>`: installs to a sandbox dir (no client writes), JIT-loads via the existing daemon, runs a smoke handshake (initialize + tools/list), prints capability summary: tools with one-line descriptions, resources, prompts. This is the "what does this thing actually do" answer in 5 seconds.
- `pharos try <name> --inspect` pipes it into the official MCP Inspector (npx @modelcontextprotocol/inspector) pre-wired.
- Reuses the daemon JIT machinery, which mcpm doesn't have — our version starts fast and auto-unloads. Theirs is a foreground process; ours leaves nothing behind.
- Sandbox flag generalizes: `pharos install <name> --sandbox` for power users.
**Effort:** M. **Risk:** LOW (daemon + handshake code exists).

### A5. Secure Tunnel Sharing  `MEDIUM` (debate: scope down)
**What mcpm does:** `mcpm share` exposes a local stdio server over a secure remote tunnel.

**How we'd do it better:** self-hostable relay (tiny Go binary, Docker one-liner) + `pharos share <name> --via my-relay` with token auth and per-tunnel TTLs. Or WebRTC/Cloudflare Tunnel integration for zero-infra.
**DEBATE:** this is a real feature but a rabbit hole (infra, security surface). Recommendation: scope to `pharos expose` = reverse-proxy an already-running daemon server with a token, leveraging existing proxy listener. Defer public relay service.
**Effort:** M as scoped, L as full relay.

### A6. Source-Tracked Updates  `MEDIUM`
**What mcpm does:** per-server origin tracking (git/npm/binary), `git pull --ff-only` updates, dry-run mode.

**Our position:** `pharos update` already rewrites client configs on update (better than mcpm, which requires client sync separately). Add:
- Origin metadata per installed server (registry package vs git URL vs local dir) recorded at install time.
- `pharos update --check` dry-run listing what would change, per server, with changelog links when the origin is a repo.
- For registry-installed packages: version pinning + `pharos update --all` summary table.
**Effort:** S-M. **Risk:** LOW.

### A7. Agent Automation Contract (env-var protocol + repo llm.txt)  `EASY WIN`
**What mcpm does:** MCPM_NON_INTERACTIVE / FORCE / JSON_OUTPUT env contract; auto-generated llm.txt describing every command for agents.

**How we do it better:**
- Formalize `PHAROS_NON_INTERACTIVE=1`, `PHAROS_ASSUME_YES=1`, `PHAROS_JSON=1` (or unify under existing --json flags) as a documented, tested contract: every command has a non-interactive path, every output machine-parsable.
- `docs/llm.txt` in the repo (and served from the site): complete command reference written FOR agents, auto-generated from the Cobra command tree in CI. mcpm does this well; matching it is table stakes, and our version doubles as the pharos-discovery agent's instruction manual.
**Effort:** S. **Risk:** NONE.

---

## Part B: Features From Other MCP Repos Worth Absorbing (debate each)

### B1. Security-First Install Pipeline (from rolecraft, 75 stars)  `RECOMMEND`
Every install runs a security scan before activation. With 77 unique cloners in 2 weeks and MCP supply-chain attacks being the ecosystem's known fear, `pharos audit` exists post-install; extend it pre-install:
- Install-time scorecard: repo age, maintainer count, known CVEs, package provenance (who published, when), manifest safety (command sanity check), community trust signals.
- Grade (A-F) shown in `pharos search` results and `pharos info`. Nothing blocked by default; risk surfaced at decision time. This IS the "npm audit moment" for MCP and nobody owns it yet. HarnessKit gestures at it; rolecraft is closest; PHAROS has the registry-side data (publish history, provenance) to do it credibly.
- Approval-gated installs fit pharos-discovery's existing trust model.
**Effort:** M-L (registry data + scoring heuristics). **Debate:** scoring credibility vs. gaming; start simple (provenance + age + manifest checks), label clearly as heuristic.

### B2. Health Scores / Community Signals in Search (from mcpmarket + common pattern)  `RECOMMEND (small)`
Search results show install count, last-updated, verified badge. Ours can show: downloads, publish date, updated date, owner, tag, origin registry. Cheap, high trust value, uses data we already have.
**Effort:** S.

### B3. Watch/Diff (from mcpmonitoring + mcpdog ideas)  `DEBATE`
`pharos watch <name>`: notify (or update) when a new version publishes. Config-drift detection: `pharos doctor --diff` reports when a client config was hand-edited out from under pharos (this WILL happen and today it's invisible).
**Recommendation:** skip watch/notify (low value at this scale); DO the drift detection — it's an integrity feature that matches our safe-write story and is a natural `doctor` extension.
**Effort:** S for drift, M for watch.

### B4. TUI Dashboard (from mcp-dock, 221 stars, and skilldock's desktop approach)  `DEBATE - LEAN NO`
Interactive terminal dashboard: installed servers, running status, toggle per client.
**Lean no for v1.1:** mcp-dock stalled despite having one; heavy build, and our phone-first principal isn't the TUI demographic. `pharos list --running` + `daemon status` covers the need. Revisit only if users ask.
**Effort:** L. **Skip watch on this one.**

### B5. GUI Desktop App (from skilldock, 499 stars, HarnessKit-adjacent)  `NO for now`
The skills-wave absorbers are winning non-CLI users via desktop apps. Real segment, wrong decade for a solo Go project. Log it, revisit at 50+ stars when there's evidence of demand.
**Effort:** XL. **Decision: parked.**

### B6. Vendor-Neutral Toggle Model (from mcpick, 95 stars)  `ALREADY HAVE, SHARPEN`
`pharos config client toggle` per-client enable/disable exists implicitly via remove/install. Making per-client enable/disable a first-class, non-destructive operation (server stays installed, entry commented/disabled in that client only) is a small UX win that mcpick's whole product is built on. Fold into A1 profiles (profiles ARE the better toggle model). **No separate work item.**

### B7. Structured Lockfile Ecosystem (from AGENTS.lock, 21 stars)  `NOTED`
Their idea: lockfiles as shareable, auditable manifests for agents. Our pharos.lock already exists per-project. The absorbing version: `pharos lock --export` → portable profile bundle (lockfile + client targets) that a teammate (or an agent) can `pharos sync` from. Ties into A1 and remote-team onboarding.
**Effort:** M. **Debate:** real feature for teams; user base may be too early. Park behind A1.

---

## Part C: Greenfield (nobody in the surveyed set has it)

### C1. Context-Aware Fleet Dashboard for Agents (pharos-discovery extension)  `RECOMMEND`
The discovery server currently lets an agent search/evaluate/connect. Extend with a `fleet_status` tool: the agent asks "what MCP servers do I have, what's healthy, what's stale?" and gets structured state across ALL clients (the 16-format map is the moat — no competitor can cheaply read every client's config). Agent-side value, zero new CLI surface, pure differentiation vs mcpm's env-var subprocess model.
**Effort:** M. **Debate:** server-side scope creep; keep read-only first.

### C2. Idle-Cost Budgets (extends our unique per-server idle-timeout)  `RECOMMEND (small)`
Nobody manages the COST side of MCP fleets: every stdio server is a resident process; every remote call is tokens. `pharos budget` = aggregate view (processes resident, estimated memory, optional token-usage hook later) + suggestions ("3 servers idle >30d: unload?"). Extends the JIT daemon story from feature to philosophy: PHAROS is the manager that treats agent resources as precious. Cheap, on-brand, unique.
**Effort:** S-M. **Debate:** token metering is vaporware until APIs expose usage; ship the process/memory view first.

### C3. `pharos explain` — Plain-English Server Summarizer  `DEBATE`
Install-time or on-demand: what does this server do, what data does it touch, what could break, in 5 lines. LLM-generated at publish time (registry stores the summary) so it's deterministic at install.
**Debate honestly:** adds an AI dependency and moderation surface for polish. Value is real (registry entries are poorly described — Chris's own PH copy mocks "poorly designed blocks of text") but the summary quality problem is hard. **Park until B2 signals land; don't lead with it.**

### C4. One-Command Team Bootstrap  `DEBATE (park behind A1)`
`pharos sync https://.../team-profile.lock` — new machine, one command, entire team MCP context restored, every client configured. Follows A1 + B7; powerful for the "agentic team" future PH investor copy describes; useless until profiles + lockfile export exist.
**Effort:** M (after A1/B7).

### C5. Deterministic Install Receipts  `RECOMMEND (small, on-thesis)`
Every install/remove/update prints (and `--json` emits) a receipt: what changed in which client configs, with file hashes. Machine-checkable side effects, replayable. Extends the safe-write guarantee into an auditable guarantee, perfect prep for enterprise/air-gapped adoption (Chris's own environment is the design target).
**Effort:** S. **Risk:** NONE.

---

## Priority Stack v1 (superseded by Four-Pass Verdict at end of file)

| Rank | Feature | Part | Why this order |
|------|---------|------|----------------|
| 1 | A2 Client import --adopt | A | Removes THE adoption barrier; everything else converts better after it |
| 2 | A1 Profiles | A | Retention model + agent context switching; the feature that makes pharos a daily tool |
| 3 | A7 Agent contract + llm.txt | A | Days of work, permanent agent-native differentiation |
| 4 | C5 Install receipts | C | Days of work, extends safe-write moat, enterprise-ready |
| 5 | A3 Homebrew + Scoop | A | Distribution; GoReleaser makes it cheap |
| 6 | A4 pharos try | A | Trust builder at decision time; reuses daemon |
| 7 | B1 Security scorecard | B | The "npm audit moment" — big, but registry-side work; queue after quick wins |
| 8 | B2 Search signals | B | Cheap trust layer |
| 9 | A6 Source-tracked updates | A | Dry-run + origin metadata |
| 10 | B3 doctor --diff drift detection | B | Integrity polish |
| 11 | C2 Idle-cost budget | C | Philosophy feature; after daemon matures |
| 12 | A5 pharos expose (scoped) | A | Only if remote-server demand shows |
| park | C1 fleet_status, C4 team sync, C3 explain, B4 TUI, B5 GUI, B7 lockfile export | - | Need adoption evidence first |

## Open Questions for the Debate
1. A2 adopt: does pharos.lock's current role (project deps) conflict with a global adopt state? Likely need to separate "global managed state" (~/.pharos) from "project lockfile" cleanly first.
2. A1 profiles vs existing always-write-all-clients behavior: is default changing to profile-scoped writes a breaking change for v1.0 users? (Probably yes → profiles must default to opt-in with a migration prompt.)
3. B1 scorecard: do we have publish-provenance data on the registry side to compute it honestly on day one?
4. GoReleaser/brew: who owns the signing/notarization step for macOS binaries (brew requires stable checksums)?
5. Does C2's process view need anything from daemon.json it doesn't already record? (Likely no — daemon.json already has PID/ports/last-activity.)

---

# Four-Pass Analysis (Sep 1, 2026, TRON — per Chris's gate process)

## PASS 1: Does it enable, encourage, or excite adoption?
Scale: E (enables = removes friction), N (encourages = gives a reason), X (excites = makes people tell others)

| Feature | E/N/X | Adoption verdict |
|---------|-------|------------------|
| A2 import --adopt | E+X | THE onboarding feature. "One command and my existing 9 servers are managed" converts skeptics. Conflict-resolution UX is a screenshot people share |
| A1 Profiles | E+N+X | Enables daily use (context switching), encourages retention, EXCITES: "pharos profile use work" demoing to a coworker is the moment pharos becomes talkable |
| A7 Agent contract + llm.txt | N | Excites exactly one audience, agent builders. That audience is small but loud and on-thesis |
| C5 Install receipts | N (quiet) | Nobody adopts FOR receipts; they adopt because receipts make pharos feel safe. Trust accelerant, not driver |
| A3 Brew + Scoop | E | Removes the "wait, no brew?" objection. Invisible when present, costly when absent |
| A4 pharos try | N+X | The "what does this actually do" answer in 5 seconds. Try-then-trust flow genuinely excites evaluation-minded devs |
| B1 Security scorecard | X | The most SHAREABLE item on the list. "pharos shows a security grade for every MCP server" is a blog post and a Show HN hook. Also positions pharos as the trust layer |
| B2 Search signals | N (quiet) | Table-stakes trust polish. Won't drive adoption, absence would hurt it |
| A6 Source-tracked updates | N (quiet) | Retention/depth item |
| B3 doctor --diff drift | N (quiet) | Delight moment when it saves someone's bacon; rare but remembered |
| C2 Idle-cost budget | X (niche) | "The MCP manager that watches your agent's resources" is a distinct, quotable identity. Niche audience: power users running 10+ servers |
| A5 pharos expose | N | Only excites remote-access users; thin slice today |
| C1 fleet_status | N (agent-side) | Invisible to humans; builds the agent-native story the market will want in 12 months |
| C4 team sync | N (teams) | High value, wrong time; needs profiles first |
| C3 explain | X (if done) | Nice demo, quality risk, consensus was park |
| B4 TUI / B5 GUI | N | Would help non-CLI adoption but big builds; parked |

**Pass 1 conclusion:** adoption drivers are A2, A1, A4, B1 (excite/enable), with A3/A7 as silent enablers. Quiet-trust items (C5, B2, B3, A6) don't drive adoption but protect it.

## PASS 2: Implementation complexity
S = days, M = 1-2 weeks of agent-led work, L = multi-week with design risk

| Feature | Effort | No-brainer? |
|---------|--------|-------------|
| A7 llm.txt + env contract | S | YES. Cheapest permanent item on the list. Do it regardless of everything else |
| C5 receipts | S | YES. Same call: small, safe, compounds the trust story |
| B2 search signals | S | YES. Data already exists server-side |
| A3 brew/scoop via GoReleaser | S-M | YES. Mostly CI config; sign/notarize is the only real work |
| B3 doctor --diff | S-M | Probably yes. Reads configs we already parse; pure addition, no behavior change |
| A6 origin tracking + --check | S-M | Yes, but only AFTER A2 (adopt must record origins correctly from day one) |
| A4 pharos try | M | YES. Daemon + handshake exist; sandbox dir is the only new machinery |
| A2 import --adopt | M | YES but do it carefully — see Pass 3. Highest value-per-effort on the list |
| A1 Profiles | M-L | The hard one. Worth it, but sequence after A2 |
| C2 idle-cost budget | S-M | Cheap given daemon.json; do when daemon surface stabilizes |
| B1 security scorecard | M-L | Registry-side data work + heuristic design. The payoff justifies it but it's a project, not a task |
| A5 expose | M | Defer unless demand appears |
| C1 fleet_status | M | pharos-discovery side, separate codebase; park until CLI v1.1 ships |
| C4 team sync | M (after A1+B7) | Parked by dependency |
| B4 TUI | L | Parked |
| B5 GUI | XL | Parked |

**Pass 2 conclusion:** four S-tier no-brainers regardless of strategy: A7, C5, B2, B3. A3 nearly free via GoReleaser.

## PASS 3: Breakage and design conflicts (the pass that reordered everything)

1. **A1 Profiles vs. current always-write-all-clients behavior** — the big one. Today, install writes the server into every target client. Profiles change the write model. If v1.0 users upgrade and suddenly installs stop appearing everywhere, that reads as breakage. Mitigation: profiles are OPT-IN (no profile = today's behavior, exactly), with a one-time `pharos profile migrate` prompt that offers to wrap existing servers in a "default" profile. Verdict: buildable without breakage IF opt-in is sacred.
2. **A2 import --adopt vs pharos.lock semantics** — `import` currently means "resolve project deps into a lockfile." Adopt means "take ownership of global state in ~/.pharos/mcp.json." Same verb, two models, confusion guaranteed. Resolution: rename the onboarding path to `pharos adopt` (its own verb, its own help text), leave `pharos import` alone. Verdict: no conflict, just naming discipline.
3. **A4 try + sandbox installs vs list/remove** — sandbox servers must be visible (`pharos list --sandbox` or flagged rows) or users get phantom servers. Also: a tried-but-not-installed server must NOT appear in client configs (that's the whole point). Verdict: contained, but `list` needs a state column and `remove` needs to sweep sandbox dirs.
4. **A6 origin tracking vs adopt** — adopted servers have no pharos install origin (user configured them by hand). Origin must record "adopted from <client>" or update --check will misreport. This is why A2 ships before A6. Verdict: sequencing constraint, not conflict.
5. **B1 scorecard vs federated results** — scoring applies cleanly to pharos-registry packages (we have provenance). For mcp.so/Smithery results we only have public metadata; a grade there would be dishonest. Resolution: score ONLY pharos-native packages at v1.1, show "not scored" for federated. Also: a paid-boost result (see D2) displaying next to a security grade makes the grade an ads-adjacent surface — the scorecard must NEVER be influenceable by revenue, or the trust story dies. Hard architectural rule: scoring pipeline is read-only over public provenance, no paid inputs, ever.
6. **C2 budgets vs idle-timeout** — budgets surface aggregate state the daemon already tracks; no conflict, but budget SUGGESTIONS (auto-unload?) must never act without explicit user/agent approval. Approval-gating is already the house style.
7. **B3 drift detection vs safe-writes** — doctor --diff must distinguish "user hand-edited" from "pharos wrote an older version" (we have receipts after C5, which makes drift detection much more precise). Sequence C5 before B3 and B3 gets cheaper and more honest.
8. **D2 paid search slots vs federated search integrity** — the big monetization/design tension, covered in Pass 4 and D2 itself.

**Pass 3 conclusion:** nothing is blocked; three sequencing rules emerge (A2 before A6, C5 before B3, profiles opt-in-only) and one hard law (B1 scoring is revenue-immune).

## PASS 4: Monetization compatibility
Chris's plan: paid boosted slots (top 1-3) in web AND CLI search results, purchasable by any MCP server author even if hosted elsewhere. Boost drives publisher adoption (people pay to be discoverable → some deploy with pharos natively to get richer listings).

| Feature | Monetization fit |
|---------|------------------|
| A2 adopt, A1 profiles | NEUTRAL-positive. More daily-use users = more search queries = more ad inventory. Nothing about them blocks ads |
| A7 agent contract, C5 receipts, B2 signals, B3 drift, A6 updates | NEUTRAL. Infrastructure; ads live in the search layer above them |
| A3 brew/scoop | POSITIVE. Distribution = query volume = inventory |
| A4 try | POSITIVE-subtle. try is the ad-to-trial bridge: a boosted server someone tries and keeps is the ad product working. Also (future) `try` is a natural pay-for-placement-adjacent surface (sponsored try), decide later |
| B1 scorecard | COMPLEX. It's the trust layer that makes paid placement defensible ("boosted, but graded honestly") AND the thing that must never bend to revenue. Keep revenue-immune (Pass 3 #5). Done right, the scorecard is why advertisers CAN sell on pharos: an A-grade boosted listing is worth paying for. The scorecard creates ad value without being for sale |
| D1/D2 (below) | See new items |

**Verdict: nothing on the list inhibits monetization.** The search-results ad surface sits above all of these. Two features actively strengthen it (A3 distribution, B1 trust), one is its bridge (A4).

---

# NEW ITEMS FROM THE MONETIZATION DISCUSSION

## D1. Sponsored/Boosted Search Slots  `RECOMMEND — build the plumbing NOW, sell later`
Chris's design, refined:
- **Server side (registry):** `boosts` table (package, active window, bid/order, budget cap), search API accepts `?boosted=1` merging paid + organic; paid entries carry `sponsored: true` + label. Cap: max 2 boosted slots (not 3 — two reads as curation, three reads as ads). Same relevance floor as organic: a boosted result must actually match the query, or it doesn't show. Non-matching boosts are the fastest way to poison trust in a dev-tool ad product.
- **CLI side:** boosted entries render in `pharos search` with a subtle marker (a `*` or `[boosted]` tag at line end), and `pharos info` already links out. CLI rendering stays minimal — the web is the sales surface, the CLI is the integrity surface.
- **Web side:** proper labeled slots at top of results; self-serve purchase flow (Stripe) is a later milestone. v1.1 only needs the plumbing: schema, API merge, CLI rendering, and the label rules.
- **Cross-registry boosts (Chris's key insight, keep it):** authors can boost a server that resolves from mcp.so/Smithery etc. The boost is pharos inventory, not pharos hosting — that's the wedge that gets publishers INTO the ecosystem (they arrive paying for discovery of an externally-hosted server; converting them to native publishing is then a product conversation, not a sales one).
- **Trust rules (non-negotiable, learn from npm/Google):** labeled always, relevance-floor always, no boost influence on B1 grades, organic results never reordered by payment, frequency cap so a query doesn't return 2 boosts + 8 ads-shaped results.
- **Analytics product (later):** boost purchasers get impression/click/install funnels. Install attribution via receipts (C5) is a genuinely unique ad metric nobody in the category can offer. C5 quietly becomes the ad platform's measurement layer — one more reason it's a no-brainer.
**Effort:** M (plumbing) / L (self-serve + analytics). **Sequencing:** plumbing in v1.1 backend; selling waits for search volume.

## D2. Other monetization strategies considered (debated, for the record)

| Strategy | Verdict | Reasoning |
|----------|---------|-----------|
| **Verified publisher badge (annual fee)** | GOOD #2 | Server authors pay for a verified checkmark + enhanced listing (logo, docs embed). Pairs with B1: verification feeds the scorecard's provenance signal honestly. Recurring revenue, dev-tool-native, npm/GitHub precedent |
| **Team tier (C4 team sync + shared profiles)** | GOOD #3 (later) | Free solo forever; paid team features (shared profile bundles, centralized policy: "these 5 servers are approved for everyone"). Natural home for C5 receipts (compliance artifact). Monetizes AFTER adoption via profiles; don't gate core CLI |
| **Private registry hosting** | GOOD #4 (much later) | Orgs pay to run pharos-registry privately (air-gapped enterprises). Chris's own deployment is the reference. Real business, heavy support surface; revisit at scale |
| **API rate-limit tiers for discovery/registry API** | MEH | Free tier generous, paid for high-volume programmatic access. Risks taxing exactly the agent-native usage we're courting. Only touch if abuse appears |
| **Registry-side "featured server of the week" (editorial, paid)** | MEH | Blurs ads/editorial line; low revenue; skip unless sponsors (not ads) want it |
| **Paid priority publish review / onboarding help** | NO | Chris's values: publishing stays free and open; pay-for-review corrupts the supply side |
| **Tipping/sponsorships (GitHub Sponsors)** | ALREADY DECIDED | Sponsors + contact email only per prior decision; not a strategy, a courtesy |
| **Selling aggregated data/telemetry** | NO | Violates the privacy-first brand Chris is building. Never |
| **Paid CLI features (pro tier)** | NO | Fragments the open-source story and hands mcpm.sh the "forever free" flag they already wave |

**Monetization stack (recommended order):** D1 boosted slots (plumbing now) → D2-verified badge (once publishers exist) → team tier (once profiles + adoption exist) → private registry (enterprise demand appears). All later stages feed the flywheel without gating the free core.

---

# FINAL VERDICT — Build Order (four-pass + monetization integrated)

| Wave | Items | One-line rationale |
|------|-------|--------------------|
| **Wave 1 (S-tier, do immediately)** | A7 agent contract + llm.txt, C5 receipts, B2 search signals, B3 doctor --diff | All small, all safe, all compounding; receipts make drift detection precise; signals + llm.txt are pure addition |
| **Wave 2 (adoption core)** | A2 pharos adopt (renamed from import), then A1 profiles (opt-in) | The onboarding barrier remover, then the retention hook. Ships together as "v1.1 adoption release" |
| **Wave 3 (distribution + decision-time)** | A3 brew/scoop (GoReleaser), A4 pharos try | Brew is CI config; try is the trust bridge at decision moment |
| **Wave 4 (trust platform)** | B1 security scorecard (revenue-immune by law) + D1 boost plumbing (schema, API merge, CLI labels) | Scorecard first: the grade must exist before anything paid renders next to it. Boost selling starts only when search volume justifies it |
| **Wave 5 (opportunistic)** | A6 origin/update --check (after A2), C2 budgets (after daemon stabilizes), A5 expose (if demand) | Depth items |
| **Parked** | C1 fleet_status, C4 team sync (needs A1), C3 explain, B4 TUI, B5 GUI, B7 lockfile export | Revisit on adoption evidence |

## Wave 6: Metrics + Monetization System (Chris addition)

**What it is:** the operational layer that turns Wave 4's plumbing into a business. Concretely:

1. **Measurement pipeline (build with/after D1 plumbing)**
   - Impression + click tracking on web search results and boosted slots (first-party, no third-party trackers — privacy brand)
   - Install attribution via C5 receipts: boosted server → receipt → install event. The funnel advertisers can't get anywhere else: impression → click → try → install → active (daemon last-activity gives "still using it" — nobody in the category has retention signal)
   - CLI-side: anonymous, opt-out-able search analytics (query → results shown → which result the user acted on). Must be transparent and off-switchable or the privacy-first story contradicts itself
2. **Boost campaign surface (self-serve, after volume)**
   - Publisher dashboard: create boost (package, budget, date window), see funnel metrics, renew
   - Stripe checkout, weekly/multi-week windows, auto-expiry
   - The receipt-based "installs delivered" number is the headline metric sellers see
3. **Billing + bookkeeping**
   - Stripe as system of record for charges; minimal ledger table registry-side for active boosts
   - Tax handling deferred until revenue justifies it (Stripe Tax handles it when needed)
4. **Guardrails (carry the trust laws down)**
   - Boost metrics are aggregate-only; never resold, never fed into B1 scores
   - Analytics never per-user; all surfaces are per-campaign or fully anonymized
   - Free tier of measurement for ALL publishers (not just boost buyers) — generosity here seeds the verified-badge and boost upsells later

**Effort:** M-L. **Sequencing law:** measurement pipeline can be built alongside Wave 4 plumbing, but NO selling surfaces until (a) B1 scorecard exists and (b) search volume makes a boost worth buying. Selling into an empty store kills the product before it starts.

**Why Wave 6 and not Wave 4:** Wave 4 builds the *schema and integrity rules* (what a boost IS). Wave 6 builds the *business* (measurement, dashboards, billing, self-serve). Keeping them separate means monetization work never delays product work, and the business layer can iterate without touching the search core.

Sequencing laws (from Pass 3): A2 before A6. C5 before B3. B1 before D1 selling. Profiles opt-in or don't ship. Scoring never takes a paid input.

