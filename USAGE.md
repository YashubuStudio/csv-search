# USAGE

## ビルド方法
- Go 1.24 以降を用意し、依存パッケージ（ONNX Runtime バインディング、トークナイザ、SQLite ドライバなど）を取得できる環境で `go build -o csv-search .` を実行すると CLI バイナリを生成できます。【F:go.mod†L1-L19】【F:basePlan.md†L246-L251】
- ビルドしたバイナリは単体で動作しますが、実行時に ONNX Runtime の共有ライブラリ、エンコーダーモデル、トークナイザファイル、取り込み対象の CSV ファイルを配置する必要があります。【F:main.go†L73-L145】【F:emb/emb.go†L29-L121】

### 配布・実行時に同梱するファイル
| 種別 | 役割 |
| --- | --- |
| `csv-search`（ビルド済みバイナリ） | CLI 本体。サブコマンド `init`/`ingest`/`search` を提供します。【F:main.go†L19-L47】 |
| ONNX Runtime 共有ライブラリ（例: `libonnxruntime.so` / `onnxruntime.dll`） | 埋め込み生成に必要。`--ort-lib` フラグでパスを指定します。【F:main.go†L73-L173】【F:emb/emb.go†L39-L55】 |
| エンコーダーモデル（例: `models/encoder.onnx`） | クエリ・ドキュメントのベクトル化に使用。`--model` フラグで読み込みます。【F:main.go†L73-L203】【F:emb/emb.go†L45-L123】 |
| トークナイザ設定（例: `models/tokenizer.json`） | テキストをトークン列に変換。`--tokenizer` フラグで指定します。【F:main.go†L73-L203】【F:emb/emb.go†L45-L123】 |
| 取り込み対象 CSV | `ingest` コマンドで読み込む原データ。`--csv` フラグでパスを指定します。【F:main.go†L73-L145】【F:internal/ingest/ingest.go†L231-L353】 |
| SQLite データベース（例: `data/app.db`） | `init` または `ingest` がスキーマを初期化し、検索対象データを保持します。【F:main.go†L50-L145】【F:internal/database/database.go†L14-L52】【F:internal/database/schema.go†L10-L38】 |

## 基本フローとコマンド
1. **データベース初期化（`init`）**
   ```bash
   ./csv-search init --db ./data/app.db
   ```
   - `--db` でデータベースファイルを指定（既定値は `data/app.db`）。初期化時にディレクトリを自動作成し、`records`/`records_vec`/`records_fts`/`records_rtree` などのテーブル・仮想テーブルを作成します。【F:main.go†L50-L70】【F:internal/database/database.go†L14-L43】【F:internal/database/schema.go†L10-L38】
   - 正常終了すると `database initialized at ...` が標準出力に表示されます。【F:main.go†L66-L70】

2. **CSV 取り込みと埋め込み生成（`ingest`）**
   ```bash
   ./csv-search ingest \
     --db ./data/app.db \
     --csv ./sample/places.csv \
     --ort-lib ./onnxruntime/libonnxruntime.so \
     --model ./models/encoder.onnx \
     --tokenizer ./models/tokenizer.json \
     --table places \
     --id-col place_id \
     --text-cols "title,description" \
     --meta-cols "*" \
     --lat-col lat --lng-col lng --batch 1000
   ```
   - `--csv`（必須）で読み込むファイル、`--table` で論理テーブル名（既定値は `default`）、`--id-col` で主キー列を指定します。【F:main.go†L73-L145】
   - `--text-cols` を省略すると、メタデータ列から ID・緯度経度を除いた列が埋め込み対象として自動選択されます。`--meta-cols "*"`（既定値）は CSV の全列をメタデータとして保存します。【F:main.go†L83-L138】【F:internal/ingest/ingest.go†L224-L299】
   - 緯度・経度列を指定すると、値をパースして `records_rtree` に格納し、ジオ検索に備えます（空欄は無視）。【F:internal/ingest/ingest.go†L336-L477】
   - 1 行ごとに ID、メタデータ、埋め込み対象テキスト、座標を組み立て、ハッシュ値で変更有無を判定することで差分同期を実現します。【F:internal/ingest/ingest.go†L301-L400】
   - 新規または更新レコードはメタデータ JSON、FTS インデックス、R\*Tree、ベクトルテーブルへ一括反映されます。【F:internal/ingest/ingest.go†L417-L494】
   - 処理が完了すると `ingested data from ...` が表示されます。【F:main.go†L141-L145】

   **CSV 入力例**
   ```csv
   place_id,title,description,lat,lng
   1,カフェA,Wi-Fi と電源ありのカフェ,35.6812,139.7671
   2,図書館B,静かな自習スペースを備えた図書館,35.6895,139.6917
   ```

3. **ベクトル検索（`search`）**
   ```bash
   ./csv-search search \
     --db ./data/app.db \
     --query "Wi-Fi カフェ" \
     --topk 5 \
     --ort-lib ./onnxruntime/libonnxruntime.so \
     --model ./models/encoder.onnx \
     --tokenizer ./models/tokenizer.json \
     --table places
   ```
   - `--query`（必須）に検索テキスト、`--topk` で取得件数、`--table` で対象データセットを指定します。【F:main.go†L148-L203】
   - コマンドはクエリを同じエンコーダーでベクトル化し、コサイン類似度が高い順に結果を整形して JSON として標準出力へ書き出します。【F:main.go†L181-L202】【F:internal/search/vector.go†L15-L114】

   **出力例**
   ```json
   [
     {
       "dataset": "places",
       "id": "1",
       "fields": {
         "place_id": "1",
         "title": "カフェA",
         "description": "Wi-Fi と電源ありのカフェ"
       },
       "score": 0.87,
       "lat": 35.6812,
       "lng": 139.7671
     }
   ]
   ```
   - 各要素にはデータセット名、レコード ID、保存済みメタデータ、類似度スコア、存在すれば緯度経度が含まれます。【F:internal/search/vector.go†L15-L114】

## 動作確認のポイント
- `init` の後に SQLite ファイルが生成されていること、`ingest` 後に `records` 系テーブルへデータが追加されることを確認するとパイプライン全体の通し動作を検証できます。【F:main.go†L50-L145】【F:internal/database/schema.go†L10-L38】【F:internal/ingest/ingest.go†L417-L494】
- `search` の JSON 出力を見て期待したフィールドやスコアが得られているかをチェックし、必要に応じて `--text-cols` や `--meta-cols` の設定を調整してください。【F:main.go†L83-L203】【F:internal/search/vector.go†L15-L114】
