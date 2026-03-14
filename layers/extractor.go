// Package layers implements tar layer extraction for container filesystem assembly.
// Layers must be extracted in manifest order (oldest to newest) so that later layers overwrite earlier ones at conflicting paths
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

// extractEntry dispatches a single tar entry to the appropriate handler.
func extractEntry(tr *tar.Reader, header *tar.Header, absDestination string) error {
	// Compute and validate the target path before touching the filesystem.
	targetPath, err := safeJoin(absDestination, header.Name)
	if err != nil {
		return err
	}
	switch header.Typeflag {
	case tar.TypeDir:
		return extractDir(targetPath, header)
	case tar.TypeReg, tar.TypeRegA:
		return extractRegularFile(tr, targetPath, header)
	case tar.TypeSymlink:
		return extractSymlink(targetPath, header)
	case tar.TypeLink:
		return extractHardLink(targetPath, header, absDestination)
	default:
		// Device files, FIFOs, etc. are out of scope for Docksmith layers.
		// Skipping them avoids errors on base images that may include such entries.
		return nil
	}
}

// safeJoin resolves name relative to absDestination and returns the absolute target path. 
// Rejects any name that would resolve outside absDestination after cleaning (Zip Slip / path traversal protection).
func safeJoin(absDestination, name string) (string, error) {
	// filepath.Join("/", name) collapses any leading "../" sequences that try to escape the root. the result is then appended under absDestination.
	cleanedName := filepath.Clean(filepath.Join("/", name))
	target := filepath.Join(absDestination, cleanedName)
	// Final guard: the resolved path must be rooted within absDestination.
	// We check for the separator suffix to prevent a path like /tmp/rootfs-other from matching prefix /tmp/rootfs.
	prefix := absDestination + string(filepath.Separator)
	if target != absDestination && !strings.HasPrefix(target, prefix) {
		return "", fmt.Errorf("path traversal rejected: %q resolves outside destination", name)
	}
	return target, nil
}

// extractDir creates a directory, preserving the mode recorded in the header.
// MkdirAll is used so that implicit parent directories are created as needed.
func extractDir(targetPath string, header *tar.Header) error {
	if err := os.MkdirAll(targetPath, header.FileInfo().Mode()); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", targetPath, err)
	}
	return nil
}