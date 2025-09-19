# USAGE

本手順はリポジトリ同梱の`/csv/image.csv`を取り込み、列「実行内容」を意味検索の対象に設定したうえで、必要に応じて列「得意先名」でシステム内フィルタリングを行い、列「受付No」を含む行単位の検索結果を得るまでの流れを説明します。

## 1. ビルドと前提ファイルの準備

1. Go 1.24 以降と、ONNX Runtime の共有ライブラリ・エンコーダーモデル・トークナイザファイルを利用できる環境を用意します。リポジトリには Windows 向けの `onnixruntime-win/lib/onnxruntime.dll`、推奨モデル `models/bge-m3/model.onnx`・`model.onnx_data`、および `tokenizer.json` が同梱されているため、まずはこれらのパスを利用する想定で進めます。
2. リポジトリのルートで次のコマンドを実行し、CLI バイナリ `csv-search` を生成します。

   ```bash
   go build -o csv-search .
   ```

3. 実行時には以下のファイルを同じディレクトリか任意のパスに配置しておきます。
   - `csv-search`（ビルド済みバイナリ）
   - ONNX Runtime 共有ライブラリ `onnixruntime-win/lib/onnxruntime.dll`（`config.json` の `embedding.ort_lib` で参照）
   - エンコーダーモデル `models/bge-m3/model.onnx` と `model.onnx_data`
   - トークナイザ設定 `models/bge-m3/tokenizer.json`
   - 取り込み対象の CSV ファイル `/csv/image.csv`

## 2. `config.json` の確認とカスタマイズ

リポジトリ直下に用意した `config.json` は、データベースの保存先や ONNX 関連ファイル、取り込み対象 CSV、デフォルトの検索トップ件数などをまとめて管理します。 CLI コマンドは起動時にこのファイルを自動的に読み込み（存在しない場合は従来のフラグのみで動作）し、指定がないフラグ値を設定値で補います。

主要な設定項目は次の通りです。

- `database.path`: SQLite ファイルの出力先。
- `embedding` ブロック: ONNX Runtime DLL、エンコーダーモデル、トークナイザ、最大トークン長の既定値。
- `default_dataset`: `ingest` / `search` コマンドが参照する既定データセット名。
- `datasets.<name>`: CSV パス、ID 列、埋め込み対象列、保持するメタデータ列、バッチサイズなどの取り込み設定。
- `search.default_topk`: 検索の既定件数。

複数のデータセットを扱う場合は `datasets` に別名を追加し、`default_dataset` を切り替えるか、コマンド実行時に `--table` で明示的に指定してください。

## 3. 初期化と CSV 取り込み

設定済みの `config.json` が存在する場合、最小限のコマンドで初期化と取り込みが実行できます。

```bash
./csv-search init
./csv-search ingest
```

`init` は `database.path` に SQLite ファイルを作成し、スキーマを初期化します。 `ingest` は `datasets.<name>` の設定を基に CSV を読み込み、必要な列から埋め込みを生成してレコード群を保存します。

`config.json` 以外を使いたい場合は、`--config ./path/to/custom.json` を各コマンドに付与してください。個別のフラグ (`--db` や `--csv` など) を併用すると、その値が設定ファイルより優先されます。

## 4. 検索と結果の活用

設定ファイルで `search.default_topk` と `default_dataset` を指定しているため、検索はクエリのみで実行できます。

```bash
./csv-search search --query "漂白"
```

コマンドはクエリをエンコードし、保存済みベクトルとのコサイン類似度でランキングした JSON 配列を標準出力へ返します。結果には `fields` に「受付No」「得意先名」「実行内容」が含まれ、`id` としても「受付No」が保持されます。 `--topk`、`--table`、`--db` などを指定すると、設定ファイルの値を上書きできます。

### メタデータによるシステム内フィルタリング（得意先名）

`search` コマンドは `--filter` オプションを複数指定でき、`"列名=値"` 形式でメタデータを絞り込んだ結果だけを返します。例えば「得意先名」が「艶栄工業㈱」の行に限定する場合は次のように実行します。

```bash
./csv-search search --query "漂白" --filter "得意先名=艶栄工業㈱"
```

フィルターは検索処理の内部で適用されるため、`jq` や `grep` など外部ツールに頼らずに目的の得意先名だけを抽出できます。複数条件を AND で組み合わせたい場合は `--filter` を繰り返し指定してください。

### 受付No を含む行出力の確認

`config.json` の `meta_columns` を `"*"` にしているため、検索結果の `fields` に元の CSV 列がすべて残ります。必要に応じて次のように整形し、行ごとの主要情報を表示できます。

```bash
./csv-search search --query "漂白" | jq -r ".[] | \"受付No: \(.fields[\"受付No\"]) / 得意先名: \(.fields[\"得意先名\"]) / 実行内容: \(.fields[\"実行内容\"])\""
```

これにより、「受付No」「得意先名」「実行内容」を同一行で確認しながら検索結果を活用できます。
