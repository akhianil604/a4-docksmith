// Package cache computes deterministic cache keys for build instructions.
// The resulting key is mapped to a layer digest produced by the layers package.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"docksmith/layers"
)

func serializeEnv(env map[string]string) string {
	// Deterministic serialization is required because Go map iteration order is random.
	if len(env) == 0 {
		return ""
	}

	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+env[k])
	}

	return strings.Join(parts, "|")
}

// sha256String returns a SHA256 digest string formatted as "sha256:<hex>".
// This is used for cache keys (not layer tar digests).
func sha256String(input string) string {
	hash := sha256.Sum256([]byte(input))
	return "sha256:" + hex.EncodeToString(hash[:])
}

// hashFile computes a digest for a source file by delegating to layers.ComputeFileDigest.
// Reuse avoids duplicating file-digest behavior across packages.
func hashFile(path string) (string, error) {
	return layers.ComputeFileDigest(path)
}

// hashCopySources computes a deterministic fingerprint for COPY inputs.
// Paths are sorted first so equivalent sets produce identical hashes regardless of caller order.
func hashCopySources(paths []string) (string, error) {
	sorted := make([]string, len(paths))
	copy(sorted, paths)
	sort.Strings(sorted)

	var builder strings.Builder
	for _, p := range sorted {
		h, err := hashFile(p)
		if err != nil {
			return "", err
		}
		builder.WriteString(h)
	}

	return builder.String(), nil
}

// ComputeCacheKey returns a deterministic cache key for one instruction.
// Key material includes prior layer digest, raw instruction text, workdir, and env.
// For COPY instructions, it also includes a combined digest of source file contents.
//
// Parameters:
//   - instr:     current instruction metadata
//   - state:     current build state (prev layer, workdir, env)
//   - copyFiles: resolved source files used by COPY (ignored for non-COPY)
//
// Integration note:
// The caller should pass COPY paths resolved with the same logic used by layer creation,
// so cache keys remain aligned with the layer delta that is actually built.
func ComputeCacheKey(instr Instruction, state BuildState, copyFiles []string) (string, error) {
	parts := make([]string, 0, 5)

	parts = append(parts, state.PrevLayerDigest)
	parts = append(parts, strings.TrimSpace(instr.Raw))
	parts = append(parts, state.WorkDir)
	parts = append(parts, serializeEnv(state.Env))

	if strings.EqualFold(strings.TrimSpace(instr.Type), "COPY") {
		hash, err := hashCopySources(copyFiles)
		if err != nil {
			return "", err
		}
		parts = append(parts, hash)
	}

	final := strings.Join(parts, "||")
	return sha256String(final), nil
}
