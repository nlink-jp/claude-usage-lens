# AGENTS.md — claude-usage-lens

## What this is

A util-series CLI that parses **Claude Code** and **Claude Cowork** local session
logs to collect token usage and compute an API **list-price-equivalent** cost,
accumulate it in a durable SQLite store, and report it by day / session / project
/ model. Personal-local scope first; designed for a future Wails GUI to reuse the
same core.

Current state: **Phase 2 complete** — every CLI command works end-to-end:
`ingest`, `reprice`, `report` (period analysis), `sessions`, `calibrate` /
`limits` (real-quota calibration, ADR-0001), `models`, `verify`, `doctor`,
`watch` (poll + incremental ingest, live deltas), `daemon` (macOS launchd). All
core packages tested. Phase 3 (GUI) shipped as the separate
`claude-usage-lens-gui` (native SwiftUI, not Wails).

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
  config/               optional config.toml: [sources] + [pricing.models] [tested]
  pricing/             rate table + tier/cache multipliers (self-contained)
  cost/                 pure cost engine  [tested]
  collect/              ParseFile/ParseFrom, Discover, Dedup, 429 limit events [tested]
  ingest/               collect → dedup → price → store orchestration
  aggregate/            group-by roll-up + sort/summary [tested]
  limits/               calibration math: window cadence, cap derivation [tested]
  store/                SQLite persistence (modernc.org/sqlite) [tested]
  audit/                parse Cowork audit.jsonl ground-truth cost [tested]
  platform/             build-tagged OS paths: paths_{darwin,windows,linux}.go [tested]
docs/{en,ja}/           RFP (canonical design)
docs/adr/               ADRs (0001 = real-quota calibration)
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
  Overridable via `[sources]` / `--code-root` / `--cowork-root`; `doctor` verifies resolution.
- **Prices are not hardcoded from memory** — fetch current published prices (see
  `/claude-api`) when populating `pricing.Default()`.
- **Config: never document a setting nothing reads.** `[sources]` and
  `[pricing.models]` were documented in `config.example.toml` and both READMEs for
  a long time before `core/config` existed, so users could write a config file
  that did nothing. If you add a knob to `config.example.toml`, add the field, the
  merge rule, and a test in the same commit. Corollaries baked into the loader:
  rate-override fields are `*float64` so a partial override inherits instead of
  zeroing (a plain float would silently blank the cache multipliers), unknown TOML
  keys are a hard error, and `doctor` reports whether the file loaded plus which
  values it changed. Deviations from the RFP, deliberate: the flags are
  `--code-root` / `--cowork-root` (one `--source-root` cannot address two distinct
  roots), and **env-var overrides are not implemented** — flags plus the file
  cover the safety-valve need, so a third precedence layer has no current use case.
- **A model missing from the table costs $0, silently** — that is intended for
  `<synthetic>` and a bug for everything else (it is how `claude-opus-5` reported
  $0). Two guards exist, keep them working: `ingest`/`reprice` warn about billable
  models absent from the table, and **`reprice`** recomputes stored `code` rows
  from their token columns. Adding a model is therefore a two-step fix — update
  `pricing.Default()`, then `reprice` — because ingest is incremental and never
  re-reads already-consumed bytes. `reprice` must never touch `cowork` rows.
- **Fast mode is a price tier, not a model** — `speed: "fast"` bills $10/$50 on
  Opus 5 / 4.8 only. `Rates.Base(speed)` picks the pair and the cache multipliers
  apply on top of it, so a fast cache read is 0.1× the *fast* input price. A
  fast-flagged record on a model with no fast tier falls back to standard, which
  is what the API does too (Opus 4.6 serves the request at standard rates; Opus
  4.7 rejects it). Batch and fast are mutually exclusive at the API level, so
  those two multipliers never stack.
- **Schema changes need a `migrate` entry, not just a `schema` edit** —
  `CREATE TABLE IF NOT EXISTS` leaves an existing store alone, so a new column
  must also go in `store.addedColumns` (idempotent `ALTER TABLE`, run on every
  `Open`). Reads of a migrated column need `COALESCE`, since old rows are NULL.
  A migration cannot backfill from the transcripts: ingest is incremental and the
  bytes are already consumed. `speed` is the worked example. (Whole new *tables*
  — `limit_events`, `calibrations` — are fine in `schema` alone: IF NOT EXISTS
  creates them on an old store.)
- **The real quota is not collectable; only calibratable (ADR-0001).** No local
  file carries utilization/reset state, and the OAuth `/api/oauth/usage`
  endpoint is rejected (private, unstable, aggressively rate-limited). Do not
  reintroduce it. `calibrate` stores the user's official `/usage` reading;
  `limits` derives caps at query time (`consumption ÷ %` on both bases) so
  `reprice`/late ingest refine them. Caps are *local-visible* — claude.ai usage
  is absorbed by the calibration as long as the mix is stable. 429 events from
  transcripts land in `limit_events` with the `rateLimits` payload verbatim;
  the payload has never been observed populated, so interpret nothing from it.
  `limits --json` (`calibrated: false` ⇒ fall back) is the GUI's contract —
  change it in lockstep with `claude-usage-lens-gui`.

## Testing strategy

- Unit tests use synthetic fixtures / rates (PII-free, Secret-Scanning-safe).
- The RFP's "validation harness" shipped instead as the **`verify` command**
  (reusable, commits no real data): it cross-checks computed cost against Cowork
  `audit.jsonl` `total_cost_usd` on the user's own machine. Aggregate agrees
  within ~5% on the author's data — outlier sessions are follow-up targets.

## Design reference

- [docs/ja/claude-usage-lens-rfp.ja.md](docs/ja/claude-usage-lens-rfp.ja.md) (primary)
- [docs/en/claude-usage-lens-rfp.md](docs/en/claude-usage-lens-rfp.md)
