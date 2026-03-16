// main.go is a self-contained integration test for the docksmith layer system.
// Creates real files, calls every exported API function, and verifies results.
// Run from the docksmith/ directory:
// go run main.go
package main
import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"docksmith/layers"
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
}