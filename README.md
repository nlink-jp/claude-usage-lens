# claude-usage-lens

Collect token usage and cost from **Claude Code** and **Claude Cowork** local
session logs — no Console or billing API required. Parses the local JSONL
transcripts, computes an API **list-price-equivalent** cost, accumulates it in a
durable store, and reports it by day / session / project / model.

> **Status: Phase 2.** All CLI commands work end-to-end — `ingest`, `report`
> (period analysis), `sessions`, `models`, `verify`, `doctor`, `watch`
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
make build-all  # cross-compile all platforms (CGO-free, pure-Go SQLite)
```

Requires Go 1.26+. No CGO, no external services.

## Commands

```
claude-usage-lens ingest     Incrementally load new/changed sessions into the store
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

### Accuracy

`verify` compares our computed notional cost against Cowork's own
`audit.jsonl` `total_cost_usd` (Anthropic's pre-computed cost) per session.
On the author's data the aggregate agrees within ~5%, with individual sessions
ranging from exact to ~15% — run it on your machine to check the pricing model:

```sh
claude-usage-lens verify
```

`report` flags:
- **Period**: `--since` (`2026-07-01` | `7d` | `today`), `--until`
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

Optional TOML at your OS config dir (see [config.example.toml](config.example.toml)):
override source paths (`[sources]`) and per-model prices (`[pricing]`). All paths
are also settable via `--source-root`.

## Cross-platform

macOS is first-class. **Windows / Linux are experimental** — their profile paths
are inferred and unverified on real hardware. Path separators are handled via
`path/filepath`; per-OS roots live behind build tags in `core/platform`. If a path
is wrong, fix it via `[sources]` / `--source-root` and confirm with `doctor`. WSL
users should use the Linux build.

**Store permissions on Windows:** the store is restricted to the owner (dir `0700`,
DB `0600`) on macOS/Linux. On Windows those UNIX modes don't apply — Go's `chmod`
only toggles the read-only bit — so the DB isn't owner-restricted at the file
level. In practice it lives under your user profile (`%LocalAppData%`), which is
already ACL-protected from other standard users; applying NTFS ACLs directly is
out of scope.

## License

MIT — see [LICENSE](LICENSE).
