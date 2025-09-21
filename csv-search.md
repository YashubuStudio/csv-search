# csv-search Operations Cheat Sheet

このドキュメントは、リポジトリの現状と代表的なコマンドを即座に把握するためのまとめです。セットアップからCLI/HTTP運用、埋め込みライブラリとしての利用まで、ここだけを読めば一通りの操作が可能です。

## 現在の実装状況
- Go 1.24 以降でビルド可能なCLI/ライブラリ構成です（モジュールパス: `yashubustudio/csv-search`）。
- SQLite は `modernc.org/sqlite` ドライバを使用し、CGO不要で動作します。
- ONNX推論は `emb` パッケージと、設定で指定した ONNX Runtime / モデル / トークナイザを利用して行います。
- **DBファイルが存在しない場合でも、CLI/HTTP/APIの各入口で自動的にSQLiteファイルを作成しスキーマを初期化します。** 明示的に `init` サブコマンドを実行する必要はありませんが、手動実行も可能です。
- 主要ディレクトリの役割は次の通りです。
  | パス | 役割 |
  |------|------|
  | `main.go` | CLIエントリーポイント（`init` `ingest` `search` `serve` サブコマンド） |
  | `pkg/csvsearch/` | 外部公開用のGoパッケージ。DB初期化、インジェスト、検索、HTTPサーバ起動などのサービスロジックを提供 |
  | `internal/config/` | 設定ファイル読込と相対パス解決 |
  | `internal/database/` | SQLite接続・スキーマ定義 (`records` / `records_vec` / `records_fts` / `records_rtree`) |
  | `internal/ingest/` | CSV取り込み、ONNXエンコード、差分アップサート |
  | `internal/search/` | コサイン類似度ベースのベクトル検索 |
  | `internal/server/` | `/search` `/healthz` を提供するHTTPサーバ |
  | `emb/` | ONNXエンコーダのラッパー（原則変更不可） |

## 前提条件
1. **Goツールチェーン**: Go 1.24 以上。
2. **設定ファイル**: 既定は `./csv-search_config.json`。DB/エンコーダ/データセットを定義します。
3. **ONNX Runtime 共有ライブラリ**: `embedding.ort_lib` または `--ort-lib` で指定。Windowsの場合は同梱の `./onnixruntime-win/lib/onnxruntime.dll` を使用可能。
4. **エンコーダモデル & トークナイザ**: 例 `./models/bge-m3/model.onnx`, `./models/bge-m3/tokenizer.json`。
5. **CSVデータ**: 各データセット毎にCSV、ID列、テキスト列、メタデータ列などを準備。

> 設定ファイル内の相対パスは設定ファイルの場所を基準に解決されます。

## クイックコマンド
| 手順 | コマンド | 補足 |
|------|----------|------|
| 1. 依存取得 & ビルド | `go build -o csv-search .` | 生成されたバイナリは全サブコマンドを内包 |
| 2. DB初期化 | `./csv-search init --config ./csv-search_config.json` | 省略可。初回起動時に自動生成されます |
| 3. CSVインジェスト | `./csv-search ingest --config ./csv-search_config.json` | CSVを読み込み、埋め込み生成・差分適用 |
| 4. CLI検索 | `./csv-search search --config ./csv-search_config.json --query "Wi-Fi カフェ" --topk 10` | JSON形式で結果出力 |
| 5. HTTPサーバ | `./csv-search serve --config ./csv-search_config.json --addr :8080` | 起動前に必要なデータセットを自動インジェスト |

## CLIサブコマンド詳細
### `init`
- 主なフラグ: `--config`, `--db`
- 役割: 設定を読込んでSQLiteスキーマを初期化。DBファイル/ディレクトリを自動生成。

### `ingest`
- 主なフラグ: `--config`, `--db`, `--csv`, `--table`, `--id-col`, `--text-cols`, `--meta-cols`, `--lat-col`, `--lng-col`, `--batch`, `--ort-lib`, `--model`, `--tokenizer`, `--max-seq-len`
- 役割: CSV行を取り込み、ONNXでベクトル化、`records` 系テーブルに保存。IDと内容ハッシュで差分更新。

### `search`
- 主なフラグ: `--config`, `--db`, `--query`, `--table`, `--topk`, `--filter`, エンコーダ関連フラグ
- 役割: クエリをエンコードし、類似度順に結果をJSONで出力。`--filter field=value` を複数指定するとAND条件。

### `serve`
- 主なフラグ: `--config`, `--db`, `--addr`, `--table`, `--topk`, `--request-timeout`, `--shutdown-timeout`, エンコーダ関連フラグ
- 役割: HTTP APIを提供。起動時に自動インジェストを実行し、SIGINT/SIGTERMでグレースフルに終了。

## HTTP API
- `GET /search`: クエリパラメータ `q|query`, `dataset|table`, `topk`, `filter=field=value` (複数指定可)。
- `POST /search`: `{"query":"テキスト","dataset":"name","topk":5,"filters":{"列":"値"}}`。配列形式のフィルタも許容。
- `POST /query`: `/search` のエイリアス。`max_results` や `summary_only` を許容。
- `GET /healthz`: 常に `200 OK` と `ok` を返却。

## ライブラリとしての利用例
```go
svc, err := csvsearch.NewService(csvsearch.ServiceOptions{
    Config:   csvsearch.ConfigReference{Path: "./csv-search_config.json"},
    Database: csvsearch.DatabaseOptions{Path: ""},
})
if err != nil { panic(err) }
defer svc.Close()

ctx := context.Background()
if err := svc.InitDatabase(ctx, csvsearch.InitDatabaseOptions{}); err != nil { panic(err) }
if _, err := svc.Ingest(ctx, csvsearch.IngestOptions{}); err != nil { panic(err) }
results, err := svc.Search(ctx, csvsearch.SearchOptions{Query: "Wi-Fi カフェ"})
if err != nil { panic(err) }
_ = results
```
- `DatabaseOptions.Handle` に既存の `*sql.DB` を渡すことも可能です。
- `EncoderOptions.Instance` を利用すれば、外部で初期化した `*emb.Encoder` を再利用できます。
- `Service.StartServer` は自動インジェスト後にHTTPサーバを起動します。カスタムMuxに組み込みたい場合は `Service.NewAPIServer` を使用してください。

## トラブルシューティング
- `encoder configuration is incomplete`: `OrtDLL`, `ModelPath`, `TokenizerPath` が到達可能か確認。
- `filter must be in the form field=value`: CLIの `--filter` 指定やAPIリクエストのフォーマットを修正。
- `id column is empty`: CSVにユニークID列が存在するか確認。
- Windows向けビルド: `GOOS=windows` を指定し、ONNX Runtime DLLを実行ファイルと同じ場所に配置。

このチートシートを参照すれば、現在の実装状態を理解しつつ、必要なコマンドと設定項目を即座に把握できます。
