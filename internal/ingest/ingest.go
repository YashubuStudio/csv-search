package ingest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"yashubustudio/csv-search/emb"
	"yashubustudio/csv-search/internal/vector"
)

// ColumnMapping describes how CSV columns map to internal fields. Leaving a
// value empty disables the corresponding column. The ingest CLI sets sensible
// defaults such as "id", "title", ... but callers may override them.
type ColumnMapping struct {
	ID        string
	Title     string
	Body      string
	Tags      string
	Category  string
	Price     string
	Stock     string
	CreatedAt string
	UpdatedAt string
	Lat       string
	Lng       string
}

// Options control the ingest process.
type Options struct {
	CSVPath   string
	BatchSize int
	Columns   ColumnMapping
}

type columnIndexes struct {
	ID        int
	Title     int
	Body      int
	Tags      int
	Category  int
	Price     int
	Stock     int
	CreatedAt int
	UpdatedAt int
	Lat       int
	Lng       int
}

type item struct {
	ID        string
	Title     string
	Body      string
	Tags      string
	Category  string
	Price     *float64
	Stock     *int64
	CreatedAt *int64
	UpdatedAt *int64
	Lat       *float64
	Lng       *float64
}

// Run reads the CSV file at opts.CSVPath, converts records into database rows
// and stores them with embeddings generated via enc. The caller must provide an
// initialized encoder (see emb.Encoder).
func Run(ctx context.Context, db *sql.DB, enc *emb.Encoder, opts Options) error {
	if opts.CSVPath == "" {
		return errors.New("csv path is required")
	}
	if db == nil {
		return errors.New("db is nil")
	}
	if enc == nil {
		return errors.New("encoder is nil")
	}

	file, err := os.Open(opts.CSVPath)
	if err != nil {
		return err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	idx, err := resolveColumns(header, opts.Columns)
	if err != nil {
		return err
	}

	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = 1000
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	rowsProcessed := 0
	line := 1 // header already read
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			return fmt.Errorf("read row %d: %w", line, err)
		}

		it, err := buildItem(record, idx)
		if err != nil {
			return fmt.Errorf("row %d: %w", line, err)
		}
		hash := hashItem(it)

		skip, err := shouldSkip(ctx, tx, it.ID, hash)
		if err != nil {
			return fmt.Errorf("row %d: %w", line, err)
		}
		if skip {
			continue
		}

		text := embeddingText(it)
		var embedding []float32
		if strings.TrimSpace(text) != "" {
			embedding, err = enc.Encode(text)
			if err != nil {
				return fmt.Errorf("row %d encode: %w", line, err)
			}
		}

		if err := upsertItem(ctx, tx, it, hash, embedding); err != nil {
			return fmt.Errorf("row %d: %w", line, err)
		}

		rowsProcessed++
		if rowsProcessed%batchSize == 0 {
			if err := tx.Commit(); err != nil {
				return err
			}
			tx = nil
			tx, err = db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
		}
	}

	if tx != nil {
		if err := tx.Commit(); err != nil {
			return err
		}
		tx = nil
	}
	return nil
}

func resolveColumns(header []string, mapping ColumnMapping) (columnIndexes, error) {
	lookup := make(map[string]int, len(header))
	for i, h := range header {
		key := strings.ToLower(strings.TrimSpace(h))
		lookup[key] = i
	}

	result := columnIndexes{
		Title:     -1,
		Body:      -1,
		Tags:      -1,
		Category:  -1,
		Price:     -1,
		Stock:     -1,
		CreatedAt: -1,
		UpdatedAt: -1,
		Lat:       -1,
		Lng:       -1,
	}

	get := func(name string, required bool) (int, error) {
		if name == "" {
			return -1, nil
		}
		idx, ok := lookup[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			if required {
				return -1, fmt.Errorf("column %q not found", name)
			}
			return -1, nil
		}
		return idx, nil
	}

	var err error
	result.ID, err = get(mapping.ID, true)
	if err != nil {
		return result, err
	}
	if result.ID < 0 {
		return result, errors.New("id column is required")
	}

	if result.Title, err = get(mapping.Title, false); err != nil {
		return result, err
	}
	if result.Body, err = get(mapping.Body, false); err != nil {
		return result, err
	}
	if result.Tags, err = get(mapping.Tags, false); err != nil {
		return result, err
	}
	if result.Category, err = get(mapping.Category, false); err != nil {
		return result, err
	}
	if result.Price, err = get(mapping.Price, false); err != nil {
		return result, err
	}
	if result.Stock, err = get(mapping.Stock, false); err != nil {
		return result, err
	}
	if result.CreatedAt, err = get(mapping.CreatedAt, false); err != nil {
		return result, err
	}
	if result.UpdatedAt, err = get(mapping.UpdatedAt, false); err != nil {
		return result, err
	}
	if result.Lat, err = get(mapping.Lat, false); err != nil {
		return result, err
	}
	if result.Lng, err = get(mapping.Lng, false); err != nil {
		return result, err
	}
	return result, nil
}

func buildItem(record []string, idx columnIndexes) (*item, error) {
	if idx.ID >= len(record) || idx.ID < 0 {
		return nil, errors.New("id column missing in record")
	}
	get := func(i int) string {
		if i < 0 || i >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[i])
	}

	it := &item{
		ID:       get(idx.ID),
		Title:    get(idx.Title),
		Body:     get(idx.Body),
		Tags:     get(idx.Tags),
		Category: get(idx.Category),
	}
	if it.ID == "" {
		return nil, errors.New("id column is empty")
	}

	if idx.Price >= 0 {
		val := get(idx.Price)
		if parsed, err := parseFloat(val); err != nil {
			return nil, fmt.Errorf("price: %w", err)
		} else {
			it.Price = parsed
		}
	}
	if idx.Stock >= 0 {
		val := get(idx.Stock)
		if parsed, err := parseInt(val); err != nil {
			return nil, fmt.Errorf("stock: %w", err)
		} else {
			it.Stock = parsed
		}
	}
	if idx.CreatedAt >= 0 {
		val := get(idx.CreatedAt)
		if parsed, err := parseTime(val); err != nil {
			return nil, fmt.Errorf("created_at: %w", err)
		} else {
			it.CreatedAt = parsed
		}
	}
	if idx.UpdatedAt >= 0 {
		val := get(idx.UpdatedAt)
		if parsed, err := parseTime(val); err != nil {
			return nil, fmt.Errorf("updated_at: %w", err)
		} else {
			it.UpdatedAt = parsed
		}
	}
	if idx.Lat >= 0 {
		val := get(idx.Lat)
		if parsed, err := parseFloat(val); err != nil {
			return nil, fmt.Errorf("lat: %w", err)
		} else {
			it.Lat = parsed
		}
	}
	if idx.Lng >= 0 {
		val := get(idx.Lng)
		if parsed, err := parseFloat(val); err != nil {
			return nil, fmt.Errorf("lng: %w", err)
		} else {
			it.Lng = parsed
		}
	}
	return it, nil
}

func parseFloat(val string) (*float64, error) {
	if val == "" {
		return nil, nil
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func parseInt(val string) (*int64, error) {
	if val == "" {
		return nil, nil
	}
	i, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return nil, err
	}
	return &i, nil
}

func parseTime(val string) (*int64, error) {
	if val == "" {
		return nil, nil
	}
	if i, err := strconv.ParseInt(val, 10, 64); err == nil {
		return &i, nil
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, val); err == nil {
			unix := t.Unix()
			return &unix, nil
		}
	}
	return nil, fmt.Errorf("cannot parse time value %q", val)
}

func hashItem(it *item) string {
	parts := []string{
		it.ID,
		it.Title,
		it.Body,
		it.Tags,
		it.Category,
		formatFloat(it.Price),
		formatInt(it.Stock),
		formatInt(it.CreatedAt),
		formatInt(it.UpdatedAt),
		formatFloat(it.Lat),
		formatFloat(it.Lng),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

func formatFloat(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

func formatInt(v *int64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(*v, 10)
}

func shouldSkip(ctx context.Context, tx *sql.Tx, id, hash string) (bool, error) {
	var existing sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT hash FROM items WHERE id = ?`, id).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if existing.Valid && existing.String == hash {
		return true, nil
	}
	return false, nil
}

func embeddingText(it *item) string {
	var parts []string
	for _, v := range []string{it.Title, it.Body, it.Tags, it.Category} {
		if strings.TrimSpace(v) != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, "\n")
}

func upsertItem(ctx context.Context, tx *sql.Tx, it *item, hash string, embedding []float32) error {
	_, err := tx.ExecContext(ctx, `
                INSERT INTO items(
                        id, title, body, tags, category,
                        price, stock, created_at, updated_at, lat, lng, hash
                ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(id) DO UPDATE SET
                        title=excluded.title,
                        body=excluded.body,
                        tags=excluded.tags,
                        category=excluded.category,
                        price=excluded.price,
                        stock=excluded.stock,
                        created_at=excluded.created_at,
                        updated_at=excluded.updated_at,
                        lat=excluded.lat,
                        lng=excluded.lng,
                        hash=excluded.hash;
        `,
		it.ID,
		nullString(it.Title),
		nullString(it.Body),
		nullString(it.Tags),
		nullString(it.Category),
		nullFloat(it.Price),
		nullInt(it.Stock),
		nullInt(it.CreatedAt),
		nullInt(it.UpdatedAt),
		nullFloat(it.Lat),
		nullFloat(it.Lng),
		hash,
	)
	if err != nil {
		return err
	}

	var rowid int64
	if err := tx.QueryRowContext(ctx, `SELECT rowid FROM items WHERE id = ?`, it.ID).Scan(&rowid); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM items_fts WHERE rowid = ?`, rowid); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO items_fts(rowid, id, title, body, tags) VALUES(?, ?, ?, ?, ?)`,
		rowid,
		it.ID,
		nullString(it.Title),
		nullString(it.Body),
		nullString(it.Tags),
	); err != nil {
		return err
	}

	if it.Lat != nil && it.Lng != nil {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO items_rtree VALUES(?, ?, ?, ?, ?)`,
			rowid,
			*it.Lat,
			*it.Lat,
			*it.Lng,
			*it.Lng,
		); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `DELETE FROM items_rtree WHERE item_rowid = ?`, rowid); err != nil {
			return err
		}
	}

	if len(embedding) > 0 {
		blob := vector.Serialize(embedding)
		if _, err := tx.ExecContext(ctx, `
                        INSERT INTO items_vec(id, embedding) VALUES(?, ?)
                        ON CONFLICT(id) DO UPDATE SET embedding=excluded.embedding;
                `, it.ID, blob); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `DELETE FROM items_vec WHERE id = ?`, it.ID); err != nil {
			return err
		}
	}

	return nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullInt(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}
