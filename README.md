# claude-usage-lens

Collect token usage and cost from **Claude Code** and **Claude Cowork** local
session logs — no Console or billing API required. Parses the local JSONL
transcripts, computes an API **list-price-equivalent** cost, accumulates it in a
durable store, and reports it by day / session / project / model.

> **Status: Phase 2.** All CLI commands work end-to-end — `ingest`, `reprice`,
> `report` (period analysis), `sessions`, `models`, `verify`, `doctor`, `watch`
> (near-real-time), and `daemon` (macOS launchd). Phase 3 is a Wails GUI over the
> same core. See [docs/en/claude-usage-lens-rfp.md](docs/en/claude-usage-lens-rfp.md).

> **Costs are notional.** The figures are the API **list-price equivalent**, not
> an actual bill. Subscription (Max/Pro) usage is not billed per token.
>
> **Two cost sources by origin:** `cowork` cost is taken straight from Cowork's
> own `audit.jsonl` (Anthropic's `total_cost_usd`) — **exact**, including internal
> helper calls. `code` (Claude Code) has no audit log, so its cost is computed
> from the transcript — a close estimate (~5%) that omits internal helper calls
> (e.g. haiku for titles) and can over-count replayed turns. The pricing itself is
> exact; `verify` quantifies the transcript-vs-audit gap.

## Why

Claude Code / Cowork leave local JSONL logs that embed per-model, per-token-type
usage (`message.usage`). This tool turns that into a usage/cost view you can
watch in near-real-time, and — because source sessions are auto-deleted — keeps a
durable copy so history is never lost.

## Install / build

```sh
make build      # → dist/claude-usage-lens   (never `go build` directly)
make test       # go test ./...
make build-all  # cross-compile release platforms (darwin arm64 only; CGO-free, pure-Go SQLite)
```

Requires Go 1.26+. No CGO, no external services.

## Commands

```
claude-usage-lens ingest     Incrementally load new/changed sessions into the store
claude-usage-lens reprice    Recompute stored Claude Code costs after a pricing change
claude-usage-lens report     Aggregate stored usage by day / session / project / model
claude-usage-lens sessions   List sessions with tokens and cost
claude-usage-lens models     Show the pricing table and flag drift
claude-usage-lens verify     Cross-check computed cost against Cowork audit.jsonl (ground truth)
claude-usage-lens doctor     Diagnose resolved source/store/config paths
claude-usage-lens watch      Poll and ingest continuously, printing live cost deltas
claude-usage-lens daemon     Install/uninstall/status a periodic-ingest service (macOS launchd)
claude-usage-lens version    Print the version
```

### Near-real-time

`watch` polls the sources on an interval, runs an incremental ingest each tick
(only changed bytes are re-read), and prints a line whenever new usage lands:

```sh
claude-usage-lens watch --interval 5s
# [16:55:35] +1 rec (Δ$0.38)   now: 4652 rec / $1557.44
```

To keep the store fresh in the background without a running terminal, install a
periodic-ingest service (macOS launchd; Windows/Linux: schedule `ingest` with
your OS scheduler):

```sh
claude-usage-lens daemon install --interval 15m   # or --dry-run to preview the config
claude-usage-lens daemon status
claude-usage-lens daemon uninstall
```

### After a pricing change

Ingest is incremental — bytes already read are never revisited — so updating the
rate table (a newly released model, a revised price) affects only records
ingested *afterwards*. `reprice` recomputes every stored Claude Code record from
the token counts already in the store, so history is corrected in place without
rebuilding the database:

```sh
claude-usage-lens reprice --dry-run   # show what would change
claude-usage-lens reprice
```

Cowork records are never repriced — their cost is Anthropic's own audited
`total_cost_usd`, which this table cannot improve on.

`ingest` and `reprice` both warn when a record's model is absent from the rate
table. Those records are stored at **$0**, so the warning is your signal to add
the model to `core/pricing` and rerun `reprice`.

### Fast mode

Claude Code's `/fast` toggle (Opus 5 and Opus 4.8) bills at a **$10 / $50**
premium instead of $5 / $25, with the cache multipliers applying on top of the
fast price. The transcript records it as `message.usage.speed`, and records are
priced accordingly — `models` shows the fast tier per model (`—` where the model
has none, in which case a fast-flagged record bills at the standard rate, matching
the API's own behaviour).

Records ingested before this was modeled have no stored speed and count as
standard. Re-ingesting cannot recover it — those bytes are already consumed — so
only turns ingested from now on are speed-accurate.

### Accuracy

`verify` compares our computed notional cost against Cowork's own
`audit.jsonl` `total_cost_usd` (Anthropic's pre-computed cost) per session.
On the author's data the aggregate agrees within ~5%, with individual sessions
ranging from exact to ~15% — run it on your machine to check the pricing model:

```sh
claude-usage-lens verify
```

`report` flags:
- **Period**: `--since` (`2026-07-01` | `2026-07-01T09:00` | RFC3339 | `7d` | `today`), `--until`
- **Timezone**: `--tz local|utc|<IANA>` (default **local**) — the zone for
  `today`, `--since`/`--until`, and day/hour/week/month boundaries. Stored
  timestamps stay absolute; only the buckets shift. Use `--tz utc` for the old
  UTC behavior (e.g. aggregating across machines in different zones).
- **Group by**: `--group-by hour|day|week|month|session|project|model|entrypoint` (comma-separated)
- **Filter**: `--source code|cowork|all`, `--entrypoint`, `--model` (substring), `--project` (substring)
- **Sort/limit**: `--sort key|cost|input|output|records|cache`, `--top N`
- **Series**: `--dense` — fill gaps in a time series with zero-cost buckets, so a
  daily/hourly/weekly/monthly series is contiguous (single time `--group-by`)
- **Views**: `--breakdown` (cache read/write split), `--summary` (period stats), `--compare` (vs preceding period), `--json`

### Analysis examples

```sh
claude-usage-lens report --group-by month                    # monthly cost trend
claude-usage-lens report --group-by project --sort cost --top 5   # top cost drivers
claude-usage-lens report --since 7d --group-by day --dense   # contiguous daily series (empty days as $0)
claude-usage-lens report --since 7d --summary                # daily avg, peak, 30d projection
claude-usage-lens report --since 7d --compare                # this week vs last week (Δ%)
claude-usage-lens report --since 3d --model opus --group-by day
```

### doctor

Run `doctor` first to confirm the tool sees your logs:

```
$ claude-usage-lens doctor
claude-usage-lens doctor (darwin/arm64)

sources:
  code    [ok     ] /Users/you/.claude/projects
           18 top-level entries
  cowork  [ok     ] /Users/you/Library/Application Support/Claude/local-agent-mode-sessions
           2 top-level entries
...
```

## Data sources

| Source | Location | Notes |
|--------|----------|-------|
| `code` | `~/.claude/projects/**/*.jsonl` | Claude Code (CLI + desktop app + SDK) |
| `cowork` | `…/Claude/local-agent-mode-sessions/**/outputs/*.jsonl` | Same schema as `code` |
| `cowork` audit | `…/local-agent-mode-sessions/**/audit.jsonl` | Pre-computed cost — used as a validation cross-check |

## Configuration

Everything works unconfigured — a missing config file is not an error. To
override, drop a TOML file in your OS config dir (see
[config.example.toml](config.example.toml) for the full schema):

```toml
[sources]                                   # when the inferred path is wrong
code_root = "/custom/path/.claude/projects"

[pricing.models."claude-sonnet-5"]          # e.g. cost at the introductory rate
input_per_mtok  = 2.0
output_per_mtok = 10.0
```

- **Precedence**: command-line flags > config file > built-in / OS-inferred defaults.
- **Paths**: `[sources]`, or `--code-root` / `--cowork-root` per command.
- **Prices**: `[pricing.models."<id>"]`. Omitted fields **inherit** — from the
  built-in entry, or from the standard cache multipliers for a model this build
  does not know — so a two-line override is enough. Run `reprice` afterwards to
  apply the change to already-stored records.
- **`--config PATH`** points at a different file.
- **Unknown keys are an error**, not silently ignored: a typo'd setting that
  looks like it works is worse than a loud failure.

`doctor` reports the config path, whether it loaded, which prices you overrode,
and which source paths differ from the OS defaults.

## Cross-platform

macOS is first-class. **Windows / Linux are experimental** — their profile paths
are inferred and unverified on real hardware. Path separators are handled via
`path/filepath`; per-OS roots live behind build tags in `core/platform`. If a path
is wrong, fix it via `[sources]` / `--code-root` / `--cowork-root` and confirm with `doctor`. WSL
users should use the Linux build.

**Store permissions on Windows:** the store is restricted to the owner (dir `0700`,
DB `0600`) on macOS/Linux. On Windows those UNIX modes don't apply — Go's `chmod`
only toggles the read-only bit — so the DB isn't owner-restricted at the file
level. In practice it lives under your user profile (`%LocalAppData%`), which is
already ACL-protected from other standard users; applying NTFS ACLs directly is
out of scope.

## License

MIT — see [LICENSE](LICENSE).
