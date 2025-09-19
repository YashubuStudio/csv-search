package database

import (
	"context"
	"database/sql"
	"fmt"
)

var schema = []string{
	"PRAGMA journal_mode=WAL;",
	`CREATE TABLE IF NOT EXISTS items (
                id TEXT PRIMARY KEY,
                title TEXT,
                body TEXT,
                tags TEXT,
                category TEXT,
                price REAL,
                stock INTEGER,
                created_at INTEGER,
                updated_at INTEGER,
                lat REAL,
                lng REAL,
                hash TEXT
        );`,
	`CREATE TABLE IF NOT EXISTS items_vec (
                id TEXT PRIMARY KEY,
                embedding BLOB NOT NULL
        );`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS items_fts USING fts5(
                id UNINDEXED,
                title,
                body,
                tags
        );`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS items_rtree USING rtree(
                item_rowid,
                min_lat,
                max_lat,
                min_lng,
                max_lng
        );`,
	`CREATE INDEX IF NOT EXISTS idx_items_category ON items(category);`,
	`CREATE INDEX IF NOT EXISTS idx_items_price ON items(price);`,
	`CREATE INDEX IF NOT EXISTS idx_items_stock ON items(stock);`,
	`CREATE INDEX IF NOT EXISTS idx_items_created_at ON items(created_at);`,
}

func applySchema(ctx context.Context, db *sql.DB, statements []string) error {
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply schema %q: %w", stmt, err)
		}
	}
	return nil
}
