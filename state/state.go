package state

import (
	"fmt"
	"os"
	"path/filepath"
)

type Paths struct {
	RootDir   string
	ImagesDir string
	LayersDir string
	CacheDir  string
}

func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home directory: %w", err)
	}
	root := filepath.Join(home, ".docksmith")
	return Paths{
		RootDir:   root,
		ImagesDir: filepath.Join(root, "images"),
		LayersDir: filepath.Join(root, "layers"),
		CacheDir:  filepath.Join(root, "cache"),
	}, nil
}

func Ensure(paths Paths) error {
	dirs := []string{paths.RootDir, paths.ImagesDir, paths.LayersDir, paths.CacheDir}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create state directory %s: %w", dir, err)
		}
	}
	return nil
}
