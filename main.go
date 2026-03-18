// main.go is a self-contained integration test for the docksmith layer system.
// Creates real files, calls every exported API function, and verifies results.
// Run from the docksmith/ directory:
// go run main.go
package main

import (
	"bytes"
	"docksmith/layers"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Output helpers
const (
	lineThin = "─────────────────────────────────────────────────────────────────────────"
	lineBold = "═════════════════════════════════════════════════════════════════════════"
)

func header(n, total int, title string) {
	fmt.Printf("\n%s\n[%d/%d] %s\n%s\n", lineThin, n, total, title, lineThin)
}
func section(title string) {
	fmt.Printf("\n%s\n[+] %s\n%s\n", lineThin, title, lineThin)
}

// Test tracker
type tracker struct {
	passed int
	failed int
}

// Pass records a named pass and prints it.
func (t *tracker) pass(name string) {
	fmt.Printf("  [PASS] %s\n", name)
	t.passed++
}

// fail records a named failure, printing the reason.
func (t *tracker) fail(name, reason string) {
	fmt.Printf("  [FAIL] %s: %s\n", name, reason)
	t.failed++
}

// Check passes if err == nil, otherwise fails with the error message.
// Returns true on pass so callers can gate subsequent checks.
func (t *tracker) check(name string, err error) bool {
	if err != nil {
		t.fail(name, err.Error())
		return false
	}
	t.pass(name)
	return true
}

// Expect passes if cond is true, otherwise fails with reason.
func (t *tracker) expect(name string, cond bool, reason string) {
	if cond {
		t.pass(name)
	} else {
		t.fail(name, reason)
	}
}

// Entry point
func main() {
	t := &tracker{}
	fmt.Printf("\n%s\n", lineBold)
	fmt.Println("Docksmith Layer System — Integration Test")
	fmt.Printf("%s\n", lineBold)
	// Setup: temp directories
	// A dedicated temp store keeps this test from polluting ~/.docksmith/layers.
	fmt.Println("\n[Setup]")
	storePath := tempDir("docksmith-store-*")
	defer os.RemoveAll(storePath)
	fmt.Printf("  store:    %s\n", storePath)
	fmt.Printf("  cleanup:  deferred on exit\n")

	// Step 1: Build the source directory
	header(1, 7, "Build source directory")
	sourceDir := tempDir("docksmith-src-*")
	defer os.RemoveAll(sourceDir)
	// sourceFiles is the delta that will become the layer.
	// Path keys use forward slashes, the helper converts for the OS.
	sourceFiles := []struct {
		rel  string
		data []byte
	}{
		{"README.md", []byte("# Docksmith Demo App\n")},
		{"app/main.py", []byte("print(\"hello from docksmith\")\n")},
		{"app/config.json", []byte("{\"version\": \"1.0\", \"name\": \"demo\"}\n")},
		{"app/lib/utils.py", []byte("def greet(): return \"hello\"\n")},
	}
	for _, f := range sourceFiles {
		writeFile(sourceDir, f.rel, f.data)
		fmt.Printf("  %-28s %d bytes\n", f.rel, len(f.data))
	}

	// Step 2: CreateLayer
	header(2, 7, "CreateLayer")
	meta, err := layers.CreateLayer(sourceDir, storePath, "COPY . /app")
	if !t.check("CreateLayer returned no error", err) {
		fatal("cannot continue without a valid layer: %v", err)
	}
	fmt.Printf("\n  digest:  %s\n", meta.Digest)
	fmt.Printf("  size:    %d bytes\n", meta.Size)
	fmt.Printf("  tarFile: %s\n", layers.LayerFilePath(meta.Digest, storePath))

	// Step 3: Layer in store
	header(3, 7, "Layer in store")
	t.expect(
		"LayerExists → true after CreateLayer",
		layers.LayerExists(meta.Digest, storePath),
		"expected LayerExists to return true immediately after CreateLayer",
	)

	// Step 4: ExtractLayer
	header(4, 7, "ExtractLayer")
	destDir := tempDir("docksmith-dest-*")
	defer os.RemoveAll(destDir)
	fmt.Printf("  destination: %s\n\n", destDir)
	t.check("ExtractLayer returned no error",
		layers.ExtractLayer(meta.Digest, storePath, destDir))

	// Step 5: Verify extracted files
	header(5, 7, "Verify extracted files")
	for _, f := range sourceFiles {
		got, err := os.ReadFile(filepath.Join(destDir, filepath.FromSlash(f.rel)))
		if err != nil {
			t.fail(f.rel+" readable", err.Error())
			continue
		}
		t.expect(
			f.rel+" content matches",
			bytes.Equal(got, f.data),
			fmt.Sprintf("got %q, want %q", got, f.data),
		)
	}
	// Verify subdirectory was recreated.
	libInfo, err := os.Stat(filepath.Join(destDir, "app", "lib"))
	t.expect(
		"app/lib/ directory exists",
		err == nil && libInfo.IsDir(),
		"directory missing after extraction",
	)

	// Step 6: Determinism & immutability
	header(6, 7, "Determinism & immutability")
	// Capture the layer file's mtime before the second CreateLayer call.
	layerPath := layers.LayerFilePath(meta.Digest, storePath)
	beforeStat, err := os.Stat(layerPath)
	if err != nil {
		fatal("cannot stat layer file: %v", err)
	}
	// Second call with identical source content.
	meta2, err := layers.CreateLayer(sourceDir, storePath, "COPY . /app")
	if t.check("second CreateLayer returned no error", err) {
		t.expect(
			"same content → same digest  (determinism)",
			meta.Digest == meta2.Digest,
			fmt.Sprintf("digest changed: first=%s  second=%s", meta.Digest, meta2.Digest),
		)
		// If digests are equal the file must not have been touched (immutability).
		afterStat, _ := os.Stat(layerPath)
		t.expect(
			"layer file not overwritten on second CreateLayer  (immutability)",
			!afterStat.ModTime().After(beforeStat.ModTime()),
			fmt.Sprintf("mtime moved from %s → %s",
				beforeStat.ModTime().Format(time.RFC3339Nano),
				afterStat.ModTime().Format(time.RFC3339Nano)),
		)
	}

	// Step 7: Layer stacking — later layer overwrites earlier
	header(7, 7, "Layer stacking (overwrite semantics)")
	// Build a second source directory that contains only a modified README.md.
	src2Dir := tempDir("docksmith-src2-*")
	defer os.RemoveAll(src2Dir)
	updatedReadme := []byte("# Version 2 — written by the second layer\n")
	writeFile(src2Dir, "README.md", updatedReadme)
	metaV2, err := layers.CreateLayer(src2Dir, storePath, "COPY README.md /")
	if !t.check("CreateLayer (layer 2) returned no error", err) {
		fatal("cannot run stacking test without second layer")
	}
	stackDir := tempDir("docksmith-stack-*")
	defer os.RemoveAll(stackDir)
	// Extract layer 1 (base), then layer 2 (delta) — same order as runtime.
	if err := layers.ExtractLayer(meta.Digest, storePath, stackDir); err != nil {
		fatal("extract layer 1 into stack: %v", err)
	}
	if err := layers.ExtractLayer(metaV2.Digest, storePath, stackDir); err != nil {
		fatal("extract layer 2 into stack: %v", err)
	}
	// README.md must reflect the second layer.
	got, err := os.ReadFile(filepath.Join(stackDir, "README.md"))
	if t.check("README.md readable in stacked rootfs", err) {
		t.expect(
			"second layer's README.md wins over first layer's",
			bytes.Equal(got, updatedReadme),
			fmt.Sprintf("got %q, want %q", got, updatedReadme),
		)
	}
	// app/main.py from layer 1 must still be present (layer 2 did not delete it).
	_, err = os.Stat(filepath.Join(stackDir, "app", "main.py"))
	t.expect(
		"layer 1 files preserved after stacking layer 2",
		err == nil,
		"app/main.py missing — layer 2 incorrectly wiped layer 1 files",
	)

	// ListLayers & DeleteLayer
	section("ListLayers & DeleteLayer")
	// At this point the store contains: meta.Digest and metaV2.Digest.
	all, err := layers.ListLayers(storePath)
	if t.check("ListLayers returned no error", err) {
		t.expect(
			"ListLayers reports 2 layers",
			len(all) == 2,
			fmt.Sprintf("got %d layers, want 2", len(all)),
		)
	}
	// Delete the first layer.
	t.check("DeleteLayer (layer 1) returned no error",
		layers.DeleteLayer(meta.Digest, storePath))
	t.expect(
		"LayerExists → false after DeleteLayer",
		!layers.LayerExists(meta.Digest, storePath),
		"LayerExists still true after DeleteLayer",
	)
	t.expect(
		"layer 2 still present after deleting layer 1",
		layers.LayerExists(metaV2.Digest, storePath),
		"second layer unexpectedly missing",
	)
	remaining, err := layers.ListLayers(storePath)
	if t.check("ListLayers after delete returned no error", err) {
		t.expect(
			"ListLayers reports 1 layer after delete",
			len(remaining) == 1,
			fmt.Sprintf("got %d layers, want 1", len(remaining)),
		)
	}

	// Results
	fmt.Printf("\n%s\n", lineBold)
	if t.failed == 0 {
		fmt.Printf(
			"RESULT  %d passed  %d failed — ALL TESTS PASSED\n",
			t.passed, t.failed,
		)
	} else {
		fmt.Printf(
			"RESULT  %d passed  %d failed — SOME TESTS FAILED\n",
			t.passed, t.failed,
		)
	}
	fmt.Printf("%s\n", lineBold)
	if t.failed > 0 {
		os.Exit(1)
	}
}

// Helpers
// tempDir creates a temporary directory and terminates the program if it fails.
func tempDir(pattern string) string {
	dir, err := os.MkdirTemp("", pattern)
	if err != nil {
		fatal("os.MkdirTemp(%q): %v", pattern, err)
	}
	return dir
}

// writeFile writes data to rel (a forward-slash path) inside parent.
// Creates any missing parent directories and terminates on any error.
func writeFile(parent, rel string, data []byte) {
	full := filepath.Join(parent, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		fatal("MkdirAll for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, data, 0644); err != nil {
		fatal("WriteFile %s: %v", rel, err)
	}
}

// fatal prints a message to stderr and exits with code 2.
// Used only for unrecoverable setup/teardown errors, not for test failures.
func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "\nFATAL: "+format+"\n", args...)
	os.Exit(2)
}
