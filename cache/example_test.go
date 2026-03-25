// example_test.go demonstrates how the cache package API is used by the Docksmith build engine.
// These are compilable Go example functions executed by `go test`.
// HOW TO RUN: go test ./cache -v -run Example
package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExampleComputeCacheKey_copyDeterministic shows that COPY cache keys are deterministic
// for identical inputs, including when COPY source path order differs.
func ExampleComputeCacheKey_copyDeterministic() {
	dir, err := os.MkdirTemp("", "docksmith-cache-copy-det-*")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer os.RemoveAll(dir)

	f1 := filepath.Join(dir, "a.txt")
	f2 := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(f1, []byte("alpha\n"), 0644); err != nil {
		fmt.Println("error:", err)
		return
	}
	if err := os.WriteFile(f2, []byte("beta\n"), 0644); err != nil {
		fmt.Println("error:", err)
		return
	}

	instr := Instruction{Type: "COPY", Raw: "COPY . /app"}
	state := BuildState{
		PrevLayerDigest: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		WorkDir:         "/app",
		Env:             map[string]string{"B": "2", "A": "1"},
	}

	k1, err := ComputeCacheKey(instr, state, []string{f1, f2})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	k2, err := ComputeCacheKey(instr, state, []string{f2, f1})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println("same key:", k1 == k2)
	fmt.Println("has sha256 prefix:", strings.HasPrefix(k1, "sha256:"))
	// Output:
	// same key: true
	// has sha256 prefix: true
}

// ExampleComputeCacheKey_copyContentChange shows that changing a COPY source file
// invalidates the key for that instruction.
func ExampleComputeCacheKey_copyContentChange() {
	dir, err := os.MkdirTemp("", "docksmith-cache-copy-change-*")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer os.RemoveAll(dir)

	f := filepath.Join(dir, "app.py")
	if err := os.WriteFile(f, []byte("print('v1')\n"), 0644); err != nil {
		fmt.Println("error:", err)
		return
	}

	instr := Instruction{Type: "COPY", Raw: "COPY app.py /app/"}
	state := BuildState{
		PrevLayerDigest: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		WorkDir:         "/app",
		Env:             map[string]string{"APP_ENV": "prod"},
	}

	k1, err := ComputeCacheKey(instr, state, []string{f})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	if err := os.WriteFile(f, []byte("print('v2')\n"), 0644); err != nil {
		fmt.Println("error:", err)
		return
	}

	k2, err := ComputeCacheKey(instr, state, []string{f})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println("key changed:", k1 != k2)
	// Output: key changed: true
}

// ExampleEngine_storeLookup shows storing a key->digest mapping and reading it back.
func ExampleEngine_storeLookup() {
	tmp, err := os.MkdirTemp("", "docksmith-cache-index-*")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer os.RemoveAll(tmp)

	oldPath := CachePath
	CachePath = filepath.Join(tmp, "index.json")
	defer func() { CachePath = oldPath }()

	eng, err := NewEngine(false)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	key := "sha256:key-1"
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := eng.Store(key, digest); err != nil {
		fmt.Println("error:", err)
		return
	}

	got, ok := eng.Lookup(key)
	fmt.Println("lookup hit:", ok)
	fmt.Println("digest matches:", got == digest)
	// Output:
	// lookup hit: true
	// digest matches: true
}

// ExampleEngine_noCacheMode shows that NoCache disables reads and writes.
func ExampleEngine_noCacheMode() {
	tmp, err := os.MkdirTemp("", "docksmith-cache-nocache-*")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer os.RemoveAll(tmp)

	oldPath := CachePath
	CachePath = filepath.Join(tmp, "index.json")
	defer func() { CachePath = oldPath }()

	eng, err := NewEngine(true)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	_ = eng.Store("sha256:key-2", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	_, ok := eng.Lookup("sha256:key-2")
	fmt.Println("lookup hit in no-cache mode:", ok)
	// Output: lookup hit in no-cache mode: false
}

// ExampleCascadeMissSimulation demonstrates 4-step cascade behavior.
// Once a miss occurs at any step, all following steps are treated as misses.
func ExampleCascadeMissSimulation() {
	steps := []string{"S1", "S2", "S3", "S4"}
	// S4 exists in index, but should still be MISS once S3 misses.
	index := map[string]string{
		"S1": "sha256:d1",
		"S2": "sha256:d2",
		"S4": "sha256:d4",
	}

	results := make([]string, 0, len(steps))
	cascadeMiss := false
	for _, step := range steps {
		if !cascadeMiss {
			if _, ok := index[step]; ok {
				results = append(results, "HIT")
				continue
			}
		}
		results = append(results, "MISS")
		cascadeMiss = true
	}

	fmt.Println(strings.Join(results, " "))
	// Output: HIT HIT MISS MISS
}
