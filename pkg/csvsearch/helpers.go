package csvsearch

import (
	"os"
	"strings"

	"yashubustudio/csv-search/internal/config"
)

func loadConfig(path string, required bool) (*config.Config, error) {
	normalized := strings.TrimSpace(path)
	if normalized == "" {
		normalized = "csv-search_config.json"
	}
	cfg, err := config.Load(normalized)
	if err != nil {
		if os.IsNotExist(err) && !required {
			return nil, nil
		}
		return nil, err
	}
	return cfg, nil
}

func configDatabasePath(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.ResolvePath(cfg.Database.Path)
}

func cfgSearchTopK(cfg *config.Config) int {
	if cfg == nil {
		return 0
	}
	return cfg.Search.DefaultTopK
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func cloneStrings(src []string) []string {
	if len(src) == 0 {
		return nil
	}
	out := make([]string, len(src))
	copy(out, src)
	return out
}

func resolveDataset(cfg *config.Config, name string) (string, config.DatasetConfig, bool) {
	datasetName := strings.TrimSpace(name)
	if datasetName == "" && cfg != nil && cfg.DefaultDataset != "" {
		datasetName = cfg.DefaultDataset
	}
	if cfg != nil && datasetName != "" {
		if ds, ok := cfg.Dataset(datasetName); ok {
			return datasetName, ds, true
		}
	}
	return datasetName, config.DatasetConfig{}, false
}

func resolveTable(datasetName string, dataset config.DatasetConfig, override string) string {
	return firstNonEmpty(strings.TrimSpace(override), dataset.Table, datasetName, "default")
}
