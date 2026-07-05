# AGENTS.md — claude-usage-lens

## What this is

A util-series CLI that parses **Claude Code** and **Claude Cowork** local session
logs to collect token usage and compute an API **list-price-equivalent** cost,
accumulate it in a durable SQLite store, and report it by day / session / project
/ model. Personal-local scope first; designed for a future Wails GUI to reuse the
same core.

Current state: **Phase 2 complete** — every CLI command works end-to-end:
`ingest`, `report` (period analysis), `sessions`, `models`, `verify`, `doctor`,
`watch` (poll + incremental ingest, live deltas), `daemon` (macOS launchd). All
core packages tested. Phase 3 = a Wails GUI over the same `core/`.

`watch` uses polling (not fsnotify) — simpler and robust against deep,
dynamically-created session trees; no new dependency. Scheduler code is per-OS
behind build tags (`core/platform/scheduler_{darwin,other}.go`); only darwin is
implemented (launchd) — Windows/Linux return ErrDaemonUnsupported by design.

## Build & test

```sh
make build      # → dist/claude-usage-lens  (NEVER `go build` directly)
make test       # go test ./...
make vet        # go vet on host + GOOS=windows + GOOS=linux
make build-all  # cross-compile all platforms, CGO-free
```

Go 1.26+. No CGO, no external services, no network at runtime.

## Structure

```
main.go                 thin entry → cmd.Execute
cmd/                    stdlib-flag CLI dispatch; doctor implemented, rest stubbed
core/                   reusable, OS-neutral core (imported by CLI and future GUI)
  model/                types + ErrNotImplemented
  pricing/             rate table + tier/cache multipliers (self-contained)
  cost/                 pure cost engine  [tested]
  collect/              ParseFile/ParseFrom, Discover, Dedup [tested]
  ingest/               collect → dedup → price → store orchestration
  aggregate/            group-by roll-up + sort/summary [tested]
  store/                SQLite persistence (modernc.org/sqlite) [tested]
  audit/                parse Cowork audit.jsonl ground-truth cost [tested]
  platform/             build-tagged OS paths: paths_{darwin,windows,linux}.go [tested]
docs/{en,ja}/           RFP (canonical design)
```

## Conventions & deliberate choices (gotchas)

- **`core/` is top-level, not `internal/`** (diverges from gem-summary). Deliberate:
  the RFP requires the collection core be importable by a future GUI. Keep it
  dependency-light and OS-neutral.
- **Pure-Go SQLite (modernc.org/sqlite), NOT mattn/go-sqlite3.** Unlike
  `json-to-sqlite` (CGO, needs Podman to cross-compile), this tool must cross-build
  CGO-free for Windows/Linux. Do not introduce CGO.
- **Path handling is OS-neutral.** Always use `path/filepath` (never hardcode `/`).
  Source roots come only from `core/platform`. Never decode the `<encoded-cwd>`
  directory name — use the in-record `cwd`/`sessionId` fields (OS-independent).
- **Dedup by `message.id`** globally — session resume/fork duplicates the same
  assistant messages across files. The store's `usage_records.message_id` is the
  PRIMARY KEY (idempotent upsert). Never double-count the `iterations[]` array.
- **`<synthetic>` model is free** — excluded from the rate table → zero cost.
- **Cache tiers matter**: `ephemeral_1h` vs `ephemeral_5m` have different
  multipliers; `cache_read` ≠ `cache_creation`. Service tier (batch) scales the total.
- **Costs are notional** (API list-price equivalent), not a real bill. Say so in UI.
- **Dual cost source by origin** (see `core/ingest`): `code` is transcript-computed
  (notional, ~5% — omits internal helper calls, may over-count replays); `cowork` is
  taken from `audit.jsonl` (`ParseRecords`, exact). Don't "unify" them — the audit is
  the right source for cowork, our table for code. Changing this needs a `usage.db`
  rebuild (old transcript-based cowork rows would double-count new audit rows).
- **Durability**: `ingest` must never delete store rows when a source file
  disappears (Claude Code auto-deletes old sessions — that's the data-loss we guard).
- **Windows/Linux are experimental** — paths inferred, unverified on real hardware.
  Overridable via `[sources]` / `--source-root`; `doctor` verifies resolution.
- **Prices are not hardcoded from memory** — fetch current published prices (see
  `/claude-api`) when populating `pricing.Default()`.

## Testing strategy

- Unit tests use synthetic fixtures / rates (PII-free, Secret-Scanning-safe).
- The RFP's "validation harness" shipped instead as the **`verify` command**
  (reusable, commits no real data): it cross-checks computed cost against Cowork
  `audit.jsonl` `total_cost_usd` on the user's own machine. Aggregate agrees
  within ~5% on the author's data — outlier sessions are follow-up targets.

## Design reference

- [docs/ja/claude-usage-lens-rfp.ja.md](docs/ja/claude-usage-lens-rfp.ja.md) (primary)
- [docs/en/claude-usage-lens-rfp.md](docs/en/claude-usage-lens-rfp.md)
