package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Config represents application level settings loaded from a JSON file.
type Config struct {
	Database       DatabaseConfig           `json:"database"`
	Embedding      EmbeddingConfig          `json:"embedding"`
	DefaultDataset string                   `json:"default_dataset"`
	Datasets       map[string]DatasetConfig `json:"datasets"`
	Search         SearchConfig             `json:"search"`

	baseDir string
}

// DatabaseConfig controls the SQLite database target.
type DatabaseConfig struct {
	Path string `json:"path"`
}

// EmbeddingConfig provides the ONNX runtime and encoder assets.
type EmbeddingConfig struct {
	OrtLib    string `json:"ort_lib"`
	Model     string `json:"model"`
	Tokenizer string `json:"tokenizer"`
	MaxSeqLen int    `json:"max_seq_len"`
}

// DatasetConfig configures ingestion defaults for a named dataset/table.
type DatasetConfig struct {
	Table       string   `json:"table"`
	CSV         string   `json:"csv"`
	BatchSize   int      `json:"batch_size"`
	IDColumn    string   `json:"id_column"`
	TextColumns []string `json:"text_columns"`
	MetaColumns []string `json:"meta_columns"`
	LatColumn   string   `json:"lat_column"`
	LngColumn   string   `json:"lng_column"`
}

// SearchConfig covers defaults for query behaviour.
type SearchConfig struct {
	DefaultTopK int `json:"default_topk"`
}

// Load reads a JSON configuration file from disk and validates its structure.
func Load(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()

	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		if err == io.EOF {
			return &cfg, nil
		}
		return nil, fmt.Errorf("decode config: %w", err)
	}

	// Ensure there is no trailing data in the config file.
	if err := ensureEOF(decoder); err != nil {
		return nil, err
	}

	cfg.baseDir = filepath.Dir(path)
	return &cfg, nil
}

// Dataset retrieves the dataset configuration by name.
func (cfg *Config) Dataset(name string) (DatasetConfig, bool) {
	if cfg == nil {
		return DatasetConfig{}, false
	}
	ds, ok := cfg.Datasets[name]
	return ds, ok
}

// ResolvePath converts a potentially relative path into an absolute one using
// the config file's directory as the base.
func (cfg *Config) ResolvePath(value string) string {
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) || cfg == nil || cfg.baseDir == "" {
		return value
	}
	return filepath.Clean(filepath.Join(cfg.baseDir, value))
}

func ensureEOF(decoder *json.Decoder) error {
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode config: unexpected trailing data")
		}
		return fmt.Errorf("decode config: %w", err)
	}
	return nil
}
