# RFP: claude-usage-lens

> Generated: 2026-07-05
> Status: Draft

## 1. Problem Statement

Claude Code（ターミナルCLI・デスクトップ版アプリ・Python SDK）および Claude Cowork
（local-agent-mode）を日常的に利用しているが、Anthropic の Console / 課金 API が
利用できない環境では、トークン使用量やコストを横断的に把握する手段がない。
一方、これらのツールは**ローカルにセッションログ（JSONL）を残しており、その中に
モデル別・トークン種別の使用量（`message.usage`）が含まれている**ことが実測で確認できた。

`claude-usage-lens` は、これらローカルログを解析してトークン使用量とコスト
（API 定価換算）を収集・計算し、日次 / セッション / プロジェクト / モデル別に
集計するローカルツールである。ソースセッションは自動削除される（Claude Code の
`.last-cleanup`）ため、**取り込んだデータを永続ストアに蓄積**し、データロストを防ぐ。
まずは個人ローカル（1台）での利用を対象とし、将来のマルチマシン集計・GUI 化に
備えたコア設計とする。ターゲットユーザーは、自分の Claude 利用状況とコストを
ニアリアルタイムで把握したい個人開発者。

## 2. Functional Specification

### Commands / API Surface

単一バイナリ + サブコマンド構成（将来の GUI サブコマンド同居を想定）。

```
claude-usage-lens ingest          # 増分取込(idempotent)。新規/変更分のみ読取り store へ upsert
    --source code|cowork|all       #   （デフォルト all）

claude-usage-lens report           # store を集計して表示（高速）。未取込 tail は自動 fold-in で最新反映
    --since <date|"7d"|"today">    #   期間フィルタ
    --until <date>
    --group-by day|session|project|model|entrypoint   # 複数指定可（デフォルト day）
    --source code|cowork|all       #   （デフォルト all）
    --entrypoint cli|claude-desktop|sdk-py             # 絞り込み（任意）
    --breakdown                    #   input/output/cache-read/cache-1h/cache-5m を展開
    --json                         #   機械可読出力（パイプ用）

claude-usage-lens sessions         # セッション一覧（id/project/model/tokens/cost/時刻）
claude-usage-lens models           # 現行単価テーブル表示 + スキーマ/価格 drift 検知
claude-usage-lens doctor           # 解決したソースroot/store/config パスと存在有無・ファイル数を診断（OS横断の検証用）

claude-usage-lens watch            # [Phase2] fsnotify で継続 ingest（常駐, near-realtime, 全OS共通）
claude-usage-lens daemon install   # [Phase2] 定期 ingest を OS 別スケジューラに登録（macOS: launchd / Windows: Task Scheduler / Linux: systemd|cron）
```

### Input / Output

**入力（ローカルファイル読取りのみ、書き込みなし）**

| Source | パス | 内容 |
|--------|------|------|
| `code` | `~/.claude/projects/<encoded-cwd>/<sessionId>.jsonl` | `assistant` レコードの `message.usage`。`entrypoint` ∈ {cli, claude-desktop, sdk-py} |
| `cowork`（transcript）| `~/Library/Application Support/Claude/local-agent-mode-sessions/**/outputs/*.jsonl`（+ `subagents/agent-*.jsonl`）| Code と**同一スキーマ** |
| `cowork`（audit）| `.../local-agent-mode-sessions/**/audit.jsonl` | `total_cost_usd` / `modelUsage` / `usage` を算出済み（HMAC 署名付き）→ **検証用 ground truth** |

`usage` フィールド実スキーマ:
```
input_tokens, output_tokens
cache_creation_input_tokens, cache_read_input_tokens
cache_creation: { ephemeral_1h_input_tokens, ephemeral_5m_input_tokens }
server_tool_use: { web_search_requests, web_fetch_requests }
service_tier            # standard | priority | batch
model                   # claude-opus-4-8 | claude-fable-5 | <synthetic> ...
requestId / message.id  # dedup キー
```

**出力**: デフォルトは整形テーブル（stdout）、`--json` で構造化 JSON。util-series の
パイプ親和方針に従う。

### Configuration

- config: `<OS 標準 config dir>/claude-usage-lens/config.toml`（sectioned TOML、既存 config 規約準拠）
  - `[pricing]` … モデル別単価・係数の上書き（同梱 default テーブルを上書き）
  - `[sources]` … ソースパス／プロファイルルートの上書き（デフォルトは OS 別の標準パス。**推定パスが外れる環境でも再コンパイル不要で正せる安全弁**）
- 永続ストア: `<OS 標準 data dir>/claude-usage-lens/usage.db`（SQLite）
- パス解決は OS 標準に従う（`core/platform` が解決）:
  - config: macOS `~/Library/Application Support/` ／ Linux `~/.config`(XDG) ／ Windows `%APPDATA%`
  - data(store): macOS `~/Library/Application Support/` ／ Linux `~/.local/share`(XDG) ／ Windows `%LOCALAPPDATA%`
  - 環境変数 / `--source-root` フラグでも上書き可能

### External Dependencies

- 外部 API / サービス依存: **なし**（完全ローカル、ファイル読取りのみ）
- ライブラリ: `modernc.org/sqlite`（pure-Go, CGO なし）、`fsnotify`（Phase2）
- 認証情報: 不要

## 3. Design Decisions

- **言語 / フレームワーク**: Go。単一静的バイナリ、`fsnotify` による near-realtime watch、
  純粋関数によるテスト容易性、notarize の容易さ（CGO 回避）を重視。
- **クロスプラットフォーム設計（macOS 主 / Windows・Linux 対応）**: スタック（Go + pure-Go SQLite +
  fsnotify）が全 OS 対応のため、解析コア（parse/cost/aggregate/store）は完全に OS 中立に保つ。
  OS 差分は「①パス区切り・プロファイル/ホーム構造」「②定期実行スケジューラ」の2点のみに閉じ込め、
  build tag で隔離する。詳細は後述の「クロスプラットフォーム対応」節。
- **コアの独立モジュール化**: 収集〜計算〜集計〜永続化を `core/` パッケージ群に集約し、
  すべて純粋関数 + 依存注入で構成。CLI も将来の Wails GUI も `core/` を import する薄い
  アダプタとする（「GUI に CLI サブコマンド同居」パターン）。
- **ストアエンジン = SQLite（modernc.org/sqlite, pure-Go）**: 単一静的バイナリで notarize が
  容易（go-duckdb + Wails の arrow 問題を回避）。この規模では SQL 集計で十分。
  DuckDB（gem-query / data-toolbox-mcp で実績）も候補だったが、軽量・単一バイナリ優先で SQLite を選択。
- **コストの意味づけ**: `Cost{ ListPriceUSD, Tier }` として保持し、サブスク（Max/Pro）利用時は
  「API 定価換算（notional cost）」＝実請求額ではない旨を README / 出力の両方に明示。
  単価は記憶から埋めず、実装時に現行 pricing を取得して default テーブルに焼き込む（drift 注意）。
- **補完する既存 nlink-jp ツール**: data-analyzer（大規模 JSON/JSONL 分析）、llm-cli、
  gem-query（DuckDB 分析 CLI）。ローカルログ解析＋定価換算という切り口で棲み分け。
- **明示的なスコープ外**:
  - 実請求額の照合（Console / Billing API 連携）は対象外（定価換算のみ）
  - マルチマシン / チーム集計は Phase 対象外だが、`UsageRecord.Source` / `Host` フィールドを
    最初から持たせ、後付け（export → 中央集計）できる余地を残す
  - Claude 以外の LLM プロバイダは対象外（claude 専用）

### パッケージ構成
```
_wip/claude-usage-lens/
  cmd/claude-usage-lens/main.go   # CLI エントリ（サブコマンド dispatch, GUI 同居想定）
  core/
    model/     types.go           # UsageRecord{ Source, Entrypoint, Host, ... }, Cost
    collect/   discover.go        # 3ソース root の jsonl 列挙（source/entrypoint 付与）
               parse.go           # jsonl → UsageRecord（境界で正規化・未知フィールドは寛容にskip）
               dedup.go           # message.id でグローバル重複排除
    pricing/   table.go           # モデル別単価（埋め込み default）
    cost/      cost.go            # UsageRecord × pricing → Cost（純粋関数）
    aggregate/ aggregate.go       # by day/session/project/model/entrypoint
    store/     store.go           # SQLite: usage_records / ingest_state, 冪等 upsert・増分取込
    platform/  paths.go           # OS 中立 IF: SourceRoots()/DataDir()/ConfigDir()
               paths_darwin.go    # //go:build darwin  — ~/.claude, ~/Library/Application Support/Claude
               paths_windows.go   # //go:build windows — %USERPROFILE%\.claude, %APPDATA%\Claude
               paths_linux.go     # //go:build linux   — ~/.claude, ~/.config, ~/.local/share
               scheduler_*.go     # [Phase2] launchd / Task Scheduler / systemd を build tag で分岐
```

### 永続ストアのスキーマ（要点）
- `usage_records(message_id PRIMARY KEY, ts, source, entrypoint, host, session_id, project, model, service_tier, input_tokens, output_tokens, cache_read, cache_1h, cache_5m, web_search, web_fetch, cost_usd, ingested_at)`
  - `message_id` を主キーとし冪等 upsert ＝ グローバル dedup。`iterations[]` は top-level と
    二重計上しない。`<synthetic>` は除外。
  - **耐久性保証**: `ingest` は source ファイルが後に消えても store 行を削除しない
    （Claude Code のセッション自動削除に対する保険）。
- `ingest_state(path PRIMARY KEY, size, mtime, last_offset, updated_at)`
  - 前回オフセット以降のみ読取る増分取込。

### クロスプラットフォーム対応（macOS 主 / Windows・Linux 対応設計）

macOS を第一級とし、Windows / Linux も「設計上は対応・実機未検証(experimental)」として作る。
テスト環境の用意が困難なため、推定パスが外れても運用で吸収できる設計にする。

- **パス区切りは `path/filepath` に統一** — `/` の直書きを禁止し、全パス操作を `filepath.Join` /
  `Glob` / `WalkDir` で行う。Windows の `\` と macOS/Linux の `/` を吸収する。
- **プロファイル/ホーム構造の差異を build tag で隔離** — `core/platform` の
  `paths_darwin.go` / `paths_windows.go` / `paths_linux.go` が OS 別のソースルートを返し、コアは
  この IF だけを見る:

  | 用途 | macOS | Linux | Windows（推定・要実機検証）|
  |------|-------|-------|------------------------------|
  | code | `~/.claude/projects/` | `~/.claude/projects/` | `%USERPROFILE%\.claude\projects\` |
  | cowork | `~/Library/Application Support/Claude/local-agent-mode-sessions/` | 同 Linux 相当 | `%APPDATA%\Claude\local-agent-mode-sessions\` |

- **ディレクトリ名のデコードに依存しない** — `<encoded-cwd>` の符号化規則は OS 依存
  （Windows はドライブレター `C:`・`\`）。ディレクトリ名は解釈せず、JSONL レコード内の
  `cwd` / `sessionId` / `project` を使う → パーサは完全に OS 中立。
- **CRLF 対応** — 行分割後に行末 `\r` をトリム。
- **SQLite は WAL モード** — watch + report の同時アクセスを全 OS で安全に。
- **未検証リスクの吸収策**: ① `[sources]` / 環境変数 / `--source-root` で推定パスを上書き可能、
  ② `doctor` サブコマンドで解決パス・存在有無・ファイル数を表示し実機検証を1コマンド化、
  ③ README で Windows は experimental と明記。WSL 利用者は Linux ビルドを使う。

## 4. Development Plan

### Phase 1: Core
- `core/`（model / collect / parse / dedup / pricing / cost / aggregate / **store**）実装
- コマンド: `ingest` / `report` / `sessions` / `models`
- テスト:
  - 合成 fixture（既知トークン数、PII 排除・Secret Scanning 安全形式）→ コスト期待値をアサート
  - **検証ハーネス（gitignore, ローカル限定）**: 自前計算 vs Cowork `audit.jsonl` の `total_cost_usd`
    を突き合わせ、単価モデルの正しさを実証
- ドキュメント: README.md / README.ja.md / CHANGELOG.md / AGENTS.md
- ビルドマトリクス: darwin(amd64/arm64) を第一級。windows(amd64/arm64)・linux(amd64/arm64) も
  CGO なしでクロスビルド。Windows/Linux バイナリは experimental 表記で配布。
- **独立レビュー可**: コア計算ロジックと store 永続化を単体で査読できる。

### Phase 2: Features（near-realtime）
- `watch`（fsnotify で最新 jsonl を増分監視 → 継続 ingest）
- `daemon install`（launchd plist 生成/登録で定期 ingest）
- ロジック（idempotent な `ingest`）とスケジューラ（launchd）を分離しテスト容易性を維持。
- **独立レビュー可**: 監視・スケジューリング層を Phase1 完了後に単独で査読できる。

### Phase 3: Release（GUI）
- Wails v2 GUI が `core/` + `store/` をそのまま import。near-realtime のグラフ/表を表示。
- Developer ID 署名 + notarize 済み `.app` として配布。
- リリース手順は nlink-jp リリースチェックリストに従う（zip + README 同梱、umbrella submodule 更新、
  org profile / web カタログ同期、check-org.sh）。

## 5. Required API Scopes / Permissions

**None（外部 API 依存なし）。**

- 完全ローカルのファイル読取りのみ。OAuth スコープ・API キー・IAM ロール等は不要。
- macOS の特別権限（Full Disk Access 等）も不要 — 読取り対象は利用者自身のホーム配下
  （`~/.claude/`・`~/Library/Application Support/Claude/`）であり、通常権限で読取れる。
- Windows / Linux も同様に、利用者自身のプロファイル配下の読取りのみで特別権限は不要。

## 6. Series Placement

Series: **util-series**

Reason: ローカルの JSONL ログを入力に、集計結果を整形テーブル / `--json` で stdout に出す
パイプ親和なデータ処理 CLI であり、util-series（pipe-friendly data transformation and
processing CLIs）の定義に合致する。data-analyzer / gem-query / llm-cli と同系統で棲み分けられる。

## 7. External Platform Constraints

- 外部 API のレート制限・UI レンダリング制約: **なし**（外部プラットフォーム非依存）。
- **唯一かつ最大の制約 = スキーマ drift**: Claude Code / Cowork の内部 JSONL・audit 形式は
  非公開であり、予告なく変わりうる。実測で `entrypoint` が
  {cli, claude-desktop, sdk-py} の3値を取り、`~/.claude/projects/` が CLI 専用でなく
  **デスクトップ版アプリの Code が主**であることも判明した（この事実自体が仕様非公開性の傍証）。
  - 対策: パーサは未知フィールドを寛容にスキップ、必須フィールド欠落レコードは
    warn してスキップ。`models` サブコマンドと検証ハーネスで drift（新モデル・スキーマ変更・
    価格変更）を検知できるようにする。
- **価格テーブル drift**: モデル単価は変わりうるため config.toml で上書き可能とし、
  同梱 default は実装時点の現行価格を焼き込む。
- **Windows / Linux のパス・プロファイル構造は推定（実機未検証）**: `code` ソース
  （`%USERPROFILE%\.claude`）は高確度で存在するが、`cowork`（`%APPDATA%\Claude` を想定、
  `Anthropic\Claude` 等の可能性）や、そもそも Windows デスクトップ版に local-agent-mode が
  存在するか自体は未確認。対策は build tag によるパス隔離 + `[sources]`/`--source-root` 上書き +
  `doctor` 診断で吸収し、Windows は experimental 扱いとする。パス区切りの差異は `filepath` で吸収。

---

## Discussion Log

- **発端**: Console / 課金 API が使えなくても、ローカルセッションログ解析でトークン使用量・
  コストが収集できると判明。Claude Code だけでなく Claude Cowork でも同様と想定。
- **実データ調査で確定した事実**:
  - `code` ソース = `~/.claude/projects/<encoded-cwd>/<sessionId>.jsonl`。`assistant` レコードの
    `message.usage` に完全なトークン内訳（input/output/cache-creation(1h,5m)/cache-read/
    server_tool_use/service_tier/model）が含まれる。コストは含まれない → 自前計算。
  - `cowork` ソース = `local-agent-mode-sessions/**/outputs/*.jsonl` が Code と**同一スキーマ**
    （subagents 含む）。同じパーサで両対応可能。
  - `cowork` の `audit.jsonl` は `total_cost_usd` / `modelUsage` を**算出済み**（HMAC 署名）→
    自前計算の**正解データ**として利用でき、テスト戦略の要になる。
  - 実測ボリューム: `code` 156 ファイル / 約 50MB。`entrypoint` 内訳は
    claude-desktop 6577 / cli 4984 / sdk-py 248。
- **設計判断の経緯**:
  - 当初 `source=cli` としたが、実測で `~/.claude/projects/` はデスクトップ版 Code が主と判明し、
    正しくは `source=code`（+ `entrypoint` サブ次元）に修正。
  - 「毎回横断集計は重くなる」「ソースセッションは自動削除されデータロストする」という指摘を受け、
    **永続ストア（SQLite）＋増分 ingest** を Phase1 の中核に格上げ。message_id 主キーで
    冪等 upsert・グローバル dedup、source 消失時も行を残す耐久性保証を採用。
  - ストアエンジンは DuckDB も候補（社内実績あり）だったが、単一バイナリ・notarize 容易性
    （Wails+go-duckdb の arrow 問題回避）を優先し **modernc.org/sqlite（pure-Go）** に決定。
  - コストは「API 定価換算（notional）」であり実請求額ではない旨を明示表示する方針。
  - Windows / Linux 対応も設計に織り込むと決定。テスト環境の用意が困難なため「設計上対応・実機未検証
    (experimental)」の位置づけとし、パス区切りは `filepath` に統一、プロファイル/ホーム構造の差異は
    `core/platform` の build tag で隔離、ディレクトリ名デコードに依存せずレコード内 `cwd`/`sessionId`
    を使用、推定パスは `[sources]`/`--source-root` 上書きと `doctor` 診断で吸収する方針。
- **確定事項**: 名称 `claude-usage-lens` / Go・util-series / 収集コアを独立モジュール化 /
  CLI 先行 → watch → Wails GUI / 個人ローカル先行（Source・Host で将来拡張余地）/
  ストア = SQLite（pure-Go）/ クロスプラットフォーム設計（macOS 主, Windows・Linux は experimental）。
