package csvsearch

import (
	"database/sql"
	"fmt"
	"strings"

	"yashubustudio/csv-search/emb"
	"yashubustudio/csv-search/internal/config"
	"yashubustudio/csv-search/internal/database"
)

// ConfigReference describes how to load an optional JSON configuration file.
type ConfigReference struct {
	Path     string
	Required bool
}

// DatabaseOptions allows callers to reuse an existing *sql.DB or request the
// library to open one from the provided path.
type DatabaseOptions struct {
	Path   string
	Handle *sql.DB
}

// EncoderConfig lists the assets required to initialize the ONNX encoder.
type EncoderConfig struct {
	OrtLibrary        string
	ModelPath         string
	TokenizerPath     string
	MaxSequenceLength int
}

// EncoderOptions lets callers pass a pre-configured encoder or request the
// library to lazily create one from EncoderConfig or the JSON configuration.
type EncoderOptions struct {
	Instance *emb.Encoder
	Config   EncoderConfig
}

// ServiceOptions groups the dependencies required to build a Service.
type ServiceOptions struct {
	Config   ConfigReference
	Database DatabaseOptions
	Encoder  EncoderOptions
}

// Service exposes high level helpers that can be embedded into another Go
// application. The struct owns the database and encoder resources when it
// creates them and will release them on Close.
type Service struct {
	cfg          *config.Config
	db           *sql.DB
	dbPath       string
	closeDB      bool
	encoder      *emb.Encoder
	closeEncoder bool
	encoderCfg   EncoderConfig
}

// NewService loads the optional JSON configuration file, opens the database (if
// Handle is nil) and stores encoder configuration for lazy initialization.
func NewService(opts ServiceOptions) (*Service, error) {
	cfg, err := loadConfig(opts.Config.Path, opts.Config.Required)
	if err != nil {
		return nil, err
	}

	db, dbPath, closeDB, err := prepareDatabase(cfg, opts.Database)
	if err != nil {
		return nil, err
	}

	svc := &Service{
		cfg:          cfg,
		db:           db,
		dbPath:       dbPath,
		closeDB:      closeDB,
		encoder:      opts.Encoder.Instance,
		closeEncoder: opts.Encoder.Instance == nil && (opts.Encoder.Config != EncoderConfig{}),
	}

	svc.encoderCfg = resolveEncoderConfig(cfg, opts.Encoder.Config)
	if svc.encoder != nil {
		svc.closeEncoder = false
	}

	return svc, nil
}

// Close releases any resources that were created by the Service instance.
func (s *Service) Close() error {
	var firstErr error
	if s.closeEncoder && s.encoder != nil {
		s.encoder.Close()
		s.encoder = nil
	}
	if s.closeDB && s.db != nil {
		if err := s.db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.db = nil
		s.dbPath = ""
	}
	return firstErr
}

// Config returns the loaded configuration (if any).
func (s *Service) Config() *config.Config {
	return s.cfg
}

// DB exposes the underlying database handle.
func (s *Service) DB() *sql.DB {
	return s.db
}

// DatabasePath returns the resolved filesystem path backing the SQLite
// connection when available.
func (s *Service) DatabasePath() string {
	return s.dbPath
}

// Encoder returns the lazily created encoder instance, initializing it if
// necessary.
func (s *Service) Encoder() (*emb.Encoder, error) {
	return s.ensureEncoder()
}

func prepareDatabase(cfg *config.Config, opts DatabaseOptions) (*sql.DB, string, bool, error) {
	if opts.Handle != nil {
		return opts.Handle, strings.TrimSpace(opts.Path), false, nil
	}
	path := firstNonEmpty(strings.TrimSpace(opts.Path), configDatabasePath(cfg), "data/app.db")
	if path == "" {
		return nil, "", false, fmt.Errorf("database path is required")
	}
	db, err := database.Open(path)
	if err != nil {
		return nil, path, false, err
	}
	return db, path, true, nil
}

func resolveEncoderConfig(cfg *config.Config, opts EncoderConfig) EncoderConfig {
	resolved := EncoderConfig{}
	if cfg != nil {
		resolved.OrtLibrary = cfg.ResolvePath(cfg.Embedding.OrtLib)
		resolved.ModelPath = cfg.ResolvePath(cfg.Embedding.Model)
		resolved.TokenizerPath = cfg.ResolvePath(cfg.Embedding.Tokenizer)
		resolved.MaxSequenceLength = cfg.Embedding.MaxSeqLen
	}

	if opts.OrtLibrary != "" {
		resolved.OrtLibrary = opts.OrtLibrary
	}
	if opts.ModelPath != "" {
		resolved.ModelPath = opts.ModelPath
	}
	if opts.TokenizerPath != "" {
		resolved.TokenizerPath = opts.TokenizerPath
	}
	if opts.MaxSequenceLength > 0 {
		resolved.MaxSequenceLength = opts.MaxSequenceLength
	}

	return resolved
}

func (s *Service) ensureEncoder() (*emb.Encoder, error) {
	if s.encoder != nil {
		return s.encoder, nil
	}

	cfg := s.encoderCfg
	if cfg.OrtLibrary == "" || cfg.ModelPath == "" || cfg.TokenizerPath == "" {
		return nil, fmt.Errorf("encoder configuration is incomplete")
	}

	enc := &emb.Encoder{}
	encoderCfg := emb.Config{
		OrtDLL:        cfg.OrtLibrary,
		ModelPath:     cfg.ModelPath,
		TokenizerPath: cfg.TokenizerPath,
		MaxSeqLen:     cfg.MaxSequenceLength,
	}
	if err := enc.Init(encoderCfg); err != nil {
		return nil, err
	}

	s.encoder = enc
	s.closeEncoder = true
	return enc, nil
}
