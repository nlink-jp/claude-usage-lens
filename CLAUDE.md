# CLAUDE.md — claude-usage-lens

**Organization rules (mandatory): https://github.com/nlink-jp/.github/blob/main/CONVENTIONS.md**

## Project overview

util-series CLI that parses Claude Code / Claude Cowork local session logs
(`~/.claude/projects/**/*.jsonl` and the Cowork `local-agent-mode-sessions` tree)
to collect token usage and compute an API **list-price-equivalent** cost. Usage is
accumulated into a durable SQLite store (surviving Claude Code's session auto-delete)
and reported by day / session / project / model. Personal-local first; the reusable
`core/` is designed for a future Wails GUI.

## Non-negotiable rules

- **Tests are mandatory** — write them with the implementation
- **Never `go build` directly** — always `make build` (outputs to `dist/`)
- **Docs in sync** — update `README.md` and `README.ja.md` together
- **Small, typed commits** — `feat:`, `fix:`, `test:`, `chore:`, `docs:`, `refactor:`, `security:`
- **No PII / secrets committed** — never commit real transcripts or `usage.db`
  (both are `.gitignore`d); test fixtures are synthetic

## Build & test

```sh
make build      # → dist/claude-usage-lens
make test       # or: go test ./...
make vet        # host + GOOS=windows + GOOS=linux (covers build-tagged files)
make build-all  # cross-compile all platforms (CGO-free)
```

## Key decisions

- **Store**: SQLite via `modernc.org/sqlite` (pure-Go, no CGO) — cross-builds for
  Windows/Linux without Podman (unlike CGO tools `gem-query` / `json-to-sqlite`).
- **`core/` top-level** (not `internal/`) so the future GUI can import it.
- **Dedup by `message.id`**; `<synthetic>` is free; cache 1h/5m + service tier
  factored into cost.
- **OS paths** isolated in `core/platform` build tags; Windows/Linux experimental.

## Architecture

- `cmd/` — stdlib-flag dispatch (`doctor` implemented; rest wired as stubs)
- `core/model` — shared types
- `core/pricing` — rate table + multipliers
- `core/cost` — pure cost engine (tested)
- `core/collect` — discover / parse / dedup (dedup tested)
- `core/aggregate` — group-by roll-up
- `core/store` — SQLite persistence (interface + Phase 1 impl)
- `core/platform` — OS-specific source roots / config / data dirs (tested)

## Design references

- [`docs/ja/claude-usage-lens-rfp.ja.md`](docs/ja/claude-usage-lens-rfp.ja.md) (primary)
- [`docs/en/claude-usage-lens-rfp.md`](docs/en/claude-usage-lens-rfp.md)
  — approved RFP; canonical source for scope decisions
