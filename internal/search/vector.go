package search

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"yashubustudio/csv-search/emb"
	"yashubustudio/csv-search/internal/vector"
)

// Result represents a row returned from a vector similarity search.
type Result struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Tags      string   `json:"tags"`
	Category  string   `json:"category"`
	Price     *float64 `json:"price,omitempty"`
	Stock     *int64   `json:"stock,omitempty"`
	CreatedAt *int64   `json:"created_at,omitempty"`
	UpdatedAt *int64   `json:"updated_at,omitempty"`
	Lat       *float64 `json:"lat,omitempty"`
	Lng       *float64 `json:"lng,omitempty"`
	Score     float64  `json:"score"`
}

// VectorSearch encodes the query with enc and ranks items stored in the
// database by cosine similarity. The topK parameter controls how many results
// are returned (defaults to 10 when non-positive).
func VectorSearch(ctx context.Context, db *sql.DB, enc *emb.Encoder, query string, topK int) ([]Result, error) {
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

	qvec, err := enc.Encode(query)
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `
                SELECT id, title, body, tags, category,
                       price, stock, created_at, updated_at, lat, lng, embedding
                FROM items
                INNER JOIN items_vec USING(id);
        `)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var (
			r       Result
			price   sql.NullFloat64
			stock   sql.NullInt64
			created sql.NullInt64
			updated sql.NullInt64
			lat     sql.NullFloat64
			lng     sql.NullFloat64
			blob    []byte
		)
		if err := rows.Scan(
			&r.ID, &r.Title, &r.Body, &r.Tags, &r.Category,
			&price, &stock, &created, &updated, &lat, &lng, &blob,
		); err != nil {
			return nil, err
		}

		vec, err := vector.Deserialize(blob)
		if err != nil {
			return nil, err
		}
		score := vector.Cosine(qvec, vec)
		r.Score = score

		if price.Valid {
			v := price.Float64
			r.Price = &v
		}
		if stock.Valid {
			v := stock.Int64
			r.Stock = &v
		}
		if created.Valid {
			v := created.Int64
			r.CreatedAt = &v
		}
		if updated.Valid {
			v := updated.Int64
			r.UpdatedAt = &v
		}
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
