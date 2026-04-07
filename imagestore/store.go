package imagestore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"docksmith/layers"
)

const manifestFileExt = ".json"

// DefaultImagesPath returns ~/.docksmith/images.
func DefaultImagesPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}
	return filepath.Join(home, ".docksmith", "images"), nil
}

// EnsureImagesPath ensures the manifest store path exists.
func EnsureImagesPath(imagesPath string) error {
	if err := os.MkdirAll(imagesPath, 0755); err != nil {
		return fmt.Errorf("failed to create images store at %s: %w", imagesPath, err)
	}
	return nil
}

func manifestPath(imagesPath, name, tag string) string {
	namePath := filepath.FromSlash(name)
	return filepath.Join(imagesPath, namePath, tag+manifestFileExt)
}

// ComputeManifestDigest computes the canonical manifest digest.
// The digest field is emptied before serialization, per spec.
func ComputeManifestDigest(m Manifest) (string, error) {
	canonical := m
	canonical.Digest = ""
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("failed to serialize manifest for digest: %w", err)
	}
	h := sha256.Sum256(data)
	return layers.DigestPrefix + hex.EncodeToString(h[:]), nil
}

// SaveManifest writes a manifest to disk after computing canonical digest.
func SaveManifest(imagesPath string, m Manifest) (Manifest, error) {
	if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.Tag) == "" {
		return Manifest{}, fmt.Errorf("manifest name and tag are required")
	}
	if err := EnsureImagesPath(imagesPath); err != nil {
		return Manifest{}, err
	}
	path := manifestPath(imagesPath, m.Name, m.Tag)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return Manifest{}, fmt.Errorf("failed to create image directory: %w", err)
	}
	digest, err := ComputeManifestDigest(m)
	if err != nil {
		return Manifest{}, err
	}
	m.Digest = digest
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Manifest{}, fmt.Errorf("failed to encode manifest: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return Manifest{}, fmt.Errorf("failed to write manifest %s: %w", path, err)
	}
	return m, nil
}

// LoadManifest resolves and loads name:tag from the manifest store.
func LoadManifest(imagesPath, reference string) (Manifest, string, error) {
	name, tag, err := ParseReference(reference)
	if err != nil {
		return Manifest{}, "", err
	}
	path := manifestPath(imagesPath, name, tag)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Manifest{}, "", fmt.Errorf("image %s not found", reference)
		}
		return Manifest{}, "", fmt.Errorf("failed to read manifest %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, "", fmt.Errorf("failed to parse manifest %s: %w", path, err)
	}
	return m, path, nil
}

// DeleteManifest removes the manifest for a reference.
func DeleteManifest(imagesPath, reference string) error {
	name, tag, err := ParseReference(reference)
	if err != nil {
		return err
	}
	path := manifestPath(imagesPath, name, tag)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("image %s not found", reference)
		}
		return fmt.Errorf("failed to delete manifest %s: %w", path, err)
	}
	return nil
}

// ListManifests loads every manifest from the images store.
func ListManifests(imagesPath string) ([]Manifest, error) {
	if err := EnsureImagesPath(imagesPath); err != nil {
		return nil, err
	}
	var paths []string
	err := filepath.WalkDir(imagesPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) == manifestFileExt {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan images store: %w", err)
	}
	sort.Strings(paths)

	out := make([]Manifest, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read manifest %s: %w", path, err)
		}
		var m Manifest
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("failed to parse manifest %s: %w", path, err)
		}
		out = append(out, m)
	}
	return out, nil
}
