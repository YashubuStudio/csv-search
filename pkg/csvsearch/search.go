package csvsearch

import (
	"context"
	"fmt"
	"strings"

	intsearch "yashubustudio/csv-search/internal/search"
)

// Filter represents a metadata equality condition applied to search results.
type Filter struct {
	Field string
	Value string
}

// Result mirrors the JSON structure returned by the HTTP API and search
// subcommand.
type Result struct {
	Dataset string            `json:"dataset"`
	ID      string            `json:"id"`
	Fields  map[string]string `json:"fields,omitempty"`
	Score   float64           `json:"score"`
	Lat     *float64          `json:"lat,omitempty"`
	Lng     *float64          `json:"lng,omitempty"`
}

// SearchOptions describe how to run a semantic search request against the
// embedded vector index.
type SearchOptions struct {
	Query   string
	Dataset string
	Table   string
	TopK    int
	Filters []Filter
}

// Search encodes the query with the ONNX encoder and performs cosine similarity
// ranking against the stored vectors.
func (s *Service) Search(ctx context.Context, opts SearchOptions) ([]Result, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context must not be nil")
	}
	if s.db == nil {
		return nil, fmt.Errorf("database handle is nil")
	}
	if strings.TrimSpace(opts.Query) == "" {
		return nil, fmt.Errorf("query is required")
	}

	datasetName, dataset, _ := resolveDataset(s.cfg, opts.Dataset)
	table := resolveTable(datasetName, dataset, opts.Table)
	limit := firstPositive(opts.TopK, cfgSearchTopK(s.cfg), 10)

	enc, err := s.ensureEncoder()
	if err != nil {
		return nil, err
	}

	filters := make([]intsearch.Filter, 0, len(opts.Filters))
	for _, f := range opts.Filters {
		field := strings.TrimSpace(f.Field)
		if field == "" {
			continue
		}
		filters = append(filters, intsearch.Filter{Field: field, Value: f.Value})
	}

	results, err := intsearch.VectorSearch(ctx, s.db, enc, table, opts.Query, limit, filters)
	if err != nil {
		return nil, err
	}

	converted := make([]Result, len(results))
	for i, r := range results {
		converted[i] = Result{
			Dataset: r.Dataset,
			ID:      r.ID,
			Fields:  r.Fields,
			Score:   r.Score,
			Lat:     r.Lat,
			Lng:     r.Lng,
		}
	}
	return converted, nil
}
