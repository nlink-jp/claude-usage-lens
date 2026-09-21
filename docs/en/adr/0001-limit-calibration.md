# ADR-0001: Real-limit calibration — manual /usage readings + passive 429 capture

- Status: Accepted
- Date: 2026-08-08

## Context

The GUI's weekly budget monitor (claude-usage-lens-gui v0.1.6+) tracks weekly
consumption against a **user-assumed budget**, because the actual subscription
quota is not knowable from local data. The user asked whether the *real*
remaining quota can be collected "as a clear number from the logs".

Investigation (2026-08-08, real local data) established:

- **No local file carries proactive quota state.** Transcripts
  (`~/.claude/projects/**/*.jsonl`) record `message.usage` token counts only.
  `~/.claude.json` and `stats-cache.json` hold feature-flag caches and local
  aggregates. No `utilization` / `resets_at` value exists anywhere on disk.
- The only rate-limit-adjacent transcript field is `error.rateLimits` on
  `type:system, subtype:api_error` records. In the examined corpus all 30
  occurrences were `null` — every one came from a 529 (Overloaded) or a network
  error. The field is evidently populated only when an actual 429 limit
  rejection happens: a **reactive** signal, not a gauge.
- The authoritative numbers live server-side, surfaced in Claude Code's
  `/usage` screen, which queries the **undocumented** OAuth endpoint
  `GET https://api.anthropic.com/api/oauth/usage`. It returns per-window
  utilization percentages and reset instants (5-hour, 7-day, 7-day-Opus).

## Decision

Do **not** call the private endpoint. Instead, obtain real numbers through two
official-surface channels and use them to *calibrate* the existing model:

### 1. Manual calibration from the official `/usage` screen

The user occasionally reads the official utilization off Claude Code's `/usage`
screen and records it:

```
claude-usage-lens calibrate add --utilization 45 --resets-at 2026-08-13T09:00
```

From a calibration point (observed-at `t`, utilization `u` %, next reset `R`)
the effective weekly cap is derived against our own store:

```
window_start = R - 7d           (must contain t)
cap = consumption(window_start … t) / (u / 100)
```

computed on **both bases** the GUI supports — notional cost (USD) and
input+output tokens. Caps are derived **at query time**, never frozen: a later
`reprice` or newly ingested history automatically improves them.

The derived cap is a **local-visible effective cap**: consumption only counts
what local logs see (Claude Code + Cowork). Usage from claude.ai web/mobile
chat is invisible to us, but the calibration absorbs it — as long as the local
share of total usage stays roughly stable, `local_consumption / cap` tracks the
official percentage. This is self-consistent by construction, and re-calibrating
re-anchors it.

New `limits` command reports the current window's state from the latest
calibration: window boundaries (the reset cadence anchored at `R`), derived
caps, consumption so far, estimated utilization %, and remaining headroom.

### 2. Passive capture of 429 limit events from transcripts

`ingest` now also extracts `system/api_error` records whose `status` is 429 or
whose `rateLimits` is non-null, into a new `limit_events` table (keyed by the
record `uuid`; the raw `rateLimits` JSON is preserved verbatim). A 429 is a
ground-truth "utilization = 100 % at this instant" observation.

Because we have **zero populated `rateLimits` examples** (schema unknown), v1
records and surfaces these events (`limits` lists any in the current window)
but does not auto-convert them into calibration points — the event alone does
not say *which* window (5-hour vs weekly) was exhausted. Turning one into a
calibration is a one-liner once the user knows which limit was hit:
`calibrate add --utilization 100 --at <event-ts> --resets-at <shown reset>`.
Auto-conversion can be added when a real payload has been observed.

### GUI integration

The GUI stays a thin front-end: it renders `limits --json` when at least one
calibration exists (badge: *calibrated*), and falls back to the manual budget
otherwise (badge: *assumed*). A settings field lets the user type the observed
% + reset time, which shells out to `calibrate add`.

### Scope choices

- **Weekly window only** in v1. The 5-hour window resets too fast for manual
  calibration to stay meaningful; the schema keeps a `window` column so it can
  come later.
- **Limit events from the `code` source only** in v1. Cowork ingestion reads
  `audit.jsonl` (cost ground truth) and deliberately does not stream the
  transcript files; scanning them just for rare 429 events is not worth the
  I/O. Extend later if Cowork limit events matter.
- CLI + GUI ship together (co-reinforcing change: CLI produces `limits --json`,
  GUI consumes it).

## Alternatives considered

- **OAuth usage endpoint (`/api/oauth/usage`)** — real percentages, zero user
  effort. **Rejected by the user**: private/undocumented API, can break or be
  blocked at any time; it also aggressively 429s its own callers (known issue).
  Requires Keychain token access besides.
- **Screen-scraping the `/usage` TUI** — strictly more fragile than the
  endpoint. Rejected.
- **Admin Usage & Cost API** — official, but only covers organization API-key
  billing, not Max/Pro subscription windows. Not applicable.
- **OTEL telemetry export** — official, but exports token/cost counters we
  already have; carries no quota state. Not applicable.
- **Do nothing (keep assumed budget)** — the monitor stays a guess; drifts
  badly when Anthropic changes limits (e.g. promotional +50 % weeks). Rejected
  in favour of cheap human-in-the-loop calibration.

## Risks

- **Stale calibration** — the official limit can change (promotions, plan
  changes) without the user re-reading `/usage`. Mitigation: `limits` prints
  the calibration's age; the GUI shows it. Re-calibrating is a 10-second act.
- **Unstable local-usage share** — a week dominated by claude.ai chat would
  make the derived % lag the official one. Accepted: re-calibration re-anchors,
  and the failure mode (conservative or optimistic drift) is visible the next
  time the user glances at `/usage`.
- **`rateLimits` schema unknown** — we store it raw and interpret nothing, so
  a future schema cannot break ingestion (worst case: an event is merely less
  informative).
- **Zero-consumption calibration is undefined** — `calibrate add` rejects
  `utilization ≤ 0`, a window with no local consumption, and an
  observed-at/reset pair more than 7 days apart, with clear errors.
