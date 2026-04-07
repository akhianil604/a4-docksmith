package imagestore

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ParseReference parses a required name:tag reference.
func ParseReference(ref string) (string, string, error) {
	name, tag, found := strings.Cut(strings.TrimSpace(ref), ":")
	if !found || strings.TrimSpace(name) == "" || strings.TrimSpace(tag) == "" {
		return "", "", fmt.Errorf("invalid reference %q: expected name:tag", ref)
	}
	if strings.Contains(name, "..") || strings.Contains(tag, "..") {
		return "", "", fmt.Errorf("invalid reference %q: path traversal is not allowed", ref)
	}
	if strings.ContainsRune(tag, filepath.Separator) {
		return "", "", fmt.Errorf("invalid reference %q: tag cannot contain path separators", ref)
	}

	return name, tag, nil
}
