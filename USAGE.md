# USAGE

本手順はリポジトリ同梱の`/csv/image.csv`を取り込み、列「実行内容」を意味検索の対象に設定したうえで、必要に応じて列「得意先名」でアナログな絞り込みを行い、列「受付No」を含む行単位の検索結果を得るまでの流れを説明します。【F:csv/image.csv†L1-L16】【F:main.go†L73-L203】

## 1. ビルドと前提ファイルの準備

1. Go 1.24 以降と、ONNX Runtime の共有ライブラリ・エンコーダーモデル・トークナイザファイルを利用できる環境を用意します。これらは埋め込みの生成と検索時に必須です。【F:main.go†L73-L203】
2. リポジトリのルートで次のコマンドを実行し、CLI バイナリ `csv-search` を生成します。

   ```bash
   go build -o csv-search .
   ```

3. 実行時には以下のファイルを同じディレクトリか任意のパスに配置しておきます。
   - `csv-search`（ビルド済みバイナリ）【F:main.go†L19-L47】
   - ONNX Runtime 共有ライブラリ（`--ort-lib` で指定）【F:main.go†L73-L173】【F:emb/emb.go†L39-L123】
   - エンコーダーモデル（`--model` で指定）【F:main.go†L73-L203】【F:emb/emb.go†L45-L123】
   - トークナイザ設定（`--tokenizer` で指定）【F:main.go†L73-L203】【F:emb/emb.go†L45-L123】
   - 取り込み対象の CSV ファイル `/csv/image.csv`【F:csv/image.csv†L1-L16】

## 2. データベースの初期化

検索結果を保存する SQLite データベースを作成します。以下の例では `data/image.db` を利用しています。

```bash
./csv-search init --db ./data/image.db
```

コマンドはデータベースを作成し、レコード本体・ベクトル・全文検索・位置情報テーブルなど検索に必要なスキーマを初期化します。【F:main.go†L49-L145】【F:internal/database/schema.go†L10-L38】

## 3. `/csv/image.csv` の取り込み

列「受付No」を ID、「実行内容」を埋め込み対象に設定し、行全体（列「受付No」「得意先名」「実行内容」）をメタデータとして保持するために、次のように `ingest` コマンドを実行します。【F:csv/image.csv†L1-L16】【F:main.go†L73-L145】

```bash
./csv-search ingest \
  --db ./data/image.db \
  --csv ./csv/image.csv \
  --ort-lib /path/to/libonnxruntime.so \
  --model /path/to/encoder.onnx \
  --tokenizer /path/to/tokenizer.json \
  --table textile_jobs \
  --id-col 受付No \
  --text-cols "実行内容" \
  --meta-cols "*"
```

- `--id-col 受付No` で受付番号を主キーに設定し、検索結果で必ず参照できるようにします。【F:csv/image.csv†L1-L16】【F:main.go†L83-L138】
- `--text-cols "実行内容"` により、埋め込み生成の対象列を「実行内容」だけに絞ります。【F:main.go†L73-L145】
- `--meta-cols "*"` を指定すると CSV の全列が JSON メタデータに残るため、後段で「得意先名」による絞り込みや「受付No」を含む行出力が可能になります。【F:main.go†L83-L138】【F:internal/ingest/ingest.go†L224-L353】

処理が完了すると `ingested data from ./csv/image.csv` が出力され、行ごとのベクトル・メタデータが `data/image.db` に保存されます。【F:main.go†L141-L145】

## 4. 検索と出力

`search` コマンドで「実行内容」を対象に意味検索を行います。`--table` には取り込み時に指定した論理テーブル名（上記例では `textile_jobs`）を指定します。【F:main.go†L148-L203】

```bash
./csv-search search \
  --db ./data/image.db \
  --query "漂白" \
  --topk 5 \
  --ort-lib /path/to/libonnxruntime.so \
  --model /path/to/encoder.onnx \
  --tokenizer /path/to/tokenizer.json \
  --table textile_jobs
```

結果は JSON 配列で標準出力に返され、各要素の `fields` に「受付No」「得意先名」「実行内容」が含まれます。これにより行単位で内容を確認でき、`id` としても「受付No」が保持されます。【F:main.go†L148-L203】【F:internal/search/vector.go†L15-L114】

### 任意のアナログ絞り込み（得意先名）

検索結果は標準出力に出るため、CLI の外側で `jq` や `grep` などを用いたアナログ絞り込みが可能です。例えば「得意先名」に「艶栄工業㈱」を含む行だけを抽出するには次のようにします。

```bash
./csv-search search ... | jq "map(select(.fields[\"得意先名\"] | contains(\"艶栄工業㈱\")))"
```

この操作により、意味検索で抽出した候補から任意の得意先名に該当する行のみを確認できます。【F:csv/image.csv†L1-L16】【F:internal/search/vector.go†L15-L114】

### 受付No を含む行出力の確認

`--meta-cols "*"` を指定しているため、上記の検索結果・絞り込み結果のいずれでも `fields["受付No"]` に受付番号が含まれます。必要に応じて次のように整形し、行ごとの主要情報を表示できます。

```bash
./csv-search search ... | jq -r ".[] | \"受付No: \(.fields[\"受付No\"]) / 得意先名: \(.fields[\"得意先名\"]) / 実行内容: \(.fields[\"実行内容\"])\""
```

これにより、「受付No」「得意先名」「実行内容」を同一行で確認しながら検索結果を活用できます。【F:csv/image.csv†L1-L16】【F:internal/search/vector.go†L15-L114】
