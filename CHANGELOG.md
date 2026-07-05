# Changelog

All notable changes to this project are documented here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Fixed
- Web-search cost is now priced at $0.01/request ($10 per 1,000 searches, per
  Anthropic's pricing) instead of $0. This closes the last reconstruction gap —
  e.g. a session's haiku cost that looked ~25% high was exactly its 52 web
  searches. (Web fetch remains free.) Re-price existing rows by rebuilding the
  store (`rm usage.db`); impact on `code` is small (web search is usually absent).

### Verified
- Pricing checked against Anthropic's live pricing page (2026-07-05): **no
  long-context premium** — all current models include the 1M context window at
  standard per-token pricing, so the flat per-model table and the `[1m]`→base
  normalization are correct (confirmed empirically: `claude-opus-4-8[1m]`
  reconstructs at exactly $5/$25). Cache multipliers (5m 1.25× / 1h 2× /
  read 0.1×) and base rates all match.

### Changed
- **Cowork cost is now exact.** `ingest` sources Cowork cost straight from its
  `audit.jsonl` (Anthropic's `total_cost_usd` / `modelUsage`) instead of computing
  from the transcript, so the Cowork total matches to the cent and includes
  internal helper calls (e.g. haiku for titles) the transcript omits. Claude Code
  (`code`) remains transcript-computed (no audit log); `verify` measures that gap.
  Model variant tags like `[1m]` are normalized to the base model for grouping.
  **Migration: delete `usage.db` once** so Cowork rows re-ingest from audit
  (old transcript-based Cowork rows would otherwise double-count).

### Added
- `audit.ParseRecords` — turn an audit.jsonl into priced records (one per
  result-event × model), keyed by `uuid:model` for idempotent upsert. Tested.

## [0.1.0] - 2026-07-05

First release. A pipe-friendly CLI that parses Claude Code / Cowork local
session logs to collect token usage and compute API list-price-equivalent
(notional) cost, accumulate it in a durable SQLite store, and report it by
day / session / project / model — with near-real-time `watch`, period analysis,
and `verify` against Cowork's own audit ground truth.

### Added (Phase 2 — near-real-time)
- `watch` — poll the sources on an interval, incrementally ingest each tick, and
  print live cost deltas; graceful Ctrl-C shutdown. (Polling, not fsnotify:
  simpler and robust against deep, dynamically-created session trees; no new dep.)
- `daemon install|uninstall|status` — periodic-ingest service via macOS launchd
  (`--dry-run` previews the plist). Windows/Linux report unsupported with a
  pointer to schedule `ingest` via the native scheduler. Per-OS via build tags in
  `core/platform` (scheduler_darwin.go / scheduler_other.go).

### Added (validation — Phase 1 step 6)
- `core/audit` — parse Cowork audit.jsonl ground-truth cost (result events'
  `total_cost_usd` + per-model `modelUsage`).
- `verify` command — cross-check computed cost vs audit per session, with
  Δ/Δ%. On the author's data, aggregate agrees within ~5% (one session exact).
- `pricing.Lookup` normalizes variant tags like `[1m]` (1M-context) to the base
  alias, alongside dated-snapshot suffixes.

### Added (analysis features)
- `report` time granularity: `--group-by hour|week|month` (in addition to day).
- `report --sort key|cost|input|output|records|cache` + `--top N`.
- `report --summary` — period stats: active days, daily average, peak day, and
  a 30-day cost projection.
- `report --compare` — this period vs the preceding equal-length period (Δ, Δ%).
- `report --model` / `--project` substring filters.

### Added (Phase 1 — working pipeline)
- `pricing.Default()` — current per-model rates (2026-07-05) with cache/tier
  multipliers; `Lookup` normalizes dated snapshot IDs (e.g. `-20251001`).
- `collect.ParseFile` / `ParseFrom` — JSONL parser (CRLF-safe, tolerant,
  `<synthetic>` excluded), with incremental offset reads.
- `collect.Discover` — enumerate code/cowork transcripts (cowork audit.jsonl
  excluded to avoid double-counting).
- `store` — SQLite (modernc.org/sqlite, pure-Go) persistence: idempotent upsert
  by message_id, incremental `ingest_state`, WAL mode.
- `core/ingest` — collect → dedup → price → store orchestration (incremental).
- `aggregate.Aggregate` — group-by day/session/project/model/entrypoint.
- CLI `ingest` / `report` (`--since`/`--until`/`--group-by`/`--source`/
  `--entrypoint`/`--breakdown`/`--json`) / `sessions` / `models` — implemented.
- Verified end-to-end against real transcripts (156 files → 3808 records).

### Added (Phase 2 of the RFP process — scaffold)
- Project scaffold: Go module, Makefile
  (`build` / `build-all` / `test` / `vet` / `clean`), MIT license, docs.
- `core/model` — OS-neutral types (`Usage`, `UsageRecord`, `Cost`, `PricedRecord`,
  `Source`, `Entrypoint`).
- `core/pricing` — per-model rate table with cache/tier multipliers (default table
  intentionally empty pending real prices).
- `core/cost` — pure cost engine (`Compute`, `ComputeRecord`) **with tests**;
  handles cache 1h/5m, service tier, web tools; `<synthetic>` is free.
- `core/collect` — `Dedup` (by `message.id`) **with tests**; `ParseFile` /
  `Discover` stubbed for Phase 1.
- `core/aggregate`, `core/store` — interfaces and stubs (SQLite via
  modernc.org/sqlite, pure-Go) for Phase 1.
- `core/platform` — build-tagged OS path resolution (darwin/windows/linux)
  **with tests**; `SourceRoots` / `ConfigDir` / `DataDir`.
- CLI dispatch (stdlib `flag`) with `ingest` / `report` / `sessions` / `models` /
  `watch` wired as stubs, and `doctor` fully implemented (diagnoses resolved paths).

### Notes
- Costs are an API list-price **equivalent** (notional), not an actual bill.
- Windows / Linux support is **experimental** — source paths are inferred and
  unverified on real hardware.

[Unreleased]: https://github.com/nlink-jp/claude-usage-lens/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/nlink-jp/claude-usage-lens/releases/tag/v0.1.0
