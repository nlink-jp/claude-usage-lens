# claude-usage-lens

**Claude Code** と **Claude Cowork** のローカルセッションログから、トークン使用量と
コストを収集します。Console / 課金 API は不要。ローカルの JSONL トランスクリプトを
解析し、API **定価換算**のコストを計算、永続ストアに蓄積して、日次 / セッション /
プロジェクト / モデル別に集計します。

> **ステータス: WIP（Phase 1 scaffold）。** 収集コアを構築中。コスト計算・dedup・
> プラットフォーム別パス解決は実装済み、ingest / report / store の内部は Phase 1 で実装。
> 設計は [docs/ja/claude-usage-lens-rfp.ja.md](docs/ja/claude-usage-lens-rfp.ja.md) を参照。

> **コストは定価換算（notional）です。** 表示額は API **定価換算**であり、実請求額では
> ありません。サブスク（Max/Pro）利用はトークン従量課金ではありません。

## なぜ

Claude Code / Cowork はローカルに JSONL ログを残し、その中にモデル別・トークン種別の
使用量（`message.usage`）が含まれます。本ツールはそれを使用量/コストのビューに変換し、
ニアリアルタイムで確認できます。ソースセッションは自動削除されるため、永続コピーを
保持して履歴を失いません。

## インストール / ビルド

```sh
make build      # → dist/claude-usage-lens（go build 直接実行は禁止）
make test       # go test ./...
make build-all  # 全プラットフォームをクロスコンパイル（CGOなし・pure-Go SQLite）
```

Go 1.26+ が必要。CGO なし、外部サービス依存なし。

## コマンド

```
claude-usage-lens ingest     新規/変更セッションをストアへ増分取込
claude-usage-lens report     蓄積データを日次/セッション/プロジェクト/モデル別に集計
claude-usage-lens sessions   セッション一覧（トークン・コスト付き）
claude-usage-lens models     単価テーブルと drift を表示
claude-usage-lens doctor     解決したソース/ストア/config パスを診断
claude-usage-lens watch      [Phase 2] near-realtime 継続取込
claude-usage-lens version    バージョン表示
```

`report` フラグ: `--since`, `--until`, `--group-by day|session|project|model|entrypoint`,
`--source code|cowork|all`, `--entrypoint cli|claude-desktop|sdk-py`, `--breakdown`, `--json`。

### doctor

まず `doctor` でログが見えているか確認します:

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

## データソース

| Source | 場所 | 備考 |
|--------|------|------|
| `code` | `~/.claude/projects/**/*.jsonl` | Claude Code（CLI + デスクトップ + SDK）|
| `cowork` | `…/Claude/local-agent-mode-sessions/**/outputs/*.jsonl` | `code` と同一スキーマ |
| `cowork` audit | `…/local-agent-mode-sessions/**/audit.jsonl` | コスト算出済み — 検証クロスチェックに使用 |

## 設定

OS の config dir に任意の TOML（[config.example.toml](config.example.toml) 参照）:
ソースパス（`[sources]`）とモデル別単価（`[pricing]`）を上書き可能。パスは
`--source-root` でも指定できます。

## クロスプラットフォーム

macOS を第一級とします。**Windows / Linux は experimental** — プロファイルパスは推定で
実機未検証です。パス区切りは `path/filepath` で吸収し、OS 別ルートは `core/platform` の
build tag に隔離。パスが違う場合は `[sources]` / `--source-root` で修正し `doctor` で確認。
WSL 利用者は Linux ビルドを使ってください。

## ライセンス

MIT — [LICENSE](LICENSE) 参照。
