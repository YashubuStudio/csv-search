package database

import (
	"context"
	"database/sql"
	"fmt"
)

var schema = []string{
	"PRAGMA journal_mode=WAL;",
	`CREATE TABLE IF NOT EXISTS records (
                dataset TEXT NOT NULL,
                id TEXT NOT NULL,
                data TEXT NOT NULL,
                lat REAL,
                lng REAL,
                hash TEXT,
                PRIMARY KEY(dataset, id)
        );`,
	`CREATE TABLE IF NOT EXISTS records_vec (
                dataset TEXT NOT NULL,
                id TEXT NOT NULL,
                embedding BLOB NOT NULL,
                PRIMARY KEY(dataset, id),
                FOREIGN KEY(dataset, id) REFERENCES records(dataset, id) ON DELETE CASCADE
        );`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS records_fts USING fts5(
                dataset UNINDEXED,
                id UNINDEXED,
                content
        );`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS records_rtree USING rtree(
                rowid,
                min_lat,
                max_lat,
                min_lng,
                max_lng
        );`,
	`CREATE INDEX IF NOT EXISTS idx_records_dataset ON records(dataset);`,
}

func applySchema(ctx context.Context, db *sql.DB, statements []string) error {
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply schema %q: %w", stmt, err)
		}
	}
	return nil
}
