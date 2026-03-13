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

// collectFiles recursively walks sourceDir and returns an unsorted slice of fileInfo entries.  
// The caller sorts before creating the tar archive.
func collectFiles(sourceDir string) ([]fileInfo, error) {
	sourceDir = filepath.Clean(sourceDir)
	var files []fileInfo
	err := filepath.Walk(sourceDir, func(fullPath string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("error walking %s: %w", fullPath, err)
		}
		relPath, err := filepath.Rel(sourceDir, fullPath)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}
		// Skip the root directory itself.
		if relPath == "." {
			return nil
		}
		// Normalise to forward slashes so tar paths are identical on all platforms.
		files = append(files, fileInfo{
			path:     filepath.ToSlash(relPath),
			fullPath: fullPath,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// createDeterministicTar writes a tar archive from a pre-sorted slice of files.
// Every entry has normalised metadata (zeroed timestamps, uid/gid = 0, no user/group names).
// Mode bits are derived via tar.FileInfoHeader which correctly maps Go's os.FileMode to POSIX tar mode bits.
// Preserving setuid/setgid/sticky while stripping the Go-internal file-type flags that must not appear in the Mode field of a tar header.
func createDeterministicTar(files []fileInfo, tarPath string) error {
	tarFile, err := os.Create(tarPath)
	if err != nil {
		return fmt.Errorf("failed to create tar file: %w", err)
	}
	tw := tar.NewWriter(tarFile)
	for _, file := range files {
		if err := addFileToTar(tw, file); err != nil {
			tarFile.Close()
			return err
		}
	}
	// Close tar writer first — writes end-of-archive marker.
	if err := tw.Close(); err != nil {
		tarFile.Close()
		return fmt.Errorf("failed to finalise tar archive: %w", err)
	}
	// Close the underlying file explicitly so that any OS-level write error
	// (full disk, quota exceeded, etc.) surfaces here rather than being silently swallowed by a deferred close.  
	// A silent failure would store a corrupt tar and compute a wrong digest from it.
	if err := tarFile.Close(); err != nil {
		return fmt.Errorf("failed to flush tar to disk: %w", err)
	}
	return nil
}

// addFileToTar adds a single filesystem entry to tw with fully normalised metadata.
// Mode bit handling:
// tar.FileInfoHeader is used to convert os.FileMode → POSIX tar mode bits.
// This is the only correct portable approach: os.FileMode stores file-type flags 
// (e.g. os.ModeDir = 1<<31, os.ModeSymlink = 1<<27) in positions that are Go-internal and must not be written into a tar Mode field.  
// Casting info.Mode() directly to int64 embeds these flags and produces a different digest on different Go versions or platforms.  
// tar.FileInfoHeader performs the canonical conversion including setuid, setgid, and sticky bits.
// Symlink handling:
// Symlinks are followed and stored as regular files, avoiding dangling-link errors at extraction time and keeps the extractor simple.  
// The symlink target's content and mode are used; the symlink name in the tar is the original path.
func addFileToTar(tw *tar.Writer, file fileInfo) error {
	info, err := os.Lstat(file.fullPath)
	if err != nil {
		return fmt.Errorf("failed to stat %s: %w", file.path, err)
	}
	// Resolve symlinks: follow the link, then use the target's metadata.
	// src is the path actually opened for content.
	src := file.fullPath
	if info.Mode()&os.ModeSymlink != 0 {
		realPath, err := filepath.EvalSymlinks(file.fullPath)
		if err != nil {
			return fmt.Errorf("failed to follow symlink %s: %w", file.path, err)
		}
		// Re-stat the real target so tar.FileInfoHeader gets the correct size and mode.
		info, err = os.Stat(realPath)
		if err != nil {
			return fmt.Errorf("failed to stat symlink target %s: %w", file.path, err)
		}
		src = realPath
	}
	// Build the header using the standard library function for correct mode mapping.
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return fmt.Errorf("failed to create tar header for %s: %w", file.path, err)
	}
	// Normalise all non-deterministic fields.
	header.Name    = file.path
	header.ModTime = ZeroTime()
	header.Uid     = DeterministicUID
	header.Gid     = DeterministicGID
	header.Uname   = "" // omit username string — varies per machine
	header.Gname   = "" // omit group name string — varies per machine
	// Directories must end with "/" in tar archives.
	if info.IsDir() {
		header.Name = ensureTrailingSlash(header.Name)
	}
	// We followed symlinks above, so Typeflag may still be TypeSymlink from tar.FileInfoHeader using the original unsymlinked info.  Force TypeReg.
	if header.Typeflag == tar.TypeSymlink {
		header.Typeflag = tar.TypeReg
		header.Linkname = ""
	}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("failed to write tar header for %s: %w", file.path, err)
	}
	// Write file content for regular files (and followed symlinks).
	if !info.IsDir() {
		// Use f (not file) to avoid shadowing the fileInfo parameter.
		f, err := os.Open(src)
		if err != nil {
			return fmt.Errorf("failed to open %s for reading: %w", file.path, err)
		}
		defer f.Close()

		if _, err := io.Copy(tw, f); err != nil {
			return fmt.Errorf("failed to write content of %s: %w", file.path, err)
		}
	}

	return nil
}

// ensureTrailingSlash ensures directory names in tar archives end with "/".
func ensureTrailingSlash(path string) string {
	if !strings.HasSuffix(path, "/") {
		return path + "/"
	}
	return path
}