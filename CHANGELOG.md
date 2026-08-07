# Changelog

All notable changes to this project are documented here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.6.0] - 2026-08-08

### Added

- **Real-quota calibration** (ADR-0001, `docs/adr/0001-limit-calibration.md`).
  The actual subscription quota is server-side and appears in no local log, so
  it cannot be collected; the private OAuth endpoint Claude Code's `/usage`
  screen uses was deliberately rejected (undocumented, breaks at will). Instead:
  - **`calibrate add --utilization <pct> --resets-at <datetime>`** records the
    official percentage read off the `/usage` screen. The effective weekly cap
    is derived as `window consumption ÷ percentage` on both bases (notional
    USD and in+out tokens), at query time — `reprice` and late ingests refine
    it retroactively. `calibrate list` / `calibrate remove --id N` manage
    points. Derivation-poisoning inputs (0 %, an empty window, an observed-at
    outside the reset window) are rejected with clear errors.
  - **`limits`** reports the calibrated state of the current weekly window:
    derived caps, consumption, estimated utilization per basis, remaining
    headroom, calibration age, and any rate-limit events in the window.
    `--json` is the contract the GUI's weekly monitor consumes;
    `calibrated: false` signals "fall back to the assumed budget".
  - **`ingest` captures 429 rate-limit events** from Claude Code transcripts
    (`system/api_error` records with status 429 or a non-null `rateLimits`
    payload) into a new `limit_events` table, keyed by record uuid. The
    payload is stored verbatim — its schema has never been observed populated,
    so nothing is interpreted from it and a future schema cannot break ingest.
    Each event is ground truth for "the quota was exhausted at this instant".
- **Store schema migration**: new `limit_events` and `calibrations` tables are
  created transparently on open; an existing `usage.db` keeps its history.

### Notes

- The derived cap is **local-visible**: claude.ai web/mobile usage is not in
  the logs, but a stable usage mix is absorbed by the calibration itself.
  Re-calibrate occasionally, and after plan/promotion changes.
- Weekly window only in v1; the 5-hour window resets too fast for manual
  calibration to be meaningful. Limit events are captured from the `code`
  source only (Cowork ingestion deliberately reads just `audit.jsonl`).

## [0.5.0] - 2026-07-26

### Fixed

- **`claude-opus-5` was priced at $0.** The model was absent from the rate table,
  and an unknown model is costed as free by design (that is how `<synthetic>` is
  handled), so every Opus 5 turn silently contributed nothing to reports. Added at
  **$5 / $25** per 1M tokens (input / output), matching Opus 4.8.

- **Fast mode was billed at standard rates.** `speed: "fast"` (Claude Code's
  `/fast`, on Opus 5 and Opus 4.8) costs **$10 / $50** per 1M tokens rather than
  $5 / $25, so a fast-mode turn was under-counted 2×. The transcript records it
  as `message.usage.speed`; it is now parsed, stored, and priced, with the cache
  multipliers applying on top of the fast base price. A fast-flagged record on a
  model with no fast tier falls back to standard rates — what the API itself does
  (Opus 4.6 serves such a request at standard rates; Opus 4.7 rejects it).

  Records ingested before this release carry no stored speed and count as
  standard. That cannot be backfilled — ingest is incremental and those bytes are
  already consumed — so only newly ingested turns are speed-accurate. No fast-mode
  usage was found in the author's local logs, so the practical impact is zero.

### Added

- **`reprice`** — recomputes the cost of stored **Claude Code** records from the
  token counts already in the store, so a rate-table change applies to history
  instead of only to records ingested afterwards. Ingest is incremental (consumed
  bytes are never re-read, upsert is DO NOTHING), so before this the only remedy
  was deleting `usage.db` — which throws away the accumulated history of sessions
  Claude Code has since auto-deleted. `--dry-run` previews the delta. Cowork rows
  are never repriced: their cost is Anthropic's audited `total_cost_usd`.
- **Unpriced-model warning** on `ingest` and `reprice` — lists any billable model
  missing from the rate table, with a record count, so a $0 shortfall is visible
  instead of silent. `<synthetic>` is excluded (legitimately free).
- **`config.toml` is now actually read** (`core/config`). `[sources]` and
  `[pricing.models]` had been documented in `config.example.toml`, both READMEs,
  the `models` footer and `doctor` output since the first release, but no code
  parsed the file — a user config silently did nothing. Now:
  - `[sources] code_root / cowork_root` override the OS-inferred source roots,
    as do the new `--code-root` / `--cowork-root` flags.
  - `[pricing.models."<id>"]` overrides or adds rate-table entries. Every field
    is optional and **inherits** when omitted (from the built-in entry, or from
    the standard cache multipliers for an unknown model), so overriding one price
    cannot silently zero the rest of the entry.
  - `--config PATH` selects a different file; precedence is flags > file >
    built-in defaults.
  - **Unknown keys are rejected** with an error naming them, and negative rates
    are rejected — a typo that looks like it works is the failure this file is
    supposed to prevent.
  - `doctor` reports the config path, whether it loaded, which models you
    repriced, and which source paths differ from the OS default; an invalid
    config makes it exit non-zero.
  - `models` gains a SOURCE column marking each row `built-in` or `config`,
    plus FAST-IN / FAST-OUT columns for the fast-mode tier.
- **Store schema migration.** `usage_records` gains a `speed` column, added in
  place on `Open` when absent, so an existing `usage.db` keeps its accumulated
  history instead of needing a rebuild.

## [0.4.0] - 2026-07-12

### Removed

- **darwin/amd64 (Intel) pre-built binary.** macOS releases now ship
  **arm64 only**, per the org-wide policy (darwin is Apple-Silicon only; no
  universal binaries). Intel Mac users can build from source.

### Changed

- **Linux release archives are now `.tar.gz`** (darwin/windows remain `.zip`),
  per `nlink-jp/.github` CONVENTIONS.md §Release Archive Standard.
- **`LICENSE` is now bundled** in every release archive alongside `README.md`.
- **darwin code-signature identifier** is now the canonical `claude-usage-lens`.

No change to the binary's behaviour — a packaging / build-config release.

## [0.3.1] - 2026-07-06

### Added
- `--since` / `--until` now accept an exact datetime — `2026-07-01T09:00`
  (`[:SS]` optional, interpreted in `--tz`) or an RFC3339 timestamp — in addition
  to a bare date / `Nd` / `today`. Enables filtering from a precise instant (e.g.
  a weekly-reset boundary), used by the GUI's weekly-budget monitor.

## [0.3.0] - 2026-07-05

### Changed
- **Day boundaries and `today` now use the local timezone by default** (previously
  UTC). A user in, say, JST now sees "today" reset at their local midnight instead
  of 09:00. Stored timestamps are unaffected — buckets are computed at query time —
  so no store rebuild is needed. Use `--tz utc` to restore the previous behavior.

### Added
- `report --tz local|utc|<IANA>` — choose the timezone for `today`,
  `--since`/`--until`, and the day/hour/week/month buckets (e.g. `--tz Asia/Tokyo`).

### Docs
- Note the Windows limitation of the store-permission hardening: UNIX modes
  (`0700`/`0600`) don't apply on Windows (Go's `chmod` only toggles read-only),
  where protection relies on the user-profile ACLs instead; NTFS ACLs are out of
  scope.

## [0.2.2] - 2026-07-05

Security & robustness hardening from an external code review (no Critical/High
findings; these are defense-in-depth improvements).

### Security
- Store is now owner-only: the data dir is created/tightened to `0700` (also
  shielding the WAL/SHM sidecars, including on existing installs) and the DB file
  is `0600`, instead of relying on the umask ([#1](https://github.com/nlink-jp/claude-usage-lens/issues/1)).
- The daemon-log fallback uses the per-user `os.TempDir()` instead of the
  world-writable, sticky `/tmp`, closing a symlink/pre-creation race on shared
  machines ([#2](https://github.com/nlink-jp/claude-usage-lens/issues/2)).

### Changed
- Discovery aborts with a clear error if a scan exceeds a high entry cap
  (1,000,000) — a safety net against a source root misconfigured to a filesystem
  root; never a silent truncation ([#3](https://github.com/nlink-jp/claude-usage-lens/issues/3)).

## [0.2.1] - 2026-07-05

### Added
- `report --dense` — fill gaps in a time series with zero-cost buckets so a
  daily/hourly/weekly/monthly series is contiguous (empty days show as $0),
  for a single time `--group-by`. Opt-in; default output is unchanged. Enables a
  gap-free daily chart in the GUI front-end.

## [0.2.0] - 2026-07-05

Accuracy release. Cowork cost is now taken straight from its `audit.jsonl`
(exact — including internal helper calls the transcript omits); web search is
priced; and the rate table is verified against Anthropic's live pricing (no
long-context premium). **Migration: delete `usage.db` once** so Cowork rows
re-ingest from audit.

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

[Unreleased]: https://github.com/nlink-jp/claude-usage-lens/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/nlink-jp/claude-usage-lens/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/nlink-jp/claude-usage-lens/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/nlink-jp/claude-usage-lens/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/nlink-jp/claude-usage-lens/compare/v0.2.2...v0.3.0
[0.2.2]: https://github.com/nlink-jp/claude-usage-lens/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/nlink-jp/claude-usage-lens/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/nlink-jp/claude-usage-lens/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/nlink-jp/claude-usage-lens/releases/tag/v0.1.0
