# claude-usage-lens

**Claude Code** と **Claude Cowork** のローカルセッションログから、トークン使用量と
コストを収集します。Console / 課金 API は不要。ローカルの JSONL トランスクリプトを
解析し、API **定価換算**のコストを計算、永続ストアに蓄積して、日次 / セッション /
プロジェクト / モデル別に集計します。

> **ステータス: Phase 2。** 全 CLI コマンドが end-to-end で動作 — `ingest` /
> `reprice` / `report`（期間分析）/ `sessions` / `models` / `verify` / `doctor` / `watch`
> （near-realtime）/ `daemon`（macOS launchd）。Phase 3 は同一コア上の Wails GUI。
> 設計は [docs/ja/claude-usage-lens-rfp.ja.md](docs/ja/claude-usage-lens-rfp.ja.md) を参照。

> **コストは定価換算（notional）です。** 表示額は API **定価換算**であり、実請求額では
> ありません。サブスク（Max/Pro）利用はトークン従量課金ではありません。
>
> **ソース別に2つのコスト源:** `cowork` は Cowork 自身の `audit.jsonl`（Anthropic の
> `total_cost_usd`）をそのまま採用 = **厳密**（内部ヘルパー呼び出しも含む）。`code`
> （Claude Code）は audit が無いため transcript から計算 = 近似（約5%）で、内部呼び出し
> （タイトル生成の haiku 等）を取りこぼし・再生分を過大計上しうる。単価計算自体は厳密で、
> `verify` が transcript と audit の差を定量化します。

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
claude-usage-lens reprice    単価変更後に蓄積済み Claude Code コストを再計算
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

### 単価変更のあと

ingest は増分方式で、読み終えたバイトを再読しません。そのため単価テーブルの更新
（新モデルの追加、価格改定）は **それ以降に取り込むレコードにしか効きません**。
`reprice` はストアに保存済みのトークン数から Claude Code レコードのコストを
再計算するので、DB を作り直さずに過去分を修正できます:

```sh
claude-usage-lens reprice --dry-run   # 変更内容だけ表示
claude-usage-lens reprice
```

Cowork レコードは再計算しません（コストは Anthropic 公式の `total_cost_usd` で、
こちらの単価表より正確なため）。

`ingest` と `reprice` は、単価テーブルに無いモデルのレコードを検出すると警告します。
該当レコードは **$0** で保存されているので、警告が出たら `core/pricing` にモデルを
追加して `reprice` を実行してください。

### fast mode

Claude Code の `/fast`（Opus 5 / Opus 4.8）は $5/$25 ではなく **$10/$50** で課金され、
キャッシュ倍率は fast 単価に対して適用されます。transcript の
`message.usage.speed` を読んで対応する単価で計算します。モデル別の fast 単価は
`models` で確認できます（`—` は fast mode 非対応。その場合 fast フラグ付き
レコードは標準単価になり、API 自身の挙動と一致します）。

対応前に取り込んだレコードには speed が保存されておらず standard 扱いになります。
再取り込みでも復元できません（該当バイトは消費済みのため）。したがって speed が
正確なのは今後取り込む分のみです。

### 精度検証

`verify` は自前計算した定価換算コストを、Cowork の `audit.jsonl` の
`total_cost_usd`（Anthropic が算出した公式コスト）とセッション単位で突合します。
作者の環境では集計で約5%以内に一致（セッション個別は完全一致〜約15%）。
自機で単価モデルの妥当性を確認できます:

```sh
claude-usage-lens verify
```

`report` フラグ:
- **期間**: `--since`（`2026-07-01` | `2026-07-01T09:00` | RFC3339 | `7d` | `today`）, `--until`
- **タイムゾーン**: `--tz local|utc|<IANA>`（既定 **local**）— `today`・`--since`/`--until`・
  日/時/週/月の区切りに使う TZ。保存タイムスタンプは絶対時刻のままで、区切りだけが変わります。
  旧来の UTC 挙動は `--tz utc`（例: 異なる TZ のマシンを跨いだ集計）。
- **グループ化**: `--group-by hour|day|week|month|session|project|model|entrypoint`（カンマ区切り）
- **フィルタ**: `--source code|cowork|all`, `--entrypoint`, `--model`（部分一致）, `--project`（部分一致）
- **ソート/上位**: `--sort key|cost|input|output|records|cache`, `--top N`
- **系列**: `--dense` — 時系列の歯抜けをゼロコストのバケットで埋め、日次/時次/週次/月次を
  連続させる（時間次元の単一 `--group-by` のみ）
- **ビュー**: `--breakdown`（キャッシュ read/write 内訳）, `--summary`（期間統計）, `--compare`（前期間比）, `--json`

### 分析の例

```sh
claude-usage-lens report --group-by month                    # 月次コスト推移
claude-usage-lens report --group-by project --sort cost --top 5   # コスト上位ドライバー
claude-usage-lens report --since 7d --group-by day --dense   # 連続した日次系列（計上ゼロの日も $0）
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

無設定でも動作します（config ファイルが無いのはエラーではありません）。上書きする
場合は OS の config dir に TOML を置きます（全スキーマは
[config.example.toml](config.example.toml) 参照）:

```toml
[sources]                                   # 推定パスが誤っている場合
code_root = "/custom/path/.claude/projects"

[pricing.models."claude-sonnet-5"]          # 例: 導入価格で計算する
input_per_mtok  = 2.0
output_per_mtok = 10.0
```

- **優先順位**: コマンドラインフラグ > config ファイル > 組み込み/OS 推定値
- **パス**: `[sources]`、またはコマンドごとの `--code-root` / `--cowork-root`
- **単価**: `[pricing.models."<id>"]`。省略したフィールドは **継承** されます
  （組み込みエントリから、未知のモデルなら標準キャッシュ倍率から）。したがって
  2行だけの上書きで十分です。蓄積済みレコードに反映するには、そのあと `reprice`
  を実行してください。
- **`--config PATH`** で別ファイルを指定できます。
- **未知のキーはエラー**です（黙って無視しません）。効いているように見えて実は
  何もしていない設定は、明示的な失敗より有害なためです。

`doctor` は config のパス・読み込めたか・上書きした単価・OS 既定と異なるソース
パスを表示します。

## クロスプラットフォーム

macOS を第一級とします。**Windows / Linux は experimental** — プロファイルパスは推定で
実機未検証です。パス区切りは `path/filepath` で吸収し、OS 別ルートは `core/platform` の
build tag に隔離。パスが違う場合は `[sources]` / `--code-root` / `--cowork-root` で修正し `doctor` で確認。
WSL 利用者は Linux ビルドを使ってください。

**Windows でのストア権限:** store は macOS/Linux では所有者のみに制限されます（dir `0700`,
DB `0600`）。Windows では UNIX パーミッションが効かず（Go の `chmod` は read-only 属性の
切替のみ）、ファイルレベルでは所有者制限されません。実運用ではユーザープロファイル配下
（`%LocalAppData%`）に置かれ、他の標準ユーザーからは既に ACL で保護されています。NTFS ACL
を直接適用する対応はスコープ外です。

## ライセンス

MIT — [LICENSE](LICENSE) 参照。
