# Changelog

All notable changes to this project are documented here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- Project scaffold (Phase 2 of the RFP process): Go module, Makefile
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
