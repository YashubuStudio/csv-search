# 📝 設計書（概要設計）

## 目的

* ユーザーが提供するCSVファイルを取り込み、内部DBに格納する。
* CSVの差分（追加・更新）を自動的に検出して同期する。
* データに位置情報（緯度経度）とテキスト情報を持たせ、\*\*意味検索（ベクトル検索）**と**位置フィルタ（距離検索）\*\*を組み合わせて検索できる。
* 単一実行ファイルとしてパッケージ化可能（Windows / Linux / macOS）。

---

## 全体構成

```
+----------------+
| CSVファイル     |
+--------+-------+
         |
         v
+----------------+
| CSVインポータ   |
| - 差分検出     |
| - 座標正規化   |
| - 埋め込み生成 |
+--------+-------+
         |
         v
+--------------------------+
| SQLite DB                |
| - items (メイン情報)     |
| - items_rtree (R*Tree)   |
| - items_vec (ベクトル)   |
| - items_fts (全文検索)   |
+--------+-------+---------+
         |
         v
+---------------------------+
| 検索API / CLI / WebUI      |
| - クエリ→埋め込み生成     |
| - R*Tree距離フィルタ       |
| - ベクトル近傍検索         |
| - スコア統合・再ランク     |
+---------------------------+
```

---

## 主な機能

### CSV取り込み（差分対応）

* CSV列マッピング（`--id-col`, `--title-col`, `--body-col`, `--lat-col`, `--lng-col`, `--geocode-col`など）
* 行ごとにハッシュを生成（`id+title+body+lat+lng`など）し、既存レコードと比較
* 差分のみ更新・再埋め込み
* 住所→緯度経度変換（オフラインは事前座標列を推奨）

### 検索

* クエリ文字列 → 埋め込み（ONNXローカル推論）
* R\*Treeで距離フィルタ（例：半径3km）
* 絞った候補に対してベクトル近傍検索（Cos類似）
* スコア計算：
  `score = α*ベクトル類似 - β*距離 + γ*BM25`
* 上位N件を返却（id, title, body, lat, lng, score, distance）

### 配布

* Goで実装 → `go build` → 単体バイナリ
* Linuxは `appimagetool` で AppImage化
* モデル（.onnx）と設定ファイルを同梱

---

# 🗄 基礎データ定義（DDL）

```sql
-- メインデータ
CREATE TABLE IF NOT EXISTS items (
  id TEXT PRIMARY KEY,
  title TEXT,
  body TEXT,
  lat REAL,
  lng REAL,
  hash TEXT,         -- 行ハッシュ（差分検出用）
  updated_at INTEGER -- 取込UNIX時刻
);

-- 位置検索用（R*Tree）
CREATE VIRTUAL TABLE IF NOT EXISTS items_rtree
USING rtree(
  id,
  minLat, maxLat,
  minLng, maxLng
);

-- ベクトル検索用（sqlite-vec拡張仮定）
CREATE VIRTUAL TABLE IF NOT EXISTS items_vec
USING vec0(
  id TEXT PRIMARY KEY,
  embedding BLOB
);

-- 任意：全文検索（BM25）
CREATE VIRTUAL TABLE IF NOT EXISTS items_fts
USING fts5(title, body, content='items', content_rowid='rowid');
```

---

# 📦 ディレクトリ構成（例）

```
myapp/
├── main.go
├── db/
│   └── app.db
├── models/
│   └── encoder.onnx
├── static/          # WebUI用
├── ingest/          # CSV取り込み処理
└── search/          # 検索API/CLI処理
```

---

# ⚙️ コマンド例

```bash
# DB初期化
myapp init --db ./db/app.db

# CSVインポート
myapp ingest --csv ./data.csv \
  --id-col id --title-col name --body-col desc \
  --lat-col lat --lng-col lng --batch 1000

# 検索（CLI）
myapp search --q "渋谷 カフェ" --lat 35.66 --lng 139.70 --km 3 --topk 20 --json

# REST API起動
myapp serve --bind :8787
```

---

# 📈 運用ポイント

* 10〜100万件規模ならSQLiteで十分
* 差分取り込みはハッシュ管理で高速
* 埋め込みモデルはONNX（bge-m3 / multilingual-e5 など）を同梱
* 将来的にQdrant/Milvusへ差し替えも可能（API層を抽象化）

---