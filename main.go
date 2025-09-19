package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"yashubustudio/csv-search/emb"
	"yashubustudio/csv-search/internal/database"
	"yashubustudio/csv-search/internal/ingest"
	"yashubustudio/csv-search/internal/search"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	ctx := context.Background()
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "init":
		err = runInit(ctx, args)
	case "ingest":
		err = runIngest(ctx, args)
	case "search":
		err = runSearch(ctx, args)
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		usage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runInit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	dbPath := fs.String("db", "data/app.db", "path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return err
	}

	db, err := database.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := database.Init(ctx, db); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "database initialized at %s\n", *dbPath)
	return nil
}

func runIngest(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	dbPath := fs.String("db", "data/app.db", "path to SQLite database")
	csvPath := fs.String("csv", "", "path to source CSV file")
	batchSize := fs.Int("batch", 1000, "rows per transaction batch")
	ortLib := fs.String("ort-lib", "", "path to ONNX Runtime shared library")
	modelPath := fs.String("model", "", "path to encoder ONNX model")
	tokenizerPath := fs.String("tokenizer", "", "path to tokenizer.json")
	maxSeqLen := fs.Int("max-seq-len", 512, "maximum sequence length for the encoder")

	tableName := fs.String("table", "default", "logical table/dataset name to store the records")
	idCol := fs.String("id-col", "id", "CSV column containing the primary identifier")
	textColsFlag := fs.String("text-cols", "", "comma-separated CSV columns used for embeddings (defaults to metadata columns)")
	metaColsFlag := fs.String("meta-cols", "*", "comma-separated CSV columns to persist as metadata; use '*' to keep all")
	latCol := fs.String("lat-col", "", "CSV column for latitude (empty to disable)")
	lngCol := fs.String("lng-col", "", "CSV column for longitude (empty to disable)")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *csvPath == "" {
		return fmt.Errorf("csv path is required")
	}
	if *ortLib == "" {
		return fmt.Errorf("ort-lib is required")
	}
	if *modelPath == "" {
		return fmt.Errorf("model is required")
	}
	if *tokenizerPath == "" {
		return fmt.Errorf("tokenizer is required")
	}

	db, err := database.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := database.Init(ctx, db); err != nil {
		return err
	}

	enc := &emb.Encoder{}
	cfg := emb.Config{
		OrtDLL:        *ortLib,
		ModelPath:     *modelPath,
		TokenizerPath: *tokenizerPath,
		MaxSeqLen:     *maxSeqLen,
	}
	if err := enc.Init(cfg); err != nil {
		return err
	}
	defer enc.Close()

	options := ingest.Options{
		CSVPath:   *csvPath,
		BatchSize: *batchSize,
		Dataset:   *tableName,
		Columns: ingest.ColumnConfig{
			ID:       *idCol,
			Text:     parseCSVList(*textColsFlag),
			Metadata: parseCSVList(*metaColsFlag),
			Lat:      *latCol,
			Lng:      *lngCol,
		},
	}

	if err := ingest.Run(ctx, db, enc, options); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "ingested data from %s\n", *csvPath)
	return nil
}

func runSearch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	dbPath := fs.String("db", "data/app.db", "path to SQLite database")
	query := fs.String("query", "", "text query for semantic vector search")
	topK := fs.Int("topk", 10, "number of results to return")
	ortLib := fs.String("ort-lib", "", "path to ONNX Runtime shared library")
	modelPath := fs.String("model", "", "path to encoder ONNX model")
	tokenizerPath := fs.String("tokenizer", "", "path to tokenizer.json")
	maxSeqLen := fs.Int("max-seq-len", 512, "maximum sequence length for the encoder")
	tableName := fs.String("table", "default", "logical table/dataset to search")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *query == "" {
		return fmt.Errorf("query is required")
	}
	if *ortLib == "" {
		return fmt.Errorf("ort-lib is required")
	}
	if *modelPath == "" {
		return fmt.Errorf("model is required")
	}
	if *tokenizerPath == "" {
		return fmt.Errorf("tokenizer is required")
	}

	db, err := database.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	enc := &emb.Encoder{}
	cfg := emb.Config{
		OrtDLL:        *ortLib,
		ModelPath:     *modelPath,
		TokenizerPath: *tokenizerPath,
		MaxSeqLen:     *maxSeqLen,
	}
	if err := enc.Init(cfg); err != nil {
		return err
	}
	defer enc.Close()

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	results, err := search.VectorSearch(ctx, db, enc, *tableName, *query, *topK)
	if err != nil {
		return err
	}
	encJSON := json.NewEncoder(os.Stdout)
	encJSON.SetIndent("", "  ")
	return encJSON.Encode(results)
}

func parseCSVList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var result []string
	for _, part := range strings.Split(value, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func usage() {
	exe := filepath.Base(os.Args[0])
	fmt.Fprintf(os.Stderr, `Usage: %s <command> [options]

Commands:
  init      Initialize the SQLite database schema
  ingest    Ingest CSV data and generate embeddings
  search    Perform a semantic vector search

Use "%s <command> -h" to see command-specific options.
`, exe, exe)
}
