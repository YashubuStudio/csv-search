package csvsearch

import (
	"context"
	"fmt"
	"strings"

	"yashubustudio/csv-search/internal/ingest"
)

// IngestOptions configure CSV ingestion for a logical dataset.
type IngestOptions struct {
	Dataset         string
	Table           string
	CSVPath         string
	BatchSize       int
	IDColumn        string
	TextColumns     []string
	MetadataColumns []string
	LatitudeColumn  string
	LongitudeColumn string
}

// IngestSummary describes the resolved ingestion parameters that were applied.
type IngestSummary struct {
	Dataset         string
	Table           string
	CSVPath         string
	BatchSize       int
	IDColumn        string
	TextColumns     []string
	MetadataColumns []string
	LatitudeColumn  string
	LongitudeColumn string
}

// Ingest reads a CSV file, generates embeddings and upserts records into the
// SQLite database managed by the Service.
func (s *Service) Ingest(ctx context.Context, opts IngestOptions) (IngestSummary, error) {
	if ctx == nil {
		return IngestSummary{}, fmt.Errorf("context must not be nil")
	}
	if s.db == nil {
		return IngestSummary{}, fmt.Errorf("database handle is nil")
	}

	datasetName, dataset, hasDataset := resolveDataset(s.cfg, opts.Dataset)
	table := resolveTable(datasetName, dataset, opts.Table)

	csvPath := strings.TrimSpace(opts.CSVPath)
	if csvPath == "" && hasDataset {
		csvPath = dataset.CSV
	}
	if s.cfg != nil {
		csvPath = s.cfg.ResolvePath(csvPath)
	}
	if csvPath == "" {
		return IngestSummary{}, fmt.Errorf("csv path is required")
	}

	batchSize := firstPositive(opts.BatchSize, dataset.BatchSize, 1000)
	identifier := firstNonEmpty(strings.TrimSpace(opts.IDColumn), dataset.IDColumn, "id")

	textCols := cloneStrings(opts.TextColumns)
	if len(textCols) == 0 && hasDataset && len(dataset.TextColumns) > 0 {
		textCols = cloneStrings(dataset.TextColumns)
	}

	metaCols := cloneStrings(opts.MetadataColumns)
	if len(metaCols) == 0 {
		if hasDataset && len(dataset.MetaColumns) > 0 {
			metaCols = cloneStrings(dataset.MetaColumns)
		} else {
			metaCols = []string{"*"}
		}
	}

	latitude := firstNonEmpty(strings.TrimSpace(opts.LatitudeColumn), dataset.LatColumn)
	longitude := firstNonEmpty(strings.TrimSpace(opts.LongitudeColumn), dataset.LngColumn)

	if err := s.ensureDatabase(ctx); err != nil {
		return IngestSummary{}, err
	}

	enc, err := s.ensureEncoder()
	if err != nil {
		return IngestSummary{}, err
	}

	ingestOpts := ingest.Options{
		CSVPath:   csvPath,
		BatchSize: batchSize,
		Dataset:   table,
		Columns: ingest.ColumnConfig{
			ID:       identifier,
			Text:     textCols,
			Metadata: metaCols,
			Lat:      latitude,
			Lng:      longitude,
		},
	}

	if err := ingest.Run(ctx, s.db, enc, ingestOpts); err != nil {
		return IngestSummary{}, err
	}

	summary := IngestSummary{
		Dataset:         datasetName,
		Table:           table,
		CSVPath:         csvPath,
		BatchSize:       batchSize,
		IDColumn:        identifier,
		TextColumns:     cloneStrings(textCols),
		MetadataColumns: cloneStrings(metaCols),
		LatitudeColumn:  latitude,
		LongitudeColumn: longitude,
	}

	return summary, nil
}
