package csvsearch

import (
	"context"
	"fmt"
	"time"

	"yashubustudio/csv-search/internal/database"
)

// InitDatabaseOptions control database schema initialization.
type InitDatabaseOptions struct {
	Timeout time.Duration
}

// InitDatabase ensures that the SQLite schema exists. The method respects the
// provided timeout (defaulting to 10 seconds) and can be called multiple times.
func (s *Service) InitDatabase(ctx context.Context, opts InitDatabaseOptions) error {
	if ctx == nil {
		return fmt.Errorf("context must not be nil")
	}
	if s.db == nil {
		return fmt.Errorf("database handle is nil")
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	initCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := database.Init(initCtx, s.db); err != nil {
		return err
	}
	s.setDatabaseReady(true)
	return nil
}

func (s *Service) ensureDatabase(ctx context.Context) error {
	if s.databaseReady() {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.InitDatabase(ctx, InitDatabaseOptions{})
}
