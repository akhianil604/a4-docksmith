// Package layers implements the content-addressed layer store.
// The store is a flat directory of tar archives named by their SHA256 digest.
// All paths default to ~/.docksmith/layers but the caller passes storePath explicitly so the build engine and runtime can share the same store without global state.
package layers
import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultStorePath returns the canonical layer store path (~/.docksmith/layers).
// Call this once at startup and pass the result through to all layer functions.
func DefaultStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}
	return filepath.Join(home, ".docksmith", "layers"), nil
}

// EnsureStoreExists creates the store directory (and any parents) if absent.
// Idempotent — safe to call at every startup.
func EnsureStoreExists(storePath string) error {
	if err := os.MkdirAll(storePath, 0755); err != nil {
		return fmt.Errorf("failed to create layer store at %s: %w", storePath, err)
	}
	return nil
}

// LayerExists reports whether the layer identified by digest is present in the store directory.  
// Performs only a filesystem stat, does not verify the tar's contents match the digest.
// Usage by Engineer 3 (Cache Engineer):
//	if !layers.LayerExists(cachedDigest, storePath) {
//	    // cache miss — layer file was deleted out-of-band; treat as miss
//	}
func LayerExists(digest string, storePath string) bool {
	if err := ValidateDigest(digest); err != nil {
		return false
	}
	_, err := os.Stat(LayerFilePath(digest, storePath))
	return err == nil
}

// DeleteLayer removes the tar archive for digest from the store.
// Returns an error if the layer does not exist or the removal fails.
// This is called by `docksmith rmi`. Layers are not reference-counted.
// Deletion is unconditional even if another image references the same digest.
// That image will subsequently fail to run.
func DeleteLayer(digest string, storePath string) error {
	if err := ValidateDigest(digest); err != nil {
		return fmt.Errorf("invalid digest %q: %w", digest, err)
	}
	// Remove directly rather than Stat-then-Remove to avoid a TOCTOU window where another process deletes the file between the two calls.
	err := os.Remove(LayerFilePath(digest, storePath))
	if err == nil {
		return nil
	}
	if os.IsNotExist(err) {
		return fmt.Errorf("layer %s not found in store", digest)
	}
	return fmt.Errorf("failed to delete layer %s: %w", digest, err)
}

// ListLayers returns the digest of every layer currently in storePath.
// Entries that do not look like valid layer filenames are silently skipped.
// Order is not guaranteed, callers must sort if needed.
// Useful for Engineer 1 (CLI & Parser) when implementing `docksmith images` and for reconciling which layer files are referenced by any manifest.
func ListLayers(storePath string) ([]string, error) {
	entries, err := os.ReadDir(storePath)
	if os.IsNotExist(err) {
		// Store not yet initialised — that is fine, just return empty.
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read store directory %s: %w", storePath, err)
	}
	digests := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, LayerFileExtension) {
			continue
		}
		digest := strings.TrimSuffix(name, LayerFileExtension)
		if ValidateDigest(digest) != nil {
			continue // e.g. temp.tar left from a crashed build
		}
		digests = append(digests, digest)
	}

	return digests, nil
}

// GetLayerInfo returns stored metadata for an existing layer.
// The CreatedBy field is not encoded in the tar itself.
// The caller should populate it from the image manifest after calling this function.
func GetLayerInfo(digest, storePath string) (LayerMetadata, error) {
	if err := ValidateDigest(digest); err != nil {
		return LayerMetadata{}, fmt.Errorf("invalid digest %q: %w", digest, err)
	}
	path := LayerFilePath(digest, storePath)
	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LayerMetadata{}, fmt.Errorf("layer %s not found in store", digest)
		}
		return LayerMetadata{}, fmt.Errorf("failed to stat layer %s: %w", digest, err)
	}
	return LayerMetadata{
		Digest:    digest,
		Size:      stat.Size(),
		CreatedBy: "", // Retrieve from image manifest, not stored inside the tar
	}, nil
}