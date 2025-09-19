# SemanticGeoSearchDB (csv-search)

## 概要
SemanticGeoSearchDBは、CSVファイルを取り込むだけでローカルのSQLiteデータベースへ正規化・差分同期し、テキストと位置情報を活用した高度な検索を単体パッケージで提供することを目指すプロジェクトです。【F:basePlan.md†L3-L24】 ネットワーク接続やクラウドサービスに依存せず、エンドユーザーが即座に検索環境を構築できることを目的としています。【F:basePlan.md†L12-L24】

## コンセプトとシステム構成
CSVインポート → 差分検出・正規化 → ONNXによる埋め込み生成 → SQLite（records/records_vec/records_fts/records_rtree）への格納 → CLI/将来のAPIやUIからの検索という流れで動作します。【F:basePlan.md†L32-L52】【F:basePlan.md†L71-L111】 コア技術としてGo言語、SQLite（WAL/R\*Tree/FTS5）、ONNX Runtimeを採用し、クロスプラットフォームな単一バイナリ配布を想定しています。【F:basePlan.md†L56-L68】

## 実装済みの主な機能
- **CLIコマンド**: `init`でスキーマ初期化、`ingest`でCSV取り込みと埋め込み生成、`search`でベクトル類似検索が実行できます。【F:main.go†L18-L350】
- **SQLiteスキーマ**: `records`（メタデータ＋差分検出ハッシュ）、`records_vec`（埋め込みBLOB）、`records_fts`（全文検索）、`records_rtree`（位置情報）など、検索機能に必要な構造を用意しています。【F:internal/database/schema.go†L9-L40】
- **CSV取り込みパイプライン**: 列の選択やメタデータ保持を柔軟に指定し、レコードごとのハッシュ比較で差分検出、ONNXエンコーダによるベクトル生成、FTS・R\*Tree・ベクトルテーブルの更新をトランザクションで行います。【F:internal/ingest/ingest.go†L22-L298】
- **埋め込みの永続化と類似度計算**: ベクトルはリトルエンディアンのBLOBへシリアライズして保存し、検索時にデシリアライズしてコサイン類似度でランキングします。【F:internal/vector/vector.go†L9-L32】【F:internal/vector/similarity.go†L5-L22】【F:internal/search/vector.go†L25-L115】

## 使い方
### 1. ビルドと事前準備
Go 1.24以降とONNX Runtime（共有ライブラリ）、推奨エンコーダモデル、トークナイザファイルを用意してください。本プロジェクトは`sugarme/tokenizer`、`yalue/onnxruntime_go`、`modernc.org/sqlite`などの依存関係を利用します。【F:go.mod†L5-L19】 バイナリは`go build`で生成できます。【F:basePlan.md†L246-L251】

### 2. データベース初期化
```bash
./csv-search init --db ./data/app.db
```
指定したパスにSQLiteデータベースを作成し、必要なテーブル・仮想テーブルを初期化します。【F:main.go†L49-L88】【F:internal/database/database.go†L14-L52】【F:internal/database/schema.go†L10-L45】

### 3. CSV取り込み
```bash
./csv-search ingest \
  --db ./data/app.db \
  --csv ./sample/image.csv \
  --ort-lib ./onnxruntime/libonnxruntime.so \
  --model ./models/encoder.onnx \
  --tokenizer ./models/tokenizer.json \
  --table images \
  --id-col image_id \
  --text-cols "title,caption" \
  --meta-cols "image_id,title,caption,path,tags" \
  --lat-col lat --lng-col lng --batch 1000
```
テーブル名や埋め込み対象の列、保持するメタデータ列、緯度経度列、バッチサイズをCLIフラグで柔軟に指定しつつ、変更があった行のみを差分検出して再エンコードし、トランザクションで反映します。【F:main.go†L90-L224】【F:internal/ingest/ingest.go†L22-L298】

### 4. ベクトル検索
```bash
./csv-search search \
  --db ./data/app.db \
  --query "Wi-Fi カフェ" \
  --topk 10 \
  --ort-lib ./onnxruntime/libonnxruntime.so \
  --model ./models/encoder.onnx \
  --tokenizer ./models/tokenizer.json \
  --table images
```
クエリ文をエンコードしたベクトルと指定したテーブルの保存済みベクトルのコサイン類似度でランキングし、関連メタデータを含むJSONで結果を取得できます。【F:main.go†L353-L414】【F:internal/search/vector.go†L25-L115】

検索対象に含まれる任意のメタデータ列で絞り込みたい場合は、`--filter "列名=値"` を繰り返し指定すると検索処理の内部で AND 条件として適用されます。例えば `--filter "得意先名=艶栄工業㈱"` を付与すると、該当する得意先名のレコードのみが結果に含まれます。【F:main.go†L365-L413】【F:internal/search/vector.go†L16-L114】

### 5. サーバーモード（HTTP API）

`serve` コマンドを使うと、設定ファイルやフラグで指定したデータベースとエンコーダー資産を読み込み、ONNX エンコーダーをメモリ上に保持したまま HTTP サーバーとして待機させられます。【F:main.go†L240-L350】【F:internal/server/server.go†L20-L162】 既定のデータセットやトップK件数、リクエストタイムアウトは `config.json` やフラグで上書きでき、停止シグナル受信時は安全にシャットダウンします。【F:main.go†L260-L350】【F:internal/server/server.go†L86-L105】

```bash
./csv-search serve \
  --db ./data/app.db \
  --addr :8080 \
  --ort-lib ./onnxruntime/libonnxruntime.so \
  --model ./models/encoder.onnx \
  --tokenizer ./models/tokenizer.json
```

サーバー起動後は次のようなエンドポイントが利用できます。

- `GET /search` — クエリ文字列 `q`（または `query`）、`topk`、`table`/`dataset`、`filter=列名=値` を指定して検索します。【F:internal/server/server.go†L166-L188】
- `POST /search` — JSON で `{"query": "Wi-Fi カフェ", "dataset": "images", "topk": 5, "filters": {"得意先名": "艶栄工業㈱"}}` のように送信できます。【F:internal/server/server.go†L191-L226】
- `GET /healthz` — ヘルスチェック用の軽量エンドポイントです。【F:internal/server/server.go†L108-L111】

`filter` パラメータは CLI と同じく `フィールド=値` 形式を複数指定でき、JSON の `filters` マップと合わせて内部で AND 条件として処理されます。【F:internal/server/server.go†L120-L224】【F:internal/server/server.go†L229-L248】 レスポンスは CLI の `search` と同様に検索結果配列の JSON を返すため、既存のパイプラインにそのまま組み込めます。【F:internal/server/server.go†L150-L162】【F:internal/search/vector.go†L25-L115】

## データフローと将来構想
ディレクトリ構成や設定ファイルのアイデア、REST API・Web UI拡張、さらなるランキング強化など、今後の拡張計画も仕様書に整理されています。【F:basePlan.md†L201-L242】【F:basePlan.md†L255-L285】 優先実装順として、差分更新や検索パイプライン強化、フィルタリング、API化、Web UIの追加が計画されています。【F:basePlan.md†L276-L285】

## ライセンス
本リポジトリ内のライセンス情報や再配布条件については、各依存パッケージおよび同梱ファイルのライセンスを参照してください。
