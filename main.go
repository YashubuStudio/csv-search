package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
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

	idCol := fs.String("id-col", "id", "CSV column containing the primary identifier")
	titleCol := fs.String("title-col", "title", "CSV column for the title (empty to disable)")
	bodyCol := fs.String("body-col", "body", "CSV column for the body (empty to disable)")
	tagsCol := fs.String("tags-col", "tags", "CSV column for tags (empty to disable)")
	categoryCol := fs.String("category-col", "category", "CSV column for category (empty to disable)")
	priceCol := fs.String("price-col", "price", "CSV column for price (empty to disable)")
	stockCol := fs.String("stock-col", "stock", "CSV column for stock (empty to disable)")
	createdCol := fs.String("created-at-col", "created_at", "CSV column for created_at (empty to disable)")
	updatedCol := fs.String("updated-at-col", "updated_at", "CSV column for updated_at (empty to disable)")
	latCol := fs.String("lat-col", "lat", "CSV column for latitude (empty to disable)")
	lngCol := fs.String("lng-col", "lng", "CSV column for longitude (empty to disable)")

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
		Columns: ingest.ColumnMapping{
			ID:        *idCol,
			Title:     *titleCol,
			Body:      *bodyCol,
			Tags:      *tagsCol,
			Category:  *categoryCol,
			Price:     *priceCol,
			Stock:     *stockCol,
			CreatedAt: *createdCol,
			UpdatedAt: *updatedCol,
			Lat:       *latCol,
			Lng:       *lngCol,
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

	results, err := search.VectorSearch(ctx, db, enc, *query, *topK)
	if err != nil {
		return err
	}
	encJSON := json.NewEncoder(os.Stdout)
	encJSON.SetIndent("", "  ")
	return encJSON.Encode(results)
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
