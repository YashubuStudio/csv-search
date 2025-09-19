package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"yashubustudio/csv-search/emb"
	"yashubustudio/csv-search/internal/vector"
)

// Result represents a row returned from a vector similarity search.
type Result struct {
	Dataset string            `json:"dataset"`
	ID      string            `json:"id"`
	Fields  map[string]string `json:"fields,omitempty"`
	Score   float64           `json:"score"`
	Lat     *float64          `json:"lat,omitempty"`
	Lng     *float64          `json:"lng,omitempty"`
}

// VectorSearch encodes the query with enc and ranks records stored in the
// database by cosine similarity. The dataset parameter selects which logical
// table to search. The topK parameter controls how many results are returned
// (defaults to 10 when non-positive).
func VectorSearch(ctx context.Context, db *sql.DB, enc *emb.Encoder, dataset, query string, topK int) ([]Result, error) {
	if enc == nil {
		return nil, fmt.Errorf("encoder is nil")
	}
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	if query == "" {
		return nil, fmt.Errorf("query must not be empty")
	}
	if topK <= 0 {
		topK = 10
	}

	dataset = strings.TrimSpace(dataset)
	if dataset == "" {
		dataset = "default"
	}

	qvec, err := enc.Encode(query)
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
                SELECT r.id, r.data, r.lat, r.lng, v.embedding
                FROM records AS r
                INNER JOIN records_vec AS v
                        ON r.dataset = v.dataset AND r.id = v.id
                WHERE r.dataset = ?;
        `, dataset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var (
			r    Result
			data string
			lat  sql.NullFloat64
			lng  sql.NullFloat64
			blob []byte
		)
		if err := rows.Scan(&r.ID, &data, &lat, &lng, &blob); err != nil {
			return nil, err
		}

		if err := json.Unmarshal([]byte(data), &r.Fields); err != nil {
			return nil, fmt.Errorf("decode metadata for %s: %w", r.ID, err)
		}

		vec, err := vector.Deserialize(blob)
		if err != nil {
			return nil, err
		}
		r.Score = vector.Cosine(qvec, vec)
		r.Dataset = dataset

		if lat.Valid {
			v := lat.Float64
			r.Lat = &v
		}
		if lng.Valid {
			v := lng.Float64
			r.Lng = &v
		}

		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].ID < results[j].ID
		}
		return results[i].Score > results[j].Score
	})

	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}
