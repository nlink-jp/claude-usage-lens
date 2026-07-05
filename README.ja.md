# claude-usage-lens

**Claude Code** と **Claude Cowork** のローカルセッションログから、トークン使用量と
コストを収集します。Console / 課金 API は不要。ローカルの JSONL トランスクリプトを
解析し、API **定価換算**のコストを計算、永続ストアに蓄積して、日次 / セッション /
プロジェクト / モデル別に集計します。

> **ステータス: Phase 2。** 全 CLI コマンドが end-to-end で動作 — `ingest` /
> `report`（期間分析）/ `sessions` / `models` / `verify` / `doctor` / `watch`
> （near-realtime）/ `daemon`（macOS launchd）。Phase 3 は同一コア上の Wails GUI。
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
claude-usage-lens verify     自前計算を Cowork audit.jsonl (ground truth) と突合
claude-usage-lens doctor     解決したソース/ストア/config パスを診断
claude-usage-lens watch      ポーリング継続取込・コスト差分をライブ表示
claude-usage-lens daemon     定期取込サービスの install/uninstall/status (macOS launchd)
claude-usage-lens version    バージョン表示
```

### ニアリアルタイム

`watch` は一定間隔でソースをポーリングし、毎回増分 ingest（変更バイトのみ再読）を
実行、新規使用量が入るたびに1行表示します:

```sh
claude-usage-lens watch --interval 5s
# [16:55:35] +1 rec (Δ$0.38)   now: 4652 rec / $1557.44
```

ターミナルを開いていなくても store を最新に保つには、定期取込サービスを導入します
（macOS launchd。Windows/Linux は OS スケジューラで `ingest` を登録）:

```sh
claude-usage-lens daemon install --interval 15m   # --dry-run で設定プレビュー
claude-usage-lens daemon status
claude-usage-lens daemon uninstall
```

### 精度検証

`verify` は自前計算した定価換算コストを、Cowork の `audit.jsonl` の
`total_cost_usd`（Anthropic が算出した公式コスト）とセッション単位で突合します。
作者の環境では集計で約5%以内に一致（セッション個別は完全一致〜約15%）。
自機で単価モデルの妥当性を確認できます:

```sh
claude-usage-lens verify
```

`report` フラグ:
- **期間**: `--since`（`2026-07-01` | `7d` | `today`）, `--until`
- **グループ化**: `--group-by hour|day|week|month|session|project|model|entrypoint`（カンマ区切り）
- **フィルタ**: `--source code|cowork|all`, `--entrypoint`, `--model`（部分一致）, `--project`（部分一致）
- **ソート/上位**: `--sort key|cost|input|output|records|cache`, `--top N`
- **ビュー**: `--breakdown`（キャッシュ read/write 内訳）, `--summary`（期間統計）, `--compare`（前期間比）, `--json`

### 分析の例

```sh
claude-usage-lens report --group-by month                    # 月次コスト推移
claude-usage-lens report --group-by project --sort cost --top 5   # コスト上位ドライバー
claude-usage-lens report --since 7d --summary                # 日平均・ピーク・30日換算
claude-usage-lens report --since 7d --compare                # 今週 vs 先週（Δ%）
claude-usage-lens report --since 3d --model opus --group-by day
```

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
