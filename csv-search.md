# csv-search Intelligent Execution Guide

## Purpose
- Provide a single, AI-friendly brief that contains **every** prerequisite to run and extend the csv-search application.
- Assume no other documentation is available: follow the steps below to build the CLI, ingest data, run semantic search, expose the HTTP API, or embed the library inside another Go program.

## Repository Snapshot
| Path | Role |
|------|------|
| `main.go` | CLI entry point exposing `init`, `ingest`, `search`, and `serve` subcommands. |
| `pkg/csvsearch/` | Public Go package with `Service` utilities (`InitDatabase`, `Ingest`, `Search`, `StartServer`, `NewAPIServer`). |
| `internal/config/` | JSON configuration loader and helpers that resolve relative paths. |
| `internal/database/` | SQLite opener and schema bootstrap (`records`, `records_vec`, `records_fts`, `records_rtree`). |
| `internal/ingest/` | CSV parser, embedding pipeline, differential upsert logic. |
| `internal/search/` | Vector search implementation using cosine similarity and metadata filters. |
| `internal/server/` | HTTP server exposing `/search` and `/healthz`. |
| `internal/vector/` | Helpers for vector serialization and cosine scoring. |
| `emb/` | ONNX encoder wrapper (`emb.Encoder`). **Do not modify** unless you are updating ONNX handling. |
| `csv/`, `models/`, `onnixruntime-win/` | Sample CSV dataset, encoder assets, and Windows ONNX Runtime binary shipped with the repo. |

## Runtime Prerequisites & Assets
1. **Go toolchain**: Go 1.24 or newer (module path: `yashubustudio/csv-search`).
2. **SQLite driver**: Included via `modernc.org/sqlite`; no CGO required.
3. **ONNX Runtime shared library**: Point `embedding.ort_lib` (or `--ort-lib`) to a `.dll/.so/.dylib`. The repo bundles `./onnixruntime-win/lib/onnxruntime.dll` for Windows builds.
4. **Encoder model + tokenizer**: e.g., `./models/bge-m3/model.onnx`, optional `model.onnx_data`, and `./models/bge-m3/tokenizer.json`.
5. **Configuration** (default `csv-search_config.json`) describing the database, encoder, and dataset metadata.
6. **CSV source data**: e.g., `./csv/image.csv` with at least an ID column plus one text column.

> 📦 Keep the binary, ONNX runtime, model, tokenizer, configuration, and CSV file in reachable paths. The configuration loader resolves relative paths against the config file’s directory.

## Configuration Contract (`csv-search_config.json`)
```json
{
  "database": { "path": "./data/image.db" },
  "embedding": {
    "ort_lib": "./onnixruntime-win/lib/onnxruntime.dll",
    "model": "./models/bge-m3/model.onnx",
    "tokenizer": "./models/bge-m3/tokenizer.json",
    "max_seq_len": 512
  },
  "default_dataset": "textile_jobs",
  "datasets": {
    "textile_jobs": {
      "table": "textile_jobs",
      "csv": "./csv/image.csv",
      "batch_size": 1000,
      "id_column": "受付No",
      "text_columns": ["実行内容"],
      "meta_columns": ["*"],
      "lat_column": "",
      "lng_column": ""
    }
  },
  "search": { "default_topk": 5 }
}
```
- `database.path`: SQLite file path (defaults to `data/app.db` when omitted).
- `embedding`: Shared library, ONNX model, tokenizer, and maximum sequence length used by `emb.Encoder`.
- `default_dataset`: Dataset name automatically used by CLI/API when `--table`/`dataset` is omitted.
- `datasets.<name>`: Defaults for ingestion (CSV path, identifier column, text columns to embed, metadata to store, optional lat/lng columns, batch size).
- `search.default_topk`: Fallback result size for `search` and API calls.

## End-to-End Workflow
1. **Build the CLI**
   ```bash
   go build -o csv-search .
   ```
2. **Initialize the database schema**
   ```bash
   ./csv-search init --config ./csv-search_config.json
   ```
   - Creates directories as needed and applies the schema (`records`, `records_vec`, `records_fts`, `records_rtree`).
3. **Ingest CSV data and generate embeddings**
   ```bash
   ./csv-search ingest --config ./csv-search_config.json
   ```
   - Reads `datasets.<default_dataset>` if flags such as `--csv`, `--id-col`, etc. are omitted.
   - Generates embeddings through ONNX, stores metadata JSON, populates FTS and R-Tree tables, and de-duplicates rows using a hash of ID + content + metadata.
4. **Run a semantic search from the CLI**
   ```bash
   ./csv-search search --config ./csv-search_config.json --query "Wi-Fi カフェ" --topk 10
   ```
   - Outputs JSON array with `dataset`, `id`, `fields` (metadata map), `score`, and optional `lat`/`lng`.
   - Add `--filter "列名=値"` repeatedly for AND filters; `--table` overrides the dataset.
5. **Expose an HTTP API**
   ```bash
   ./csv-search serve --config ./csv-search_config.json --addr :8080
   ```
   - Auto-ingests configured datasets before serving (unless `ServeOptions.AutoIngest` is set to `false` via the Go API).
   - Gracefully shuts down on SIGINT/SIGTERM respecting `--shutdown-timeout`.

## CLI Reference
| Command | Key Flags | Behaviour |
|---------|-----------|-----------|
| `init` | `--config`, `--db` | Loads configuration (if present) and initializes SQLite schema. |
| `ingest` | `--config`, `--db`, `--csv`, `--table`, `--id-col`, `--text-cols`, `--meta-cols`, `--lat-col`, `--lng-col`, `--ort-lib`, `--model`, `--tokenizer`, `--max-seq-len`, `--batch` | Imports CSV, generates embeddings, updates `records`, `records_vec`, `records_fts`, and `records_rtree`. |
| `search` | `--config`, `--db`, `--query`, `--table`, `--topk`, `--filter`, encoder flags | Encodes the query via ONNX and prints ranked JSON results. |
| `serve` | `--config`, `--db`, `--addr`, `--table`, `--topk`, `--request-timeout`, `--shutdown-timeout`, encoder flags | Starts HTTP server with `/search` and `/healthz`. |

> All encoder-related flags override configuration values. Provide `--ort-lib`, `--model`, and `--tokenizer` when the config file does not exist.

## HTTP API Contract
- **`GET /search`**: Query parameters `q`/`query`, `dataset`/`table`, `topk`, and repeated `filter=field=value`.
- **`POST /search`**: JSON body `{"query": "text", "dataset": "name", "topk": 5, "filters": {"列": "値"}}`. Arrays under `filter` are also accepted.
- **`POST /query`**: Alias of `/search` that also accepts `max_results` instead of `topk` and tolerates an optional `summary_only` flag for compatibility with external tools.
- **`GET /healthz`**: Returns `200 OK` with body `ok`.
- Responses mirror CLI search results. Timeout defaults to 30 s; exceeding it returns HTTP 504.

## Embedding the Library in Go Code
```go
svc, err := csvsearch.NewService(csvsearch.ServiceOptions{
    Config:   csvsearch.ConfigReference{Path: "./csv-search_config.json"},
    Database: csvsearch.DatabaseOptions{Path: ""},
    Encoder:  csvsearch.EncoderOptions{Config: csvsearch.EncoderConfig{}},
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
- `DatabaseOptions.Handle` lets you supply an existing `*sql.DB`.
- `EncoderOptions.Instance` accepts a pre-initialized `*emb.Encoder`; otherwise the encoder is lazily built from config.
- `Service.StartServer` wraps auto-ingest plus HTTP serving; use `Service.NewAPIServer` to mount the handler on a custom mux.

## Data Pipeline Details
- **CSV Parsing**: Header row is mandatory. `MetadataColumns` accepts `"*"` to store all columns; duplicates are deduplicated automatically.
- **Lat/Lng Handling**: Optional numeric columns populate `records.lat`/`records.lng` and the `records_rtree` spatial index.
- **Change Detection**: Each row produces a SHA-256 hash of dataset, ID, text content, metadata, and coordinates to skip unchanged rows on re-ingest.
- **Vector Storage**: Embeddings are serialized as little-endian float32 blobs in `records_vec`. Empty texts remove the vector entry but keep metadata.
- **Full-Text Search**: Text columns populate `records_fts`, enabling hybrid search strategies if required.

## Troubleshooting Checklist
- `encoder configuration is incomplete`: ensure `OrtDLL`, `ModelPath`, and `TokenizerPath` are reachable (absolute or relative to config file).
- `filter must be in the form field=value`: sanitize `--filter` arguments or JSON payloads.
- `id column is empty`: verify the CSV has unique IDs defined in `id_column`.
- Cross-compiling for Windows: set `GOOS=windows` and keep the ONNX Runtime DLL alongside the executable or update config paths accordingly.

With this guide alone, an automated agent can build the binary, ingest data, run semantic queries, launch the HTTP API, or link against the Go package without consulting additional files.
