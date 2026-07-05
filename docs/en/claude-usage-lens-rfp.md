# RFP: claude-usage-lens

> Generated: 2026-07-05
> Status: Draft

## 1. Problem Statement

The user relies daily on Claude Code (terminal CLI, desktop app, Python SDK) and
Claude Cowork (local-agent-mode), but in environments where Anthropic's Console /
billing API is unavailable, there is no way to get a cross-cutting view of token
usage and cost. However, empirical inspection confirmed that **these tools leave
local session logs (JSONL) that embed per-model, per-token-type usage
(`message.usage`)**.

`claude-usage-lens` parses these local logs to collect and compute token usage and
cost (list-price equivalent), aggregating by day / session / project / model. Because
source sessions are auto-deleted (Claude Code's `.last-cleanup`), ingested data is
**accumulated into a durable store** to prevent data loss. The initial scope is
personal, single-machine use, with a core design that anticipates future
multi-machine aggregation and a GUI. Target user: an individual developer who wants
near-real-time visibility into their own Claude usage and cost.

## 2. Functional Specification

### Commands / API Surface

Single binary + subcommands (designed so a future GUI can co-reside as a subcommand).

```
claude-usage-lens ingest           # incremental idempotent load: read only new/changed data, upsert into store
    --source code|cowork|all        #   (default all)

claude-usage-lens report            # aggregate from store (fast). Un-ingested tail is auto folded-in for freshness
    --since <date|"7d"|"today">     #   time filter
    --until <date>
    --group-by day|session|project|model|entrypoint   # repeatable (default day)
    --source code|cowork|all        #   (default all)
    --entrypoint cli|claude-desktop|sdk-py             # optional filter
    --breakdown                     #   expand input/output/cache-read/cache-1h/cache-5m
    --json                          #   machine-readable output (for piping)

claude-usage-lens sessions          # session list (id/project/model/tokens/cost/time)
claude-usage-lens models            # show current pricing table + schema/price drift detection
claude-usage-lens doctor            # diagnose resolved source-root/store/config paths, existence, file counts (cross-OS verification)

claude-usage-lens watch             # [Phase2] continuous ingest via fsnotify (resident, near-realtime, all OSes)
claude-usage-lens daemon install    # [Phase2] register periodic ingest with the OS scheduler (macOS: launchd / Windows: Task Scheduler / Linux: systemd|cron)
```

### Input / Output

**Input (local file reads only, no writes to sources)**

| Source | Path | Content |
|--------|------|---------|
| `code` | `~/.claude/projects/<encoded-cwd>/<sessionId>.jsonl` | `message.usage` on `assistant` records. `entrypoint` ∈ {cli, claude-desktop, sdk-py} |
| `cowork` (transcript) | `~/Library/Application Support/Claude/local-agent-mode-sessions/**/outputs/*.jsonl` (+ `subagents/agent-*.jsonl`) | **Identical schema** to `code` |
| `cowork` (audit) | `.../local-agent-mode-sessions/**/audit.jsonl` | Pre-computed `total_cost_usd` / `modelUsage` / `usage` (HMAC-signed) → **validation ground truth** |

Observed `usage` schema:
```
input_tokens, output_tokens
cache_creation_input_tokens, cache_read_input_tokens
cache_creation: { ephemeral_1h_input_tokens, ephemeral_5m_input_tokens }
server_tool_use: { web_search_requests, web_fetch_requests }
service_tier            # standard | priority | batch
model                   # claude-opus-4-8 | claude-fable-5 | <synthetic> ...
requestId / message.id  # dedup keys
```

**Output**: formatted table (stdout) by default, structured JSON via `--json`,
following util-series' pipe-friendly convention.

### Configuration

- config: `<OS-standard config dir>/claude-usage-lens/config.toml` (sectioned TOML, per existing config convention)
  - `[pricing]` … override per-model rates/multipliers (overrides bundled default table)
  - `[sources]` … override source paths / profile roots (defaults to per-OS standard paths; **a safety valve to fix inferred paths without recompiling**)
- Durable store: `<OS-standard data dir>/claude-usage-lens/usage.db` (SQLite)
- Path resolution follows OS conventions (resolved by `core/platform`):
  - config: macOS `~/Library/Application Support/` / Linux `~/.config` (XDG) / Windows `%APPDATA%`
  - data (store): macOS `~/Library/Application Support/` / Linux `~/.local/share` (XDG) / Windows `%LOCALAPPDATA%`
  - also overridable via env var / `--source-root` flag

### External Dependencies

- External API / service dependencies: **None** (fully local, file reads only)
- Libraries: `modernc.org/sqlite` (pure-Go, no CGO), `fsnotify` (Phase2)
- Credentials: none

## 3. Design Decisions

- **Language / framework**: Go — single static binary, `fsnotify` for near-real-time
  watch, testability via pure functions, and easy notarization (avoiding CGO).
- **Cross-platform design (macOS-first / Windows・Linux support)**: since the stack
  (Go + pure-Go SQLite + fsnotify) is fully cross-platform, the analysis core
  (parse/cost/aggregate/store) is kept entirely OS-neutral. OS variance is confined to
  two surfaces — "① path separator & profile/home structure" and "② scheduler for
  periodic runs" — isolated behind build tags. See "Cross-platform support" below.
- **Independent core module**: collect → parse → dedup → pricing → cost → aggregate →
  store are consolidated into `core/` packages, all pure functions + dependency
  injection. Both the CLI and a future Wails GUI are thin adapters importing `core/`
  (the "GUI co-resides with CLI subcommands" pattern).
- **Store engine = SQLite (modernc.org/sqlite, pure-Go)**: single static binary and
  easy notarization (avoids the go-duckdb + Wails arrow issue). SQL aggregation is
  sufficient at this scale. DuckDB (proven in gem-query / data-toolbox-mcp) was a
  candidate but SQLite was chosen for lightweight, single-binary priority.
- **Cost semantics**: held as `Cost{ ListPriceUSD, Tier }`; for subscription (Max/Pro)
  users, both README and output make clear this is a "list-price equivalent (notional
  cost)", not the actual billed amount. Rates are not filled from memory — current
  pricing is fetched at implementation time and baked into the default table (mind drift).
- **Complements existing nlink-jp tools**: data-analyzer (large JSON/JSONL analysis),
  llm-cli, gem-query (DuckDB analysis CLI). Differentiated by the local-log-parsing +
  list-price-equivalent angle.
- **Explicitly out of scope**:
  - Reconciliation against actual billing (Console / Billing API integration) — list-price only
  - Multi-machine / team aggregation is out of phase scope, but `UsageRecord.Source` /
    `Host` fields are present from the start to allow later extension (export → central rollup)
  - Non-Claude LLM providers (claude-only)

### Package layout
```
_wip/claude-usage-lens/
  cmd/claude-usage-lens/main.go   # CLI entry (subcommand dispatch, GUI co-residence in mind)
  core/
    model/     types.go           # UsageRecord{ Source, Entrypoint, Host, ... }, Cost
    collect/   discover.go        # enumerate jsonl across 3 source roots (tag source/entrypoint)
               parse.go           # jsonl → UsageRecord (normalize at boundary; skip unknown fields leniently)
               dedup.go           # global dedup by message.id
    pricing/   table.go           # per-model rates (embedded default)
    cost/      cost.go            # UsageRecord × pricing → Cost (pure function)
    aggregate/ aggregate.go       # by day/session/project/model/entrypoint
    store/     store.go           # SQLite: usage_records / ingest_state, idempotent upsert / incremental load
    platform/  paths.go           # OS-neutral IF: SourceRoots()/DataDir()/ConfigDir()
               paths_darwin.go    # //go:build darwin  — ~/.claude, ~/Library/Application Support/Claude
               paths_windows.go   # //go:build windows — %USERPROFILE%\.claude, %APPDATA%\Claude
               paths_linux.go     # //go:build linux   — ~/.claude, ~/.config, ~/.local/share
               scheduler_*.go     # [Phase2] launchd / Task Scheduler / systemd branched by build tag
```

### Durable store schema (essentials)
- `usage_records(message_id PRIMARY KEY, ts, source, entrypoint, host, session_id, project, model, service_tier, input_tokens, output_tokens, cache_read, cache_1h, cache_5m, web_search, web_fetch, cost_usd, ingested_at)`
  - `message_id` as primary key → idempotent upsert = global dedup. `iterations[]` must
    not be double-counted against the top-level usage. `<synthetic>` is excluded.
  - **Durability guarantee**: `ingest` never deletes store rows when a source file later
    disappears (insurance against Claude Code's session auto-deletion).
- `ingest_state(path PRIMARY KEY, size, mtime, last_offset, updated_at)`
  - incremental load reading only bytes past the last offset.

### Cross-platform support (macOS-first / Windows・Linux by design)

macOS is first-class; Windows / Linux are built as "supported by design, unverified on
real hardware (experimental)". Because a test environment is hard to arrange, the design
absorbs inferred-path mismatches operationally.

- **Path separators unified via `path/filepath`** — no hardcoded `/`; all path ops go
  through `filepath.Join` / `Glob` / `WalkDir`, absorbing Windows `\` vs macOS/Linux `/`.
- **Profile/home structure variance isolated behind build tags** — `core/platform`'s
  `paths_darwin.go` / `paths_windows.go` / `paths_linux.go` return per-OS source roots;
  the core sees only this interface:

  | Purpose | macOS | Linux | Windows (inferred, needs real-HW verification) |
  |---------|-------|-------|-----------------------------------------------|
  | code | `~/.claude/projects/` | `~/.claude/projects/` | `%USERPROFILE%\.claude\projects\` |
  | cowork | `~/Library/Application Support/Claude/local-agent-mode-sessions/` | Linux equivalent | `%APPDATA%\Claude\local-agent-mode-sessions\` |

- **No dependence on directory-name decoding** — the `<encoded-cwd>` encoding is OS-dependent
  (Windows drive letters `C:`, `\`). Directory names are not interpreted; instead the JSONL
  records' `cwd` / `sessionId` / `project` fields are used → the parser is fully OS-neutral.
- **CRLF handling** — trim trailing `\r` after line splitting.
- **SQLite in WAL mode** — safe concurrent watch + report on all OSes.
- **Mitigations for the unverified risk**: ① override inferred paths via `[sources]` / env var /
  `--source-root`; ② the `doctor` subcommand prints resolved paths, existence, and file counts,
  turning real-HW verification into one command; ③ README labels Windows experimental. WSL users
  use the Linux build.

## 4. Development Plan

### Phase 1: Core
- Implement `core/` (model / collect / parse / dedup / pricing / cost / aggregate / **store**)
- Commands: `ingest` / `report` / `sessions` / `models`
- Tests:
  - Synthetic fixtures (known token counts, PII-free / Secret-Scanning-safe form) → assert expected cost
  - **Validation harness (gitignored, local-only)**: cross-check own computation vs Cowork
    `audit.jsonl` `total_cost_usd` to prove the pricing model
- Docs: README.md / README.ja.md / CHANGELOG.md / AGENTS.md
- Build matrix: darwin(amd64/arm64) first-class; windows(amd64/arm64) and linux(amd64/arm64)
  also cross-built without CGO. Windows/Linux binaries are distributed labeled experimental.
- **Independently reviewable**: core computation and store persistence can be reviewed on their own.

### Phase 2: Features (near-real-time)
- `watch` (incrementally monitor latest jsonl via fsnotify → continuous ingest)
- `daemon install` (generate/register a launchd plist for periodic ingest)
- Separate the logic (idempotent `ingest`) from the scheduler (launchd) to keep testability.
- **Independently reviewable**: the watch/scheduling layer can be reviewed after Phase 1.

### Phase 3: Release (GUI)
- A Wails v2 GUI imports `core/` + `store/` as-is; shows near-real-time graphs/tables.
- Distributed as a Developer ID signed + notarized `.app`.
- Release follows the nlink-jp checklist (zip + bundled README, umbrella submodule update,
  org profile / web catalog sync, check-org.sh).

## 5. Required API Scopes / Permissions

**None (no external API dependency).**

- Fully local file reads only. No OAuth scopes, API keys, or IAM roles required.
- No macOS special permission (e.g. Full Disk Access) required — the read targets are
  under the user's own home (`~/.claude/`, `~/Library/Application Support/Claude/`) and
  readable with normal permissions.
- Windows / Linux likewise require no special permission — reads are confined to the user's own profile.

## 6. Series Placement

Series: **util-series**

Reason: a pipe-friendly data-processing CLI that takes local JSONL logs as input and
emits aggregated results as a formatted table / `--json` to stdout — matching util-series
(pipe-friendly data transformation and processing CLIs). It sits alongside data-analyzer /
gem-query / llm-cli with a distinct niche.

## 7. External Platform Constraints

- External API rate limits / UI rendering constraints: **None** (no external platform dependency).
- **Single, largest constraint = schema drift**: the internal JSONL / audit formats of
  Claude Code / Cowork are undocumented and may change without notice. Inspection showed
  `entrypoint` takes three values {cli, claude-desktop, sdk-py}, revealing that
  `~/.claude/projects/` is not CLI-only but **dominated by the desktop app's Code** — itself
  evidence of the undocumented nature of the format.
  - Mitigation: the parser skips unknown fields leniently and warns-and-skips records missing
    required fields. The `models` subcommand and validation harness surface drift (new models,
    schema changes, price changes).
- **Pricing-table drift**: per-model rates can change, so config.toml allows overrides; the
  bundled default bakes in the current price at implementation time.
- **Windows / Linux paths & profile structure are inferred (unverified on real hardware)**: the
  `code` source (`%USERPROFILE%\.claude`) very likely exists, but `cowork` (assumed `%APPDATA%\Claude`,
  possibly `Anthropic\Claude`), and whether local-agent-mode even exists in the Windows desktop app,
  are unconfirmed. Mitigated by build-tag path isolation + `[sources]`/`--source-root` override +
  `doctor` diagnostics; Windows is treated as experimental. Path-separator variance is absorbed by `filepath`.

---

## Discussion Log

- **Origin**: Discovered that even without Console / billing API, local session-log parsing
  can collect token usage and cost. Assumed to apply to both Claude Code and Claude Cowork.
- **Facts established via real-data inspection**:
  - `code` source = `~/.claude/projects/<encoded-cwd>/<sessionId>.jsonl`. `assistant` records'
    `message.usage` carry the full token breakdown (input/output/cache-creation(1h,5m)/
    cache-read/server_tool_use/service_tier/model). Cost is not included → compute it.
  - `cowork` source = `local-agent-mode-sessions/**/outputs/*.jsonl` shares the **identical
    schema** with `code` (including subagents). One parser handles both.
  - Cowork's `audit.jsonl` has **pre-computed** `total_cost_usd` / `modelUsage` (HMAC-signed) →
    usable as **ground truth** for own computation, central to the test strategy.
  - Observed volume: `code` 156 files / ~50MB. `entrypoint` breakdown:
    claude-desktop 6577 / cli 4984 / sdk-py 248.
- **Design evolution**:
  - Initially labeled `source=cli`, but inspection showed `~/.claude/projects/` is dominated by
    the desktop app's Code; corrected to `source=code` (+ `entrypoint` sub-dimension).
  - Given "cross-cutting re-aggregation gets slow" and "source sessions auto-delete → data loss",
    a **durable store (SQLite) + incremental ingest** was promoted to the Phase 1 core.
    message_id primary key gives idempotent upsert / global dedup, with a durability guarantee
    that rows survive source disappearance.
  - DuckDB was a candidate (in-house track record) but single-binary / notarization ease
    (avoiding the Wails + go-duckdb arrow issue) favored **modernc.org/sqlite (pure-Go)**.
  - Cost is displayed as a "list-price equivalent (notional)", explicitly not the actual bill.
  - Decided to design in Windows / Linux support too. Because a test environment is hard to arrange,
    it is positioned as "supported by design, unverified on real hardware (experimental)": path
    separators unified via `filepath`, profile/home structure variance isolated behind `core/platform`
    build tags, no dependence on directory-name decoding (use in-record `cwd`/`sessionId`), inferred
    paths absorbed via `[sources]`/`--source-root` override and `doctor` diagnostics.
- **Confirmed**: name `claude-usage-lens` / Go · util-series / independent collection core /
  CLI first → watch → Wails GUI / personal-local first (Source · Host leave room to extend) /
  store = SQLite (pure-Go) / cross-platform design (macOS-first, Windows・Linux experimental).
