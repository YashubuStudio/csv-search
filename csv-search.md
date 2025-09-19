# csv-search Application Overview

## 概要
csv-search は CSV データを取り込み、ONNX Runtime を用いて文書ベクトルを生成し、SQLite に保存された埋め込みを使ってセマンティック検索を提供するアプリケーションです。CLI ツールとして単体で利用できるほか、`pkg/csvsearch` パッケージを経由して別の Go アプリケーションへ統合できます。

## 主な機能
- **データベース初期化**: SQLite スキーマの作成・更新。CLI の `init` サブコマンドおよび `Service.InitDatabase` で実施。【F:pkg/csvsearch/init.go†L7-L26】【F:main.go†L43-L73】
- **CSV 取り込み**: CSV 行を読み込み、ONNX エンコーダーでテキストをベクトル化してデータベースへアップサート。`ingest` サブコマンドまたは `Service.Ingest` で利用可能。【F:pkg/csvsearch/ingest.go†L16-L121】【F:main.go†L75-L137】
- **セマンティック検索**: クエリをベクトル化してコサイン類似度でランキング。`search` サブコマンドおよび `Service.Search` が提供し、結果は JSON 配列として取得できます。【F:pkg/csvsearch/search.go†L8-L70】【F:main.go†L139-L189】
- **HTTP API サーバー**: REST エンドポイント (`/search`, `/healthz`) を提供。CLI の `serve` サブコマンドか `Service.StartServer` / `Service.NewAPIServer` で起動・統合可能。`internal/server.Server.Handler` により既存の HTTP サーバーへマウントすることもできます。【F:pkg/csvsearch/server.go†L7-L101】【F:internal/server/server.go†L70-L104】【F:main.go†L191-L240】

## 必要なもの
- **SQLite ドライバー**: `modernc.org/sqlite` を使用しており、CGO なしでビルド可能です。【F:pkg/csvsearch/service.go†L9-L11】【F:go.mod†L6-L13】
- **ONNX Runtime DLL/共有ライブラリ**: `emb.Encoder` の `OrtDLL` へパスを指定する必要があります。Windows 向け `.dll` などを想定しています。【F:emb/emb.go†L1-L105】【F:pkg/csvsearch/service.go†L88-L116】
- **エンコーダーモデル/トークナイザー**: ONNX モデル (`model.onnx`) と `tokenizer.json` を `EncoderConfig` または設定ファイルから指定。【F:pkg/csvsearch/service.go†L88-L116】【F:internal/config/config.go†L15-L52】
- **設定ファイル (任意)**: `config.json` 形式でデータベースパス、エンコーディング資産、デフォルトデータセット、検索設定を定義できます。【F:internal/config/config.go†L11-L66】
- **CSV データ**: 取り込み対象。デフォルトでは `id` 列とテキスト列（`text-cols` or `meta-cols`）が必要です。【F:pkg/csvsearch/ingest.go†L48-L103】

## 入出力
- **取り込み (`Ingest`) 入力**
  - `IngestOptions` で CSV パス、ID 列、テキスト列、メタデータ列、緯度経度列などを指定。省略時は設定ファイルのデータセット定義が自動適用されます。【F:pkg/csvsearch/ingest.go†L16-L103】
  - 出力として `IngestSummary` を返し、使用されたパラメータを参照できます。【F:pkg/csvsearch/ingest.go†L18-L27】【F:pkg/csvsearch/ingest.go†L105-L121】
- **検索 (`Search`) 入力/出力**
  - `SearchOptions` はクエリ文字列、対象データセット、TopK、フィルター（`Filter` 構造体）を受け取ります。【F:pkg/csvsearch/search.go†L24-L37】
  - 戻り値は `Result` のスライスで、ID・スコア・メタデータ・位置情報を含む JSON 互換構造です。【F:pkg/csvsearch/search.go†L10-L22】【F:pkg/csvsearch/search.go†L39-L68】
- **HTTP API**
  - `/search`: GET/POST 共通。`q`/`query`、`dataset`/`table`、`topk`、`filter=field=value` をサポート。【F:internal/server/server.go†L96-L165】
  - `/healthz`: サーバーヘルスチェック用。常に `200 OK` を返します。【F:internal/server/server.go†L88-L94】

## Go 統合ポイント
- **Service 構築**: `NewService(ServiceOptions)` で設定・DB・エンコーダー（遅延初期化）を一括管理。外部から提供された DB/エンコーダーを再利用できます。【F:pkg/csvsearch/service.go†L33-L117】
- **データベースパス取得**: `Service.DatabasePath()` で実際に使用している SQLite ファイルのパスを取得可能（外部接続時は空文字）。【F:pkg/csvsearch/service.go†L61-L68】
- **HTTP 統合**: `APIServer.Handler()` を既存ルーターに登録、または `APIServer.Serve(ctx)` で単独起動。【F:pkg/csvsearch/server.go†L23-L55】
- **Graceful Shutdown**: `StartServer` は `context.Context` を介して OS シグナルなどに対応し、自動でデータセットを取り込みます（設定に CSV パスがある場合）。【F:pkg/csvsearch/server.go†L57-L101】

## CLI サブコマンド概要
| コマンド | 主なオプション | 概要 |
|----------|----------------|------|
| `init`   | `--config`, `--db` | SQLite スキーマ初期化。デフォルトは `config.json` と `data/app.db`。【F:main.go†L43-L73】 |
| `ingest` | `--csv`, `--table`, `--text-cols`, `--meta-cols`, `--lat-col`, `--lng-col`, `--ort-lib`, `--model`, `--tokenizer`, `--max-seq-len` | CSV を読み込みベクトル化し保存。終了後に取り込み概要を出力。【F:main.go†L75-L137】 |
| `search` | `--query`, `--table`, `--topk`, `--filter`, エンコーダー関連オプション | クエリを検索し、JSON 形式で結果を標準出力。【F:main.go†L139-L189】 |
| `serve`  | `--addr`, `--table`, `--topk`, `--request-timeout`, `--shutdown-timeout`, エンコーダー関連オプション | HTTP API サーバーを起動。Ctrl+C などでグレースフル終了。【F:main.go†L191-L240】 |

## ビルドに関する注意
- Windows 向け `.exe` ビルドを想定する場合、`GOOS=windows` とし、`EncoderConfig` の `OrtLibrary` に ONNX Runtime の DLL を指定してください。`modernc.org/sqlite` を採用しているため、CGO 無効のままクロスビルドが可能です。【F:pkg/csvsearch/service.go†L9-L11】【F:pkg/csvsearch/service.go†L88-L116】
- ONNX ランタイム DLL、モデルファイル、トークナイザーをアプリと同じディレクトリに配置するか、設定ファイルで絶対パス/相対パスを指定してください。【F:internal/config/config.go†L40-L52】【F:pkg/csvsearch/service.go†L88-L116】
