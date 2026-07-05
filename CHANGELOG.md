# Changelog

All notable changes to this project are documented here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

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

[Unreleased]: https://github.com/nlink-jp/claude-usage-lens
