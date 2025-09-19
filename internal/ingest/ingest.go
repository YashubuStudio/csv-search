package ingest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"yashubustudio/csv-search/emb"
	"yashubustudio/csv-search/internal/vector"
)

// ColumnConfig describes how CSV columns map to internal fields. The ID column
// is mandatory. Text columns are concatenated to form the text that is passed
// to the embedding model and FTS index. Metadata columns are persisted as-is in
// the records table. Leaving Metadata empty (or using "*") stores every column
// from the CSV as metadata.
type ColumnConfig struct {
	ID       string
	Text     []string
	Metadata []string
	Lat      string
	Lng      string
}

// Options control the ingest process.
type Options struct {
	CSVPath   string
	BatchSize int
	Dataset   string
	Columns   ColumnConfig
}

type columnIndex struct {
	Name  string
	Index int
}

type columnIndexes struct {
	ID       columnIndex
	Text     []columnIndex
	Metadata []columnIndex
	Lat      columnIndex
	Lng      columnIndex
}

type record struct {
	ID        string
	Metadata  map[string]string
	TextParts []string
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

	dataset := strings.TrimSpace(opts.Dataset)
	if dataset == "" {
		dataset = "default"
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
	idx, err := resolveColumns(header, opts)
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
		recordValues, err := reader.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			return fmt.Errorf("read row %d: %w", line, err)
		}

		rec, err := buildRecord(recordValues, idx)
		if err != nil {
			return fmt.Errorf("row %d: %w", line, err)
		}
		hash := hashRecord(dataset, rec)

		skip, err := shouldSkip(ctx, tx, dataset, rec.ID, hash)
		if err != nil {
			return fmt.Errorf("row %d: %w", line, err)
		}
		if skip {
			continue
		}

		text := embeddingText(rec)
		var embedding []float32
		if strings.TrimSpace(text) != "" {
			embedding, err = enc.Encode(text)
			if err != nil {
				return fmt.Errorf("row %d encode: %w", line, err)
			}
		}

		if err := upsertRecord(ctx, tx, dataset, rec, hash, embedding); err != nil {
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

func resolveColumns(header []string, opts Options) (columnIndexes, error) {
	lookup := make(map[string]columnIndex, len(header))
	normalized := make([]string, len(header))
	for i, h := range header {
		trimmed := strings.TrimSpace(h)
		normalized[i] = trimmed
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		lookup[key] = columnIndex{Name: trimmed, Index: i}
	}

	get := func(name string, required bool) (columnIndex, error) {
		cleaned := strings.TrimSpace(name)
		if cleaned == "" {
			if required {
				return columnIndex{}, fmt.Errorf("column name is required")
			}
			return columnIndex{Name: cleaned, Index: -1}, nil
		}
		if col, ok := lookup[strings.ToLower(cleaned)]; ok {
			return col, nil
		}
		if required {
			return columnIndex{}, fmt.Errorf("column %q not found", cleaned)
		}
		return columnIndex{Name: cleaned, Index: -1}, nil
	}

	var result columnIndexes
	var err error
	result.ID, err = get(opts.Columns.ID, true)
	if err != nil {
		return result, err
	}
	if result.ID.Index < 0 {
		return result, errors.New("id column is required")
	}

	if result.Lat, err = get(opts.Columns.Lat, false); err != nil {
		return result, err
	}
	if result.Lng, err = get(opts.Columns.Lng, false); err != nil {
		return result, err
	}

	metadataSet := make(map[string]bool)
	addMetadata := func(ci columnIndex) {
		if ci.Index < 0 {
			return
		}
		if metadataSet[ci.Name] {
			return
		}
		metadataSet[ci.Name] = true
		result.Metadata = append(result.Metadata, ci)
	}

	includeAll := len(opts.Columns.Metadata) == 0
	if len(opts.Columns.Metadata) == 1 && strings.TrimSpace(opts.Columns.Metadata[0]) == "*" {
		includeAll = true
	}

	if includeAll {
		for i, name := range normalized {
			if name == "" {
				continue
			}
			addMetadata(columnIndex{Name: name, Index: i})
		}
	} else {
		for _, name := range opts.Columns.Metadata {
			ci, err := get(name, true)
			if err != nil {
				return result, err
			}
			addMetadata(ci)
		}
	}

	addMetadata(result.ID)
	if result.Lat.Index >= 0 {
		addMetadata(result.Lat)
	}
	if result.Lng.Index >= 0 {
		addMetadata(result.Lng)
	}

	textNames := opts.Columns.Text
	if len(textNames) == 0 {
		for _, ci := range result.Metadata {
			if ci.Index == result.ID.Index {
				continue
			}
			if result.Lat.Index >= 0 && ci.Index == result.Lat.Index {
				continue
			}
			if result.Lng.Index >= 0 && ci.Index == result.Lng.Index {
				continue
			}
			result.Text = append(result.Text, ci)
		}
	} else {
		seen := make(map[string]bool)
		for _, name := range textNames {
			ci, err := get(name, true)
			if err != nil {
				return result, err
			}
			if ci.Index < 0 {
				continue
			}
			if seen[ci.Name] {
				continue
			}
			seen[ci.Name] = true
			result.Text = append(result.Text, ci)
		}
	}

	return result, nil
}

func buildRecord(row []string, idx columnIndexes) (*record, error) {
	if idx.ID.Index >= len(row) || idx.ID.Index < 0 {
		return nil, errors.New("id column missing in record")
	}
	get := func(i int) string {
		if i < 0 || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}

	idVal := get(idx.ID.Index)
	if idVal == "" {
		return nil, errors.New("id column is empty")
	}

	metadata := make(map[string]string, len(idx.Metadata))
	for _, ci := range idx.Metadata {
		metadata[ci.Name] = get(ci.Index)
	}

	textParts := make([]string, 0, len(idx.Text))
	for _, ci := range idx.Text {
		val := get(ci.Index)
		if strings.TrimSpace(val) != "" {
			textParts = append(textParts, val)
		}
	}

	rec := &record{
		ID:        idVal,
		Metadata:  metadata,
		TextParts: textParts,
	}

	if idx.Lat.Index >= 0 {
		val := get(idx.Lat.Index)
		if parsed, err := parseFloat(val); err != nil {
			return nil, fmt.Errorf("%s: %w", idx.Lat.Name, err)
		} else {
			rec.Lat = parsed
		}
	}
	if idx.Lng.Index >= 0 {
		val := get(idx.Lng.Index)
		if parsed, err := parseFloat(val); err != nil {
			return nil, fmt.Errorf("%s: %w", idx.Lng.Name, err)
		} else {
			rec.Lng = parsed
		}
	}
	return rec, nil
}

func parseFloat(val string) (*float64, error) {
	if strings.TrimSpace(val) == "" {
		return nil, nil
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func hashRecord(dataset string, rec *record) string {
	parts := []string{dataset, rec.ID}
	if len(rec.TextParts) > 0 {
		parts = append(parts, strings.Join(rec.TextParts, "\n"))
	}

	keys := make([]string, 0, len(rec.Metadata))
	for k := range rec.Metadata {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, k+"="+rec.Metadata[k])
	}

	parts = append(parts, formatFloat(rec.Lat), formatFloat(rec.Lng))

	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

func shouldSkip(ctx context.Context, tx *sql.Tx, dataset, id, hash string) (bool, error) {
	var existing sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT hash FROM records WHERE dataset = ? AND id = ?`, dataset, id).Scan(&existing)
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

func embeddingText(rec *record) string {
	return strings.Join(rec.TextParts, "\n")
}

func metadataJSON(metadata map[string]string) (string, error) {
	if metadata == nil {
		metadata = map[string]string{}
	}
	buf, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func upsertRecord(ctx context.Context, tx *sql.Tx, dataset string, rec *record, hash string, embedding []float32) error {
	metaJSON, err := metadataJSON(rec.Metadata)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
                INSERT INTO records(
                        dataset, id, data, lat, lng, hash
                ) VALUES(?, ?, ?, ?, ?, ?)
                ON CONFLICT(dataset, id) DO UPDATE SET
                        data=excluded.data,
                        lat=excluded.lat,
                        lng=excluded.lng,
                        hash=excluded.hash;
        `,
		dataset,
		rec.ID,
		metaJSON,
		nullFloat(rec.Lat),
		nullFloat(rec.Lng),
		hash,
	)
	if err != nil {
		return err
	}

	var rowid int64
	if err := tx.QueryRowContext(ctx, `SELECT rowid FROM records WHERE dataset = ? AND id = ?`, dataset, rec.ID).Scan(&rowid); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM records_fts WHERE rowid = ?`, rowid); err != nil {
		return err
	}
	if text := embeddingText(rec); strings.TrimSpace(text) != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO records_fts(rowid, dataset, id, content) VALUES(?, ?, ?, ?)`,
			rowid,
			dataset,
			rec.ID,
			text,
		); err != nil {
			return err
		}
	}

	if rec.Lat != nil && rec.Lng != nil {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO records_rtree VALUES(?, ?, ?, ?, ?)`,
			rowid,
			*rec.Lat,
			*rec.Lat,
			*rec.Lng,
			*rec.Lng,
		); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `DELETE FROM records_rtree WHERE rowid = ?`, rowid); err != nil {
			return err
		}
	}

	if len(embedding) > 0 {
		blob := vector.Serialize(embedding)
		if _, err := tx.ExecContext(ctx, `
                        INSERT INTO records_vec(dataset, id, embedding) VALUES(?, ?, ?)
                        ON CONFLICT(dataset, id) DO UPDATE SET embedding=excluded.embedding;
                `, dataset, rec.ID, blob); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `DELETE FROM records_vec WHERE dataset = ? AND id = ?`, dataset, rec.ID); err != nil {
			return err
		}
	}

	return nil
}

func nullFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func formatFloat(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}
