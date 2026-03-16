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
}