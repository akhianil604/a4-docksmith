// Package layers implements deterministic tar layer creation.
package layers
import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CreateLayer creates a deterministic tar layer from a source directory.
// This function:
//  1. Walks the source directory and collects all files
//  2. Sorts files lexicographically for reproducibility
//  3. Creates a tar archive with normalized metadata (timestamps, ownership)
//  4. Computes the SHA256 digest of the tar
//  5. Stores the tar in the layer store with a content-addressed filename
//  6. Returns fully-populated layer metadata
// Parameters:
//   - sourceDir:  directory containing files that form this layer (the delta)
//   - storePath:  directory where layers are stored (~/.docksmith/layers)
//   - createdBy:  human-readable instruction string stored in the manifest
//     (e.g. "COPY . /app" or "RUN pip install -r requirements.txt")
// The function writes a randomly-named temp file, computes its digest, then
// renames it to the final content-addressed name (sha256:<digest>.tar).
// If the layer already exists in the store the temp file is discarded and
// the existing layer's metadata is returned unchanged (idempotent).
// Integration notes for teammates:
//   - sourceDir should contain only the files changed by this instruction
//   - Empty directories are preserved in the tar
//   - Symlinks are followed and stored as regular files
//   - The returned Digest is the stable identifier used in the image manifest
func CreateLayer(sourceDir string, storePath string, createdBy string) (LayerMetadata, error) {
	if sourceDir == "" {
		return LayerMetadata{}, fmt.Errorf("sourceDir cannot be empty")
	}
	if storePath == "" {
		return LayerMetadata{}, fmt.Errorf("storePath cannot be empty")
	}
	if _, err := os.Stat(sourceDir); err != nil {
		return LayerMetadata{}, fmt.Errorf("source directory does not exist: %w", err)
	}
	if err := os.MkdirAll(storePath, 0755); err != nil {
		return LayerMetadata{}, fmt.Errorf("failed to create store directory: %w", err)
	}
	// Step 1: Collect all files from the source directory.
	files, err := collectFiles(sourceDir)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("failed to collect files: %w", err)
	}
	// Step 2: Sort files lexicographically so the tar entry order is deterministic regardless of filesystem readdir ordering.
	sort.Slice(files, func(i, j int) bool {
		return files[i].path < files[j].path
	})
	// Step 3: Create a uniquely-named temp file in the store directory.
	// Using a random name (os.CreateTemp) prevents concurrent builds — or a previous interrupted build — from clobbering each other's work.
	// The file must live in storePath so the rename (Step 7) remains on the same filesystem and is therefore atomic.
	tmpFile, err := os.CreateTemp(storePath, "tmp-layer-*.tar")
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("failed to allocate temp file: %w", err)
	}
	// Close immediately; createDeterministicTar reopens the file by path.
	tempTarPath := tmpFile.Name()
	tmpFile.Close()
	// Step 4: Write the deterministic tar archive.
	if err := createDeterministicTar(files, tempTarPath); err != nil {
		os.Remove(tempTarPath)
		return LayerMetadata{}, fmt.Errorf("failed to create tar: %w", err)
	}
	// Step 5: Compute the digest of the completed tar.
	digest, err := ComputeTarDigest(tempTarPath)
	if err != nil {
		os.Remove(tempTarPath)
		return LayerMetadata{}, fmt.Errorf("failed to compute digest: %w", err)
	}
	// Step 6: If an identical layer already exists, discard the temp tar.
	// Equal digests mean byte-for-byte identical content — no data is lost.
	// This also enforces immutability: the on-disk file is never overwritten.
	finalPath := LayerFilePath(digest, storePath)
	if LayerExists(digest, storePath) {
		os.Remove(tempTarPath)
		existingStat, err := os.Stat(finalPath)
		if err != nil {
			return LayerMetadata{}, fmt.Errorf("layer exists but cannot stat %s: %w", finalPath, err)
		}
		return LayerMetadata{
			Digest:    digest,
			Size:      existingStat.Size(),
			CreatedBy: createdBy,
		}, nil
	}
	// Step 7: Capture size before the rename consumes the temp path.
	stat, err := os.Stat(tempTarPath)
	if err != nil {
		os.Remove(tempTarPath)
		return LayerMetadata{}, fmt.Errorf("failed to stat temp tar: %w", err)
	}
	size := stat.Size()
	// Step 8: Atomic rename to the content-addressed filename.
	// Because temp and final are on the same filesystem, this is a single syscall and cannot leave a partial file visible to other readers.
	if err := os.Rename(tempTarPath, finalPath); err != nil {
		os.Remove(tempTarPath)
		return LayerMetadata{}, fmt.Errorf("failed to move tar into store: %w", err)
	}
	return LayerMetadata{
		Digest:    digest,
		Size:      size,
		CreatedBy: createdBy,
	}, nil
}