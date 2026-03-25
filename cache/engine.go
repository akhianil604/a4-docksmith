// Package cache provides an in-memory cache engine backed by the on-disk JSON index.
package cache

// Engine owns loaded cache entries and exposes lookup/store operations.
type Engine struct {
	// Index maps deterministic cache keys to content-addressed layer digests.
	Index map[string]string
	// NoCache disables cache reads and writes when true.
	NoCache bool
}

// NewEngine initializes a cache engine by loading the current on-disk index.
func NewEngine(noCache bool) (*Engine, error) {
	idx, err := LoadIndex()
	if err != nil {
		return nil, err
	}

	return &Engine{
		Index:   idx,
		NoCache: noCache,
	}, nil
}

// Lookup returns the digest for key and whether it exists.
// When NoCache is enabled, Lookup always reports a miss.
func (e *Engine) Lookup(key string) (string, bool) {
	if e == nil || e.NoCache {
		return "", false
	}

	val, ok := e.Index[key]
	return val, ok
}

// Store records key -> digest and persists the index to disk.
// When NoCache is enabled, Store is a no-op.
func (e *Engine) Store(key, digest string) error {
	if e == nil || e.NoCache {
		return nil
	}

	if e.Index == nil {
		e.Index = map[string]string{}
	}

	e.Index[key] = digest
	return SaveIndex(e.Index)
}
