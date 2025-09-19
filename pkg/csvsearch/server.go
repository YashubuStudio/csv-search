package csvsearch

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"yashubustudio/csv-search/internal/server"
)

// ServeOptions configure the HTTP API server exposed by the Service.
type ServeOptions struct {
	Address         string
	Dataset         string
	Table           string
	TopK            int
	RequestTimeout  time.Duration
	ShutdownTimeout time.Duration
	AutoIngest      *bool
}

// APIServer wraps the internal server.Server to provide a stable API surface for
// applications embedding csv-search.
type APIServer struct {
	server *server.Server
}

// Handler exposes the HTTP handler so that callers can mount it on an existing
// http.Server or router.
func (s *APIServer) Handler() http.Handler {
	if s == nil || s.server == nil {
		return nil
	}
	return s.server.Handler()
}

// Serve starts the HTTP server using the provided context for shutdown signals.
func (s *APIServer) Serve(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("server is nil")
	}
	return s.server.Serve(ctx)
}

// NewAPIServer prepares the HTTP API server using the provided options. The
// caller is responsible for ensuring the database schema exists (use
// InitDatabase) and ingesting data before serving traffic.
func (s *Service) NewAPIServer(opts ServeOptions) (*APIServer, error) {
	if s.db == nil {
		return nil, fmt.Errorf("database handle is nil")
	}

	datasetName, datasetCfg, _ := resolveDataset(s.cfg, opts.Dataset)
	table := resolveTable(datasetName, datasetCfg, opts.Table)
	defaultTopK := firstPositive(opts.TopK, cfgSearchTopK(s.cfg), 10)

	reqTimeout := opts.RequestTimeout
	if reqTimeout <= 0 {
		reqTimeout = 30 * time.Second
	}
	shutdownTimeout := opts.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = 5 * time.Second
	}

	addr := firstNonEmpty(strings.TrimSpace(opts.Address), ":8080")

	enc, err := s.ensureEncoder()
	if err != nil {
		return nil, err
	}

	cfg := server.Config{
		Addr:            addr,
		Dataset:         table,
		DefaultTopK:     defaultTopK,
		RequestTimeout:  reqTimeout,
		ShutdownTimeout: shutdownTimeout,
	}

	srv, err := server.New(s.db, enc, cfg)
	if err != nil {
		return nil, err
	}
	return &APIServer{server: srv}, nil
}

// StartServer optionally ingests data from the configuration and starts the HTTP
// server until the context is cancelled.
func (s *Service) StartServer(ctx context.Context, opts ServeOptions) error {
	if ctx == nil {
		return fmt.Errorf("context must not be nil")
	}

	if err := s.InitDatabase(ctx, InitDatabaseOptions{}); err != nil {
		return err
	}

	datasetName, datasetCfg, hasDataset := resolveDataset(s.cfg, opts.Dataset)
	table := resolveTable(datasetName, datasetCfg, opts.Table)

	autoIngest := true
	if opts.AutoIngest != nil {
		autoIngest = *opts.AutoIngest
	}

	if autoIngest && hasDataset && strings.TrimSpace(datasetCfg.CSV) != "" {
		if _, err := s.Ingest(ctx, IngestOptions{Dataset: datasetName, Table: table}); err != nil {
			return err
		}
	}

	apiServer, err := s.NewAPIServer(ServeOptions{
		Address:         opts.Address,
		Dataset:         datasetName,
		Table:           table,
		TopK:            opts.TopK,
		RequestTimeout:  opts.RequestTimeout,
		ShutdownTimeout: opts.ShutdownTimeout,
	})
	if err != nil {
		return err
	}
	return apiServer.Serve(ctx)
}
