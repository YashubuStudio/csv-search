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

	"yashubustudio/csv-search/emb"
	"yashubustudio/csv-search/internal/config"
	"yashubustudio/csv-search/internal/database"
	"yashubustudio/csv-search/internal/ingest"
	"yashubustudio/csv-search/internal/search"
	"yashubustudio/csv-search/internal/server"
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
	configFlag := fs.String("config", "", "path to configuration file (default: config.json if present)")
	dbPath := fs.String("db", "", "path to SQLite database")
	if err := fs.Parse(args); err != nil {
		return err
	}

	appCfg, err := loadConfig(*configFlag, flagWasProvided(fs, "config"))
	if err != nil {
		return err
	}

	db := firstNonEmpty(*dbPath, configDatabasePath(appCfg), "data/app.db")
	if db == "" {
		return fmt.Errorf("db path is required")
	}

	dbHandle, err := database.Open(db)
	if err != nil {
		return err
	}
	defer dbHandle.Close()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := database.Init(ctx, dbHandle); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "database initialized at %s\n", db)
	return nil
}

func runIngest(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	configFlag := fs.String("config", "", "path to configuration file (default: config.json if present)")
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

	appCfg, err := loadConfig(*configFlag, flagWasProvided(fs, "config"))
	if err != nil {
		return err
	}

	datasetName := strings.TrimSpace(*tableName)
	if datasetName == "" && appCfg != nil && appCfg.DefaultDataset != "" {
		datasetName = appCfg.DefaultDataset
	}

	var dataset config.DatasetConfig
	hasDataset := false
	if appCfg != nil && datasetName != "" {
		if ds, ok := appCfg.Dataset(datasetName); ok {
			dataset = ds
			hasDataset = true
		}
	}

	table := firstNonEmpty(*tableName, dataset.Table, datasetName, "default")

	csv := *csvPath
	if csv == "" && hasDataset {
		csv = dataset.CSV
	}
	if appCfg != nil {
		csv = appCfg.ResolvePath(csv)
	}
	if csv == "" {
		return fmt.Errorf("csv path is required")
	}

	db := firstNonEmpty(*dbPath, configDatabasePath(appCfg), "data/app.db")
	if db == "" {
		return fmt.Errorf("db path is required")
	}

	batch := firstPositive(*batchSize, dataset.BatchSize, 1000)
	identifier := firstNonEmpty(*idCol, dataset.IDColumn, "id")

	textCols := parseCSVList(*textColsFlag)
	if len(textCols) == 0 && hasDataset && len(dataset.TextColumns) > 0 {
		textCols = cloneStrings(dataset.TextColumns)
	}

	metaCols := parseCSVList(*metaColsFlag)
	if len(metaCols) == 0 {
		if hasDataset && len(dataset.MetaColumns) > 0 {
			metaCols = cloneStrings(dataset.MetaColumns)
		} else {
			metaCols = []string{"*"}
		}
	}

	latitude := firstNonEmpty(*latCol, dataset.LatColumn)
	longitude := firstNonEmpty(*lngCol, dataset.LngColumn)

	ortDLL := *ortLib
	if ortDLL == "" && appCfg != nil {
		ortDLL = appCfg.ResolvePath(appCfg.Embedding.OrtLib)
	}
	if ortDLL == "" {
		return fmt.Errorf("ort-lib is required")
	}

	model := *modelPath
	if model == "" && appCfg != nil {
		model = appCfg.ResolvePath(appCfg.Embedding.Model)
	}
	if model == "" {
		return fmt.Errorf("model is required")
	}

	tokenizer := *tokenizerPath
	if tokenizer == "" && appCfg != nil {
		tokenizer = appCfg.ResolvePath(appCfg.Embedding.Tokenizer)
	}
	if tokenizer == "" {
		return fmt.Errorf("tokenizer is required")
	}

	seqLen := firstPositive(*maxSeqLen, cfgEmbeddingMaxSeqLen(appCfg), 512)

	dbHandle, err := database.Open(db)
	if err != nil {
		return err
	}
	defer dbHandle.Close()

	if err := database.Init(ctx, dbHandle); err != nil {
		return err
	}

	enc := &emb.Encoder{}
	encoderCfg := emb.Config{
		OrtDLL:        ortDLL,
		ModelPath:     model,
		TokenizerPath: tokenizer,
		MaxSeqLen:     seqLen,
	}
	if err := enc.Init(encoderCfg); err != nil {
		return err
	}
	defer enc.Close()

	options := ingest.Options{
		CSVPath:   csv,
		BatchSize: batch,
		Dataset:   table,
		Columns: ingest.ColumnConfig{
			ID:       identifier,
			Text:     textCols,
			Metadata: metaCols,
			Lat:      latitude,
			Lng:      longitude,
		},
	}

	if err := ingest.Run(ctx, dbHandle, enc, options); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "ingested data from %s\n", csv)
	return nil
}

func runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configFlag := fs.String("config", "", "path to configuration file (default: config.json if present)")
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

	appCfg, err := loadConfig(*configFlag, flagWasProvided(fs, "config"))
	if err != nil {
		return err
	}

	datasetName := strings.TrimSpace(*tableName)
	if datasetName == "" && appCfg != nil && appCfg.DefaultDataset != "" {
		datasetName = appCfg.DefaultDataset
	}

	var dataset config.DatasetConfig
	if appCfg != nil && datasetName != "" {
		if ds, ok := appCfg.Dataset(datasetName); ok {
			dataset = ds
		}
	}

	table := firstNonEmpty(*tableName, dataset.Table, datasetName, "default")

	db := firstNonEmpty(*dbPath, configDatabasePath(appCfg), "data/app.db")
	if db == "" {
		return fmt.Errorf("db path is required")
	}

	ortDLL := *ortLib
	if ortDLL == "" && appCfg != nil {
		ortDLL = appCfg.ResolvePath(appCfg.Embedding.OrtLib)
	}
	if ortDLL == "" {
		return fmt.Errorf("ort-lib is required")
	}

	model := *modelPath
	if model == "" && appCfg != nil {
		model = appCfg.ResolvePath(appCfg.Embedding.Model)
	}
	if model == "" {
		return fmt.Errorf("model is required")
	}

	tokenizer := *tokenizerPath
	if tokenizer == "" && appCfg != nil {
		tokenizer = appCfg.ResolvePath(appCfg.Embedding.Tokenizer)
	}
	if tokenizer == "" {
		return fmt.Errorf("tokenizer is required")
	}

	seqLen := firstPositive(*maxSeqLen, cfgEmbeddingMaxSeqLen(appCfg), 512)
	defaultTopK := firstPositive(*topK, cfgSearchTopK(appCfg), 10)
	if defaultTopK <= 0 {
		defaultTopK = 10
	}

	dbHandle, err := database.Open(db)
	if err != nil {
		return err
	}
	defer dbHandle.Close()

	initCtx, cancelInit := context.WithTimeout(ctx, 10*time.Second)
	if err := database.Init(initCtx, dbHandle); err != nil {
		cancelInit()
		return err
	}
	cancelInit()

	enc := &emb.Encoder{}
	encoderCfg := emb.Config{
		OrtDLL:        ortDLL,
		ModelPath:     model,
		TokenizerPath: tokenizer,
		MaxSeqLen:     seqLen,
	}
	if err := enc.Init(encoderCfg); err != nil {
		return err
	}
	defer enc.Close()

	srvCfg := server.Config{
		Addr:            *addr,
		Dataset:         table,
		DefaultTopK:     defaultTopK,
		RequestTimeout:  *requestTimeout,
		ShutdownTimeout: *shutdownTimeout,
	}

	srv, err := server.New(dbHandle, enc, srvCfg)
	if err != nil {
		return err
	}

	serveCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	return srv.Serve(serveCtx)
}

func runSearch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	configFlag := fs.String("config", "", "path to configuration file (default: config.json if present)")
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
	if *query == "" {
		return fmt.Errorf("query is required")
	}

	appCfg, err := loadConfig(*configFlag, flagWasProvided(fs, "config"))
	if err != nil {
		return err
	}

	datasetName := strings.TrimSpace(*tableName)
	if datasetName == "" && appCfg != nil && appCfg.DefaultDataset != "" {
		datasetName = appCfg.DefaultDataset
	}

	var dataset config.DatasetConfig
	if appCfg != nil && datasetName != "" {
		if ds, ok := appCfg.Dataset(datasetName); ok {
			dataset = ds
		}
	}

	table := firstNonEmpty(*tableName, dataset.Table, datasetName, "default")
	db := firstNonEmpty(*dbPath, configDatabasePath(appCfg), "data/app.db")
	if db == "" {
		return fmt.Errorf("db path is required")
	}

	ortDLL := *ortLib
	if ortDLL == "" && appCfg != nil {
		ortDLL = appCfg.ResolvePath(appCfg.Embedding.OrtLib)
	}
	if ortDLL == "" {
		return fmt.Errorf("ort-lib is required")
	}

	model := *modelPath
	if model == "" && appCfg != nil {
		model = appCfg.ResolvePath(appCfg.Embedding.Model)
	}
	if model == "" {
		return fmt.Errorf("model is required")
	}

	tokenizer := *tokenizerPath
	if tokenizer == "" && appCfg != nil {
		tokenizer = appCfg.ResolvePath(appCfg.Embedding.Tokenizer)
	}
	if tokenizer == "" {
		return fmt.Errorf("tokenizer is required")
	}

	seqLen := firstPositive(*maxSeqLen, cfgEmbeddingMaxSeqLen(appCfg), 512)
	limit := firstPositive(*topK, cfgSearchTopK(appCfg), 10)

	dbHandle, err := database.Open(db)
	if err != nil {
		return err
	}
	defer dbHandle.Close()

	enc := &emb.Encoder{}
	encoderCfg := emb.Config{
		OrtDLL:        ortDLL,
		ModelPath:     model,
		TokenizerPath: tokenizer,
		MaxSeqLen:     seqLen,
	}
	if err := enc.Init(encoderCfg); err != nil {
		return err
	}
	defer enc.Close()

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	results, err := search.VectorSearch(ctx, dbHandle, enc, table, *query, limit, []search.Filter(filterArgs))
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
  serve     Start the long-running HTTP search server

Use "%s <command> -h" to see command-specific options.
`, exe, exe)
}

func loadConfig(path string, required bool) (*config.Config, error) {
	normalized := strings.TrimSpace(path)
	if normalized == "" {
		normalized = "config.json"
	}
	cfg, err := config.Load(normalized)
	if err != nil {
		if os.IsNotExist(err) && !required {
			return nil, nil
		}
		return nil, err
	}
	return cfg, nil
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

func configDatabasePath(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.ResolvePath(cfg.Database.Path)
}

func cfgEmbeddingMaxSeqLen(cfg *config.Config) int {
	if cfg == nil {
		return 0
	}
	return cfg.Embedding.MaxSeqLen
}

func cfgSearchTopK(cfg *config.Config) int {
	if cfg == nil {
		return 0
	}
	return cfg.Search.DefaultTopK
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func cloneStrings(src []string) []string {
	if len(src) == 0 {
		return nil
	}
	out := make([]string, len(src))
	copy(out, src)
	return out
}

type filterFlag []search.Filter

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
	*f = append(*f, search.Filter{Field: field, Value: val})
	return nil
}
