// Package cache persists the build-cache index to disk.
// The index stores cache-key -> layer-digest mappings in JSON.
package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

var CachePath = filepath.Join(os.Getenv("HOME"), ".docksmith", "cache", "index.json")

// LoadIndex loads the cache index from CachePath.
// If the file does not exist, it returns an empty index and no error.
func LoadIndex() (map[string]string, error) {
	if _, err := os.Stat(CachePath); os.IsNotExist(err) {
		return map[string]string{}, nil
	}

	data, err := os.ReadFile(CachePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read cache index: %w", err)
	}

	index := map[string]string{}
	if len(data) == 0 {
		return index, nil
	}

	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("failed to parse cache index: %w", err)
	}

	if index == nil {
		index = map[string]string{}
	}

	return index, nil
}

// SaveIndex writes the full cache index atomically by replacing the JSON file contents.
// The cache directory is created if it does not already exist.
func SaveIndex(index map[string]string) error {
	dir := filepath.Dir(CachePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	if index == nil {
		index = map[string]string{}
	}

	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize cache index: %w", err)
	}

	if err := os.WriteFile(CachePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write cache index: %w", err)
	}

	return nil
}
