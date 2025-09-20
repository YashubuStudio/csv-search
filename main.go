package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"yashubustudio/csv-search/pkg/csvsearch"
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
	case "serve":
		err = runServe(ctx, args)
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
	configFlag := fs.String("config", "", "path to configuration file (default: csv-search_config.json if present)")
	dbPath := fs.String("db", "", "path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return err
	}

	svc, err := csvsearch.NewService(csvsearch.ServiceOptions{
		Config:   csvsearch.ConfigReference{Path: *configFlag, Required: flagWasProvided(fs, "config")},
		Database: csvsearch.DatabaseOptions{Path: *dbPath},
	})
	if err != nil {
		return err
	}
	defer svc.Close()

	if err := svc.InitDatabase(ctx, csvsearch.InitDatabaseOptions{}); err != nil {
		return err
	}

	path := svc.DatabasePath()
	if strings.TrimSpace(path) == "" {
		path = "(in-memory or external connection)"
	}
	fmt.Fprintf(os.Stdout, "database initialized at %s\n", path)
	return nil
}

func runIngest(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	configFlag := fs.String("config", "", "path to configuration file (default: csv-search_config.json if present)")
	dbPath := fs.String("db", "", "path to SQLite database")
	csvPath := fs.String("csv", "", "path to source CSV file")
	batchSize := fs.Int("batch", -1, "rows per transaction batch")
	ortLib := fs.String("ort-lib", "", "path to ONNX Runtime shared library")
	modelPath := fs.String("model", "", "path to encoder ONNX model")
	tokenizerPath := fs.String("tokenizer", "", "path to tokenizer.json")
	maxSeqLen := fs.Int("max-seq-len", -1, "maximum sequence length for the encoder")

	tableName := fs.String("table", "", "logical table/dataset name to store the records")
	idCol := fs.String("id-col", "", "CSV column containing the primary identifier")
	textColsFlag := fs.String("text-cols", "", "comma-separated CSV columns used for embeddings (defaults to metadata columns)")
	metaColsFlag := fs.String("meta-cols", "", "comma-separated CSV columns to persist as metadata; use '*' to keep all")
	latCol := fs.String("lat-col", "", "CSV column for latitude (empty to disable)")
	lngCol := fs.String("lng-col", "", "CSV column for longitude (empty to disable)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	svc, err := csvsearch.NewService(csvsearch.ServiceOptions{
		Config:   csvsearch.ConfigReference{Path: *configFlag, Required: flagWasProvided(fs, "config")},
		Database: csvsearch.DatabaseOptions{Path: *dbPath},
		Encoder: csvsearch.EncoderOptions{
			Config: csvsearch.EncoderConfig{
				OrtLibrary:        *ortLib,
				ModelPath:         *modelPath,
				TokenizerPath:     *tokenizerPath,
				MaxSequenceLength: *maxSeqLen,
			},
		},
	})
	if err != nil {
		return err
	}
	defer svc.Close()

	textCols := parseCSVList(*textColsFlag)
	metaCols := parseCSVList(*metaColsFlag)

	summary, err := svc.Ingest(ctx, csvsearch.IngestOptions{
		Dataset:         strings.TrimSpace(*tableName),
		CSVPath:         strings.TrimSpace(*csvPath),
		BatchSize:       *batchSize,
		IDColumn:        strings.TrimSpace(*idCol),
		TextColumns:     textCols,
		MetadataColumns: metaCols,
		LatitudeColumn:  strings.TrimSpace(*latCol),
		LongitudeColumn: strings.TrimSpace(*lngCol),
	})
	if err != nil {
		return err
	}

	datasetLabel := strings.TrimSpace(summary.Dataset)
	if datasetLabel == "" {
		datasetLabel = summary.Table
	}
	if datasetLabel == "" {
		datasetLabel = "default"
	}
	fmt.Fprintf(os.Stdout, "ingested dataset %s from %s\n", datasetLabel, summary.CSVPath)
	return nil
}

func runSearch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	configFlag := fs.String("config", "", "path to configuration file (default: csv-search_config.json if present)")
	dbPath := fs.String("db", "", "path to SQLite database")
	query := fs.String("query", "", "text query for semantic vector search")
	topK := fs.Int("topk", -1, "number of results to return")
	ortLib := fs.String("ort-lib", "", "path to ONNX Runtime shared library")
	modelPath := fs.String("model", "", "path to encoder ONNX model")
	tokenizerPath := fs.String("tokenizer", "", "path to tokenizer.json")
	maxSeqLen := fs.Int("max-seq-len", -1, "maximum sequence length for the encoder")
	tableName := fs.String("table", "", "logical table/dataset to search")
	var filterArgs filterFlag
	fs.Var(&filterArgs, "filter", "metadata filter in the form field=value (repeatable)")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*query) == "" {
		return fmt.Errorf("query is required")
	}

	svc, err := csvsearch.NewService(csvsearch.ServiceOptions{
		Config:   csvsearch.ConfigReference{Path: *configFlag, Required: flagWasProvided(fs, "config")},
		Database: csvsearch.DatabaseOptions{Path: *dbPath},
		Encoder: csvsearch.EncoderOptions{
			Config: csvsearch.EncoderConfig{
				OrtLibrary:        *ortLib,
				ModelPath:         *modelPath,
				TokenizerPath:     *tokenizerPath,
				MaxSequenceLength: *maxSeqLen,
			},
		},
	})
	if err != nil {
		return err
	}
	defer svc.Close()

	searchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	results, err := svc.Search(searchCtx, csvsearch.SearchOptions{
		Query:   strings.TrimSpace(*query),
		Dataset: strings.TrimSpace(*tableName),
		TopK:    *topK,
		Filters: []csvsearch.Filter(filterArgs),
	})
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(results)
}

func runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configFlag := fs.String("config", "", "path to configuration file (default: csv-search_config.json if present)")
	dbPath := fs.String("db", "", "path to SQLite database")
	addr := fs.String("addr", ":8080", "address for the HTTP server (host:port)")
	tableName := fs.String("table", "", "default dataset to search")
	topK := fs.Int("topk", -1, "default number of results to return")
	ortLib := fs.String("ort-lib", "", "path to ONNX Runtime shared library")
	modelPath := fs.String("model", "", "path to encoder ONNX model")
	tokenizerPath := fs.String("tokenizer", "", "path to tokenizer.json")
	maxSeqLen := fs.Int("max-seq-len", -1, "maximum sequence length for the encoder")
	requestTimeout := fs.Duration("request-timeout", 30*time.Second, "maximum duration for each search request")
	shutdownTimeout := fs.Duration("shutdown-timeout", 5*time.Second, "graceful shutdown timeout")

	if err := fs.Parse(args); err != nil {
		return err
	}

	svc, err := csvsearch.NewService(csvsearch.ServiceOptions{
		Config:   csvsearch.ConfigReference{Path: *configFlag, Required: flagWasProvided(fs, "config")},
		Database: csvsearch.DatabaseOptions{Path: *dbPath},
		Encoder: csvsearch.EncoderOptions{
			Config: csvsearch.EncoderConfig{
				OrtLibrary:        *ortLib,
				ModelPath:         *modelPath,
				TokenizerPath:     *tokenizerPath,
				MaxSequenceLength: *maxSeqLen,
			},
		},
	})
	if err != nil {
		return err
	}
	defer svc.Close()

	serveCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	return svc.StartServer(serveCtx, csvsearch.ServeOptions{
		Address:         *addr,
		Dataset:         strings.TrimSpace(*tableName),
		TopK:            *topK,
		RequestTimeout:  *requestTimeout,
		ShutdownTimeout: *shutdownTimeout,
	})
}

func usage() {
	exe := filepath.Base(os.Args[0])
	fmt.Fprintf(os.Stderr, `Usage: %s <command> [options]

Commands:
  init      Initialize the SQLite database schema
  ingest    Ingest CSV data and generate embeddings
  search    Perform a semantic vector search
  serve     Start the long-running HTTP search server

Use "%s <command> -h" to see command-specific options.
`, exe, exe)
}

func flagWasProvided(fs *flag.FlagSet, name string) bool {
	provided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			provided = true
		}
	})
	return provided
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

type filterFlag []csvsearch.Filter

func (f *filterFlag) String() string {
	if f == nil || len(*f) == 0 {
		return ""
	}
	parts := make([]string, 0, len(*f))
	for _, filter := range *f {
		parts = append(parts, fmt.Sprintf("%s=%s", filter.Field, filter.Value))
	}
	return strings.Join(parts, ",")
}

func (f *filterFlag) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("filter must be in the form field=value")
	}
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("filter must be in the form field=value")
	}
	field := strings.TrimSpace(parts[0])
	val := strings.TrimSpace(parts[1])
	if field == "" {
		return fmt.Errorf("filter field must not be empty")
	}
	*f = append(*f, csvsearch.Filter{Field: field, Value: val})
	return nil
}
