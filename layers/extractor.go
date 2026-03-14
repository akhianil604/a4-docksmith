// Package layers implements tar layer extraction for container filesystem assembly.
// Layers must be extracted in manifest order (oldest → newest) so that later layers overwrite earlier ones at conflicting paths — 
// Similar to Docker-style union filesystems, emulated in Docksmith.
package layers
import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)
// ExtractLayer extracts the layer identified by digest into destination.
// Files already present at the destination are overwritten
// Call this in image-manifest layer order to assemble the full filesystem:
//	for _, layer := range manifest.Layers {
//	    if err := layers.ExtractLayer(layer.Digest, storePath, rootfs); err != nil {
//	        return err
//	    }
//	}
// Parameters:
//   - digest:      "sha256:<hex>" identifying the layer tar in the store
//   - storePath:   directory containing layer tar files (e.g. ~/.docksmith/layers)
//   - destination: root directory to extract into (created if absent)
// Security: every tar entry path is validated to remain within destination before any file is written (prevents Zip Slip / path traversal).
func ExtractLayer(digest string, storePath string, destination string) error {
	if err := ValidateDigest(digest); err != nil {
		return fmt.Errorf("invalid digest %q: %w", digest, err)
	}
	if destination == "" {
		return fmt.Errorf("destination cannot be empty")
	}
	// Open the layer tar.
	tarPath := LayerFilePath(digest, storePath)
	f, err := os.Open(tarPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("layer %s not found in store (path: %s)", digest, tarPath)
		}
		return fmt.Errorf("failed to open layer %s: %w", digest, err)
	}
	defer f.Close()
	// Create destination root if it does not yet exist.
	if err := os.MkdirAll(destination, 0755); err != nil {
		return fmt.Errorf("failed to create destination %s: %w", destination, err)
	}
	// Resolve to an absolute, clean path once so every entry check is fast.
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("failed to resolve destination path: %w", err)
	}
	tr := tar.NewReader(f)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading layer tar %s: %w", digest, err)
		}

		if err := extractEntry(tr, header, absDestination); err != nil {
			return fmt.Errorf("failed to extract entry %q from layer %s: %w",
				header.Name, digest, err)
		}
	}
	return nil
}