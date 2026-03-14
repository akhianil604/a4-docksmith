// Package layers provides digest computation for content-addressable storage
package layers
import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)
// ComputeFileDigest computes the SHA256 digest of a file's contents.
// Returns the digest in the format "sha256:<hex>".
// Used for computing cache keys based on source files.
func ComputeFileDigest(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file %s: %w", filePath, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("failed to hash file %s: %w", filePath, err)
	}
	return FormatDigest(hash.Sum(nil)), nil
}

// ComputeTarDigest computes the SHA256 digest of a tar archive's raw bytes.
// Returns the digest in the format "sha256:<hex>".
// Content-address used to name and locate layers.
func ComputeTarDigest(tarPath string) (string, error) {
	file, err := os.Open(tarPath)
	if err != nil {
		return "", fmt.Errorf("failed to open tar %s: %w", tarPath, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("failed to hash tar %s: %w", tarPath, err)
	}
	return FormatDigest(hash.Sum(nil)), nil
}

// ComputeDataDigest computes the SHA256 digest of arbitrary data.
// Returns the digest in the format "sha256:<hex>".
// Useful for computing cache keys from concatenated strings.
func ComputeDataDigest(data []byte) string {
	hash := sha256.Sum256(data)
	return FormatDigest(hash[:])
}