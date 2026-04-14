// Package layers provides functionality for creating, storing, and extracting container image layers. 
// Layers are stored as deterministic tar archives with content-addressed filenames (SHA256 digest).
package layers
import (
	"time"
)
// LayerMetadata contains information about a single layer.
// This structure is returned when creating a layer and is typically stored in the image manifest.
type LayerMetadata struct {
	// Digest is the SHA256 hash of the layer's tar archive in the format "sha256:<hex>"
	Digest string
	// Size is the byte size of the tar archive on disk
	Size int64
	// CreatedBy is a human-readable string describing what created this layer
	// (e.g., "COPY . /app" or "RUN apt-get update")
	CreatedBy string
}
// Constants for deterministic tar creation
const (
	// LayerFileExtension is the file extension for layer tar archives
	LayerFileExtension = ".tar"
	// DigestPrefix is the prefix used for SHA256 digests
	DigestPrefix = "sha256:"
	// DeterministicMtime is the zeroed timestamp used for all files in layers to ensure reproducible builds (Unix epoch: 1970-01-01 00:00:00 UTC)
	DeterministicMtime = int64(0)
	// DeterministicUID is the normalized user ID for all files in layers
	DeterministicUID = 0
	// DeterministicGID is the normalized group ID for all files in layers
	DeterministicGID = 0
)
// ZeroTime returns the zero time used for deterministic tar creation
func ZeroTime() time.Time {
	return time.Unix(DeterministicMtime, 0)
}
// fileInfo is used internally to sort files lexicographically
// before adding them to the tar archive.
type fileInfo struct {
	// path is the relative path from the source directory, using forward slashes.
	path string
	// fullPath is the absolute OS path used to open the file.
	fullPath string
	// Note: whether the entry is a directory is determined at tar-write time via a fresh os.Lstat call rather than caching it here, so no isDir field is stored.
}
