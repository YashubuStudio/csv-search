# 📘 プロジェクト企画仕様書

**プロジェクト名（仮称）**：SemanticGeoSearchDB
**目的**：CSVデータを取り込み、差分同期しつつローカル内蔵DBに格納。位置情報とテキスト情報を複数列で意味検索・絞り込み・並び替えできる検索アプリケーションを単体パッケージとして提供する。

---

## 1. 背景と目的

### 背景

* データベース運用経験がない利用者でも、CSVファイルを渡すだけで検索可能な環境が欲しい。
* 一度取り込んだデータを差分更新でき、かつ位置情報・テキスト情報を活用した高度検索をローカルで完結させたい。
* ネット接続やクラウドDB依存を避け、クラッシュしてもDBを再構築できる仕組みが求められる。
* アプリケーションは**単一パッケージ**（exe, AppImageなど）として配布しやすい構成が必須。

### 目的

* CSV → 内部DB化（差分同期）
* 緯度経度（または住所）を用いた位置検索
* 複数列を対象にしたベクトル意味検索＋全文検索
* 絞り込み（カテゴリ、価格帯など）と任意ソート
* CLIとREST APIとWeb UIで操作可能
* 単体実行形式（クロスプラットフォーム）で配布

---

## 2. システム構成

### 全体構造

```
[CSVファイル] → [CSVインポータ]
         ↓             │
         ↓        ┌────┴─────────────┐
         ↓        │ 差分検出・正規化 │
         ↓        │ 住所→座標変換     │
         ↓        │ 埋め込み生成(ONNX)│
         ↓        └────┬─────────────┘
                     ↓
 ┌──────────────────────────────────────────┐
 │               SQLite DB                  │
 │ ┌─────────────┐ ┌─────────────────────┐ │
 │ │ items        │ 基本データ             │ │
 │ │ items_rtree  │ R*Tree(位置)           │ │
 │ │ items_vec(*) │ ベクトル(ANN)          │ │
 │ │ items_fts    │ 全文検索(BM25)          │ │
 │ └─────────────┘ └─────────────────────┘ │
 └──────────────────────────────────────────┘
                     ↓
            [検索API / CLI / WebUI]
```

---

## 3. 技術スタック

| 項目     | 技術                           | 備考                          |
| ------ | ---------------------------- | --------------------------- |
| 言語     | Go                           | 単一バイナリ化・クロスコンパイルが容易         |
| DB     | SQLite                       | 内蔵DB・依存不要                   |
| 位置検索   | R\*Tree（SQLite組込）            | 緯度経度を矩形で格納                  |
| ベクトル検索 | sqlite-vec / sqlite-vss      | Cosine/L2類似によるANN           |
| 全文検索   | SQLite FTS5                  | BM25スコア対応                   |
| 埋め込み生成 | ONNX Runtime (Go binding)    | bge-m3 / multilingual-e5 推奨 |
| UI     | CLI / REST API / Web UI(SPA) | オプション                       |
| 配布     | go build / AppImage / .exe   | モデルとDB同梱                    |

---

## 4. データモデル

### メインテーブル

```sql
CREATE TABLE IF NOT EXISTS items (
  id TEXT PRIMARY KEY,
  title TEXT,
  body TEXT,
  tags TEXT,            -- カンマ区切り or JSON
  category TEXT,
  price REAL,
  stock INTEGER,
  created_at INTEGER,
  updated_at INTEGER,
  lat REAL,
  lng REAL,
  hash TEXT              -- 行ハッシュ（差分検出用）
);
```

### インデックス・補助構造

```sql
CREATE VIRTUAL TABLE IF NOT EXISTS items_rtree
USING rtree(id, minLat, maxLat, minLng, maxLng);

CREATE VIRTUAL TABLE IF NOT EXISTS items_vec
USING vec0(id TEXT PRIMARY KEY, embedding BLOB);

CREATE VIRTUAL TABLE IF NOT EXISTS items_fts
USING fts5(
  title, body, tags,
  content='items', content_rowid='rowid'
);

CREATE INDEX IF NOT EXISTS idx_items_category ON items(category);
CREATE INDEX IF NOT EXISTS idx_items_price ON items(price);
CREATE INDEX IF NOT EXISTS idx_items_stock ON items(stock);
CREATE INDEX IF NOT EXISTS idx_items_created_at ON items(created_at);
```

---

## 5. データ投入（CSVインポート）

* **差分判定**：

  * 行ハッシュ（id+title+body+lat+lngなど連結してSHA256）を生成
  * DB側の`hash`と比較し、異なれば更新・再埋め込み
* **住所→座標**（任意）：

  * 住所列を指定した場合はジオコーディング（オンライン or 事前変換済み推奨）
* **取り込み処理**：

  1. CSV読み込み（列マッピング）
  2. 正規化（空白・全半角・小文字統一）
  3. ハッシュ計算→差分抽出
  4. DBにUpsert（items, items\_rtree, items\_fts, items\_vec）
  5. トランザクション分割（1000行ごと）

---

## 6. 検索機能

### 検索対象列

* テキスト列：title, body, tags（全文検索＋ベクトル）
* 数値列：price, stock（範囲絞り込み）
* カテゴリ列：category（eq/in絞り込み）
* 日付列：created\_at, updated\_at（gte/lte/between）
* 位置：lat, lng（距離検索）

### 検索パラメータ例

| パラメータ                  | 意味                              |
| ---------------------- | ------------------------------- |
| q                      | フリーテキスト検索語                      |
| q\_fields              | 対象列（例：`title,body,tags`）        |
| lat,lng,radius\_km     | 距離フィルタ                          |
| filters (JSON)         | 絞り込み条件（eq/in/between/gte/lteなど） |
| sort                   | 並び順（例：`-score,price`）           |
| topk                   | 最大返却件数                          |
| w\_vec,w\_bm25,w\_dist | スコア重み                           |

### スコア式（統合評価）

```
score = w_vec * vec_sim
      + w_bm25 * bm25
      - w_dist * distance_km
      + Σ w_col * norm(col)
```

---

## 7. CLI/REST API仕様

### CLI

```bash
myapp init --db ./db/app.db

myapp ingest --csv ./data.csv \
  --id-col id --title-col name --body-col desc \
  --lat-col lat --lng-col lng --batch 1000

myapp search \
  --q "Wi-Fi カフェ" \
  --q-fields title,tags \
  --lat 35.66 --lng 139.70 --km 2 \
  --filter 'category:eq:cafe' \
  --filter 'price:between:400,1200' \
  --sort '-score,price' \
  --topk 50
```

### REST

```
GET /search?q=Wi-Fi%20カフェ
  &q_fields=title,tags
  &lat=35.66&lng=139.70&radius_km=2
  &filters={"category":{"eq":"cafe"},"price":{"between":[400,1200]}}
  &sort=-score,price
  &topk=50
```

---

## 8. ディレクトリ構成

```
myapp/
├── main.go
├── db/
│   └── app.db
├── ingest/            # CSV取り込みロジック
├── search/             # 検索処理・スコア統合
├── models/
│   └── encoder.onnx    # 埋め込みモデル
├── static/              # Web UI (任意)
└── config.yml           # 列定義と検索設定
```

---

## 9. config.yml（列定義例）

```yaml
schema:
  id: {type: string, key: true}
  title: {type: text, search: [fts, vector], weight: {fts: 1.0, vec: 0.6}}
  body:  {type: text, search: [fts, vector], weight: {fts: 0.7, vec: 0.3}}
  tags:  {type: text, search: [fts, vector], weight: {fts: 0.3, vec: 0.1}, split: ","}
  category: {type: enum, filter: [eq,in], index: true}
  price:    {type: number, filter: [between,gte,lte], sort: true, index: true}
  stock:    {type: integer, filter: [gte,lte], sort: true, index: true}
  created_at: {type: datetime, filter: [between,gte,lte], sort: true, index: true}
  lat: {type: number}
  lng: {type: number}

search:
  vector_mode: unified
  weights:
    vec: 0.6
    bm25: 0.3
    dist: 0.1
  candidate_sizes:
    ftsN: 2000
    annK: 400
```

---

## 10. 配布仕様

* **Windows**：`go build -o myapp.exe`
* **Linux**：`go build -o myapp && appimagetool ./` → `myapp.AppImage`
* **macOS**：`go build -o myapp`（.dmg梱包も可）
* 同梱ファイル：`myapp`実行ファイル、`models/encoder.onnx`、`config.yml`、（任意で）`static/`UI

---

## 11. 拡張計画（将来）

* 埋め込み列を分離（title/body/tags別ベクトル）して重み合成 → 精度向上
* Web UI（React/Svelte）でGUI検索・フィルタ
* Qdrant/Milvus対応（1000万件超スケール用）
* インポート監視（CSVフォルダ監視）機能
* 再ランクに学習モデルを導入（クリックログ活用）

---

## 12. 引き継ぎに必要な最小情報

* 本仕様書（この文書）
* config.yml（列定義・重み設定）
* models/encoder.onnx（埋め込みモデルファイル）
* ソースコード（Go）
* 初期CSVまたはデータバックアップ
* SQLite `app.db` ファイル（あれば）

---

## ✅ 開発の優先実装順序（推奨）

1. DB初期化・CLIコマンド雛形
2. CSV取り込み（差分更新）
3. 埋め込み生成（ONNX）と保存
4. R\*Tree・FTS5・vecインデックス作成
5. 検索パイプライン（FTS→候補→ベクトル→再ランク）
6. 絞り込みとソート機能
7. REST API化
8. Web UI（必要なら）
