// Package cache provides build-cache key computation and persistent key→layer-digest mapping.
// It integrates with the layers package by reusing layer digests as cache values rather than
// duplicating tar or layer hashing logic.
package cache

// BuildState captures the build context that affects cache validity for a single instruction.
// If any field changes between builds, ComputeCacheKey should produce a different key.
type BuildState struct {
	// PrevLayerDigest is the digest of the layer produced by the previous instruction.
	// Including this enforces Docker-like cache chaining across instruction order.
	PrevLayerDigest string
	// WorkDir is the effective working directory at this instruction.
	WorkDir string
	// Env is the effective environment visible to this instruction.
	// The map is serialized deterministically by sorting keys.
	Env map[string]string
}

// Instruction is the minimum instruction data needed to compute a cache key.
type Instruction struct {
	// Type is the normalized instruction kind (e.g. COPY, RUN, ENV).
	Type string
	// Raw is the original instruction text used as part of the cache fingerprint.
	Raw string
}
