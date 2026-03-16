// example_test.go demonstrates how the layers package API is used by the rest of the Docksmith system.  
// These are compilable Go example functions executed by `go test
// Output: comments are intentionally omitted because the examples depend on real filesystem state that varies per machine.
// HOW TO RUN: go test ./layers/... -v -run Example
package layers
import (
	"fmt"
	"os"
	"path/filepath"
)
// Engineer 1 (Build Engine) — COPY and RUN layer creation

// ExampleCreateLayer_copy shows how the build engine creates a layer for a COPY instruction.  
// Pass the instruction string as createdBy so the returned LayerMetadata is immediately ready to be appended to the image manifest.
func ExampleCreateLayer_copy() {
	storePath, err := DefaultStorePath()
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	if err := EnsureStoreExists(storePath); err != nil {
		fmt.Println("error:", err)
		return
	}
	// sourceDir is a temporary directory the build engine has populated with
	// exactly the files changed by this COPY instruction (the delta).
	sourceDir, err := os.MkdirTemp("", "docksmith-copy-*")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer os.RemoveAll(sourceDir)
	// Write sample app files into the delta directory.
	if err := os.WriteFile(filepath.Join(sourceDir, "main.py"), []byte("print('hello')"), 0644); err != nil {
		fmt.Println("error:", err)
		return
	}
	// The createdBy string is stored in the image manifest alongside the layer.
	meta, err := CreateLayer(sourceDir, storePath, "COPY . /app")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("digest:     %s\n", meta.Digest[:len(DigestPrefix)+12]+"...")
	fmt.Printf("size:       %d bytes\n", meta.Size)
	fmt.Printf("createdBy:  %s\n", meta.CreatedBy)
	// Output lines are omitted because digest/size depend on actual file content.
}

// ExampleCreateLayer_idempotent shows that calling CreateLayer twice with identical source content returns the same digest without overwriting the stored file.
func ExampleCreateLayer_idempotent() {
	storePath, err := DefaultStorePath()
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	if err := EnsureStoreExists(storePath); err != nil {
		fmt.Println("error:", err)
		return
	}
	// Create a source directory with a fixed file.
	sourceDir, err := os.MkdirTemp("", "docksmith-idem-*")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer os.RemoveAll(sourceDir)
	if err := os.WriteFile(filepath.Join(sourceDir, "hello.txt"), []byte("hello"), 0644); err != nil {
		fmt.Println("error:", err)
		return
	}
	meta1, err := CreateLayer(sourceDir, storePath, "COPY . /app")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	// Second call with the same content must return the same digest.
	meta2, err := CreateLayer(sourceDir, storePath, "COPY . /app")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("digests match:", meta1.Digest == meta2.Digest)
	// Output: digests match: true
}