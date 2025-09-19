# SemanticGeoSearchDB (csv-search)

## 概要
SemanticGeoSearchDBは、CSVファイルを取り込むだけでローカルのSQLiteデータベースへ正規化・差分同期し、テキストと位置情報を活用した高度な検索を単体パッケージで提供することを目指すプロジェクトです。【F:basePlan.md†L3-L24】 ネットワーク接続やクラウドサービスに依存せず、エンドユーザーが即座に検索環境を構築できることを目的としています。【F:basePlan.md†L12-L24】

## コンセプトとシステム構成
CSVインポート → 差分検出・正規化 → ONNXによる埋め込み生成 → SQLite（items/items_vec/items_fts/items_rtree）への格納 → CLI/将来のAPIやUIからの検索という流れで動作します。【F:basePlan.md†L32-L52】【F:basePlan.md†L71-L111】 コア技術としてGo言語、SQLite（WAL/R\*Tree/FTS5）、ONNX Runtimeを採用し、クロスプラットフォームな単一バイナリ配布を想定しています。【F:basePlan.md†L56-L68】

## 実装済みの主な機能
- **CLIコマンド**: `init`でスキーマ初期化、`ingest`でCSV取り込みと埋め込み生成、`search`でベクトル類似検索が実行できます。【F:main.go†L18-L224】
- **SQLiteスキーマ**: `items`（メタデータ＋差分検出ハッシュ）、`items_vec`（埋め込みBLOB）、`items_fts`（全文検索）、`items_rtree`（位置情報）など、検索機能に必要な構造を用意しています。【F:internal/database/schema.go†L10-L45】
- **CSV取り込みパイプライン**: 列マッピングを柔軟に指定し、レコードごとのハッシュ比較で差分検出、ONNXエンコーダによるベクトル生成、FTS・R\*Tree・ベクトルテーブルの更新をトランザクションで行います。【F:internal/ingest/ingest.go†L21-L178】【F:internal/ingest/ingest.go†L374-L515】
- **埋め込みの永続化と類似度計算**: ベクトルはリトルエンディアンのBLOBへシリアライズして保存し、検索時にデシリアライズしてコサイン類似度でランキングします。【F:internal/vector/vector.go†L9-L32】【F:internal/vector/similarity.go†L5-L22】【F:internal/search/vector.go†L29-L129】

## 使い方
### 1. ビルドと事前準備
Go 1.24以降とONNX Runtime（共有ライブラリ）、推奨エンコーダモデル、トークナイザファイルを用意してください。本プロジェクトは`sugarme/tokenizer`、`yalue/onnxruntime_go`、`modernc.org/sqlite`などの依存関係を利用します。【F:go.mod†L5-L19】 バイナリは`go build`で生成できます。【F:basePlan.md†L246-L251】

### 2. データベース初期化
```bash
./csv-search init --db ./data/app.db
```
指定したパスにSQLiteデータベースを作成し、必要なテーブル・仮想テーブルを初期化します。【F:main.go†L49-L70】【F:internal/database/database.go†L14-L52】【F:internal/database/schema.go†L10-L45】

### 3. CSV取り込み
```bash
./csv-search ingest \
  --db ./data/app.db \
  --csv ./sample/items.csv \
  --ort-lib ./onnxruntime/libonnxruntime.so \
  --model ./models/encoder.onnx \
  --tokenizer ./models/tokenizer.json \
  --id-col id --title-col title --body-col body \
  --lat-col lat --lng-col lng --batch 1000
```
列マッピングやバッチサイズを調整しつつ、変更があった行のみを再エンコードしてデータベースへ反映します。【F:main.go†L72-L155】【F:internal/ingest/ingest.go†L73-L178】【F:internal/ingest/ingest.go†L374-L515】

### 4. ベクトル検索
```bash
./csv-search search \
  --db ./data/app.db \
  --query "Wi-Fi カフェ" \
  --topk 10 \
  --ort-lib ./onnxruntime/libonnxruntime.so \
  --model ./models/encoder.onnx \
  --tokenizer ./models/tokenizer.json
```
クエリ文をエンコードしたベクトルと保存済みベクトルのコサイン類似度でランキングし、JSONで結果を取得できます。【F:main.go†L157-L210】【F:internal/search/vector.go†L29-L129】

## データフローと将来構想
ディレクトリ構成や設定ファイルのアイデア、REST API・Web UI拡張、さらなるランキング強化など、今後の拡張計画も仕様書に整理されています。【F:basePlan.md†L201-L242】【F:basePlan.md†L255-L285】 優先実装順として、差分更新や検索パイプライン強化、フィルタリング、API化、Web UIの追加が計画されています。【F:basePlan.md†L276-L285】

## ライセンス
本リポジトリ内のライセンス情報や再配布条件については、各依存パッケージおよび同梱ファイルのライセンスを参照してください。
