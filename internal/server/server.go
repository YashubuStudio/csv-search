package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"yashubustudio/csv-search/emb"
	"yashubustudio/csv-search/internal/search"
)

type Config struct {
	Addr            string
	Dataset         string
	DefaultTopK     int
	RequestTimeout  time.Duration
	ShutdownTimeout time.Duration
}

type Server struct {
	db       *sql.DB
	enc      *emb.Encoder
	cfg      Config
	encodeMu sync.Mutex
}

func New(db *sql.DB, enc *emb.Encoder, cfg Config) (*Server, error) {
	if db == nil {
		return nil, fmt.Errorf("db must not be nil")
	}
	if enc == nil {
		return nil, fmt.Errorf("encoder must not be nil")
	}
	cfg.Dataset = strings.TrimSpace(cfg.Dataset)
	if cfg.Dataset == "" {
		cfg.Dataset = "default"
	}
	cfg.Addr = strings.TrimSpace(cfg.Addr)
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	if cfg.DefaultTopK <= 0 {
		cfg.DefaultTopK = 10
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 5 * time.Second
	}
	return &Server{db: db, enc: enc, cfg: cfg}, nil
}

func (s *Server) Serve(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context must not be nil")
	}
	handler := s.Handler()
	srv := &http.Server{
		Addr:    s.cfg.Addr,
		Handler: handler,
	}

	log.Printf("csv-search server listening on %s (dataset=%s, topK=%d)\n", s.cfg.Addr, s.cfg.Dataset, s.cfg.DefaultTopK)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		err := <-errCh
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			log.Printf("csv-search server shutdown complete\n")
			return nil
		}
		return err
	case err := <-errCh:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Handler builds a new http.Handler exposing the search and health endpoints.
// Callers can mount the handler on an existing mux when embedding the service.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", s.handleSearch)
	mux.HandleFunc("/healthz", s.handleHealth)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

type searchRequest struct {
	Query   string
	Dataset string
	TopK    int
	Filters []search.Filter
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodPost:
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	req, err := s.decodeSearchRequest(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("query is required"))
		return
	}

	dataset := req.Dataset
	if dataset == "" {
		dataset = s.cfg.Dataset
	}
	topK := req.TopK
	if topK <= 0 {
		topK = s.cfg.DefaultTopK
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout)
	defer cancel()

	s.encodeMu.Lock()
	results, err := search.VectorSearch(ctx, s.db, s.enc, dataset, req.Query, topK, req.Filters)
	s.encodeMu.Unlock()
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		s.writeError(w, status, err)
		return
	}

	s.writeJSON(w, http.StatusOK, results)
}

func (s *Server) decodeSearchRequest(r *http.Request) (searchRequest, error) {
	if r.Method == http.MethodGet {
		values := r.URL.Query()
		query := strings.TrimSpace(values.Get("q"))
		if query == "" {
			query = strings.TrimSpace(values.Get("query"))
		}
		dataset := strings.TrimSpace(values.Get("dataset"))
		if dataset == "" {
			dataset = strings.TrimSpace(values.Get("table"))
		}
		topK := 0
		if rawTopK := strings.TrimSpace(values.Get("topk")); rawTopK != "" {
			v, err := strconv.Atoi(rawTopK)
			if err != nil {
				return searchRequest{}, fmt.Errorf("invalid topk value %q", rawTopK)
			}
			topK = v
		}
		filters, err := parseFilterValues(values["filter"])
		if err != nil {
			return searchRequest{}, err
		}
		return searchRequest{Query: query, Dataset: dataset, TopK: topK, Filters: filters}, nil
	}

	var payload struct {
		Query   string            `json:"query"`
		Dataset string            `json:"dataset"`
		TopK    int               `json:"topk"`
		Filters map[string]string `json:"filters"`
		Filter  []string          `json:"filter"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return searchRequest{}, fmt.Errorf("decode request: %w", err)
	}

	req := searchRequest{
		Query:   strings.TrimSpace(payload.Query),
		Dataset: strings.TrimSpace(payload.Dataset),
		TopK:    payload.TopK,
	}
	if len(payload.Filters) > 0 {
		req.Filters = make([]search.Filter, 0, len(payload.Filters))
		for k, v := range payload.Filters {
			key := strings.TrimSpace(k)
			if key == "" {
				return searchRequest{}, fmt.Errorf("filter key must not be empty")
			}
			req.Filters = append(req.Filters, search.Filter{Field: key, Value: v})
		}
	}
	if len(payload.Filter) > 0 {
		extra, err := parseFilterValues(payload.Filter)
		if err != nil {
			return searchRequest{}, err
		}
		req.Filters = append(req.Filters, extra...)
	}
	return req, nil
}

func parseFilterValues(values []string) ([]search.Filter, error) {
	if len(values) == 0 {
		return nil, nil
	}
	filters := make([]search.Filter, 0, len(values))
	for _, raw := range values {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("filter must be in the form field=value")
		}
		field := strings.TrimSpace(parts[0])
		if field == "" {
			return nil, fmt.Errorf("filter field must not be empty")
		}
		filters = append(filters, search.Filter{Field: field, Value: parts[1]})
	}
	return filters, nil
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	if status == http.StatusOK {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(v); err != nil {
		log.Printf("writeJSON encode error: %v\n", err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, status int, err error) {
	if err == nil {
		err = fmt.Errorf("unknown error")
	}
	payload := map[string]string{"error": err.Error()}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if encodeErr := json.NewEncoder(w).Encode(payload); encodeErr != nil {
		log.Printf("writeError encode error: %v\n", encodeErr)
	}
}
