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