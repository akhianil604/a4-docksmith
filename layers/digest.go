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
	// Use Stat instead of Lstat to follow symlinks and ensure we have the actual file info
	info, err := os.Stat(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to stat file %s: %w", filePath, err)
	}
	// Skip directories
	if info.IsDir() {
		return "", fmt.Errorf("path is a directory: %s", filePath)
	}

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
// Computes cache keys from concatenated strings.
func ComputeDataDigest(data []byte) string {
	hash := sha256.Sum256(data)
	return FormatDigest(hash[:])
}

// FormatDigest formats a raw SHA256 hash into the standard digest format.
// Input: raw bytes from a hash computation
// Output: "sha256:<hex_string>"
func FormatDigest(hashBytes []byte) string {
	return DigestPrefix + hex.EncodeToString(hashBytes)
}

// ParseDigest extracts the hex portion from a digest string.
// Input: "sha256:<hex_string>"
// Output: "<hex_string>", true if valid; "", false otherwise
func ParseDigest(digest string) (string, bool) {
	if !strings.HasPrefix(digest, DigestPrefix) {
		return "", false
	}
	hexPart := strings.TrimPrefix(digest, DigestPrefix)
	if len(hexPart) != 64 { // SHA256 produces 64 hex characters
		return "", false
	}
	return hexPart, true
}

// LayerFileName returns the filename for a layer given its digest.
// Input: "sha256:abc123..."
// Output: "sha256:abc123....tar"
func LayerFileName(digest string) string {
	return digest + LayerFileExtension
}

// LayerFilePath returns the full path to a layer file in the store.
// Input: digest "sha256:abc123...", storePath "/home/user/.docksmith/layers"
// Output: "/home/user/.docksmith/layers/sha256:abc123....tar"
func LayerFilePath(digest, storePath string) string {
	return filepath.Join(storePath, LayerFileName(digest))
}

// ValidateDigest checks if a digest string is well-formed.
// Returns an error if the digest is invalid.
func ValidateDigest(digest string) error {
	if !strings.HasPrefix(digest, DigestPrefix) {
		return fmt.Errorf("digest must start with %s", DigestPrefix)
	}
	hexPart := strings.TrimPrefix(digest, DigestPrefix)
	if len(hexPart) != 64 {
		return fmt.Errorf("digest hex part must be 64 characters, got %d", len(hexPart))
	}
	// Validate hex encoding
	if _, err := hex.DecodeString(hexPart); err != nil {
		return fmt.Errorf("digest hex part is not valid hex: %w", err)
	}
	return nil
}
