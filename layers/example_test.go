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

// Engineer 3 (Build Cache) — Checking layer existence
// ExampleLayerExists shows the cache system checking whether a previously computed layer digest is still present on disk before declaring a cache hit.
func ExampleLayerExists() {
	storePath, err := DefaultStorePath()
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	// A well-formed digest that is (very likely) not in the store.
	absentDigest := "sha256:" + fmt.Sprintf("%064x", 0)
	fmt.Println("absent layer exists:", LayerExists(absentDigest, storePath))
	// Output: absent layer exists: false
}

// Engineer 4 (Container Runtime) — Assembling a rootfs
// ExampleExtractLayer shows the runtime extracting multiple layers in manifest order to build the container's root filesystem.
func ExampleExtractLayer() {
	storePath, err := DefaultStorePath()
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	if err := EnsureStoreExists(storePath); err != nil {
		fmt.Println("error:", err)
		return
	}
	// Step 1: Create two layers to simulate a base layer and an app layer.
	baseDir, err := os.MkdirTemp("", "docksmith-base-*")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer os.RemoveAll(baseDir)
	appDir, err := os.MkdirTemp("", "docksmith-app-*")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer os.RemoveAll(appDir)
	// Base layer: a /etc/os-release file.
	etcDir := filepath.Join(baseDir, "etc")
	if err := os.MkdirAll(etcDir, 0755); err != nil {
		fmt.Println("error:", err)
		return
	}
	if err := os.WriteFile(filepath.Join(etcDir, "os-release"), []byte("ID=docksmith\n"), 0644); err != nil {
		fmt.Println("error:", err)
		return
	}
	// App layer: overrides /etc/os-release and adds /app/main.py.
	etcDir2 := filepath.Join(appDir, "etc")
	appDirInLayer := filepath.Join(appDir, "app")
	if err := os.MkdirAll(etcDir2, 0755); err != nil {
		fmt.Println("error:", err)
		return
	}
	if err := os.MkdirAll(appDirInLayer, 0755); err != nil {
		fmt.Println("error:", err)
		return
	}
	if err := os.WriteFile(filepath.Join(etcDir2, "os-release"), []byte("ID=myapp\n"), 0644); err != nil {
		fmt.Println("error:", err)
		return
	}
	if err := os.WriteFile(filepath.Join(appDirInLayer, "main.py"), []byte("print('hello')"), 0644); err != nil {
		fmt.Println("error:", err)
		return
	}
	baseMeta, err := CreateLayer(baseDir, storePath, "FROM alpine:3.18")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	appMeta, err := CreateLayer(appDir, storePath, "COPY . /app")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	// Step 2: Extract layers in order into a temporary rootfs directory.
	rootfs, err := os.MkdirTemp("", "docksmith-rootfs-*")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer os.RemoveAll(rootfs)
	// Manifest order: Base layer first, then the app layer (overwrites files).
	for _, digest := range []string{baseMeta.Digest, appMeta.Digest} {
		if err := ExtractLayer(digest, storePath, rootfs); err != nil {
			fmt.Println("extract error:", err)
			return
		}
	}
	// Step 3: Verify the app layer's /etc/os-release wins over the base.
	data, err := os.ReadFile(filepath.Join(rootfs, "etc", "os-release"))
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("os-release after stacking: %s", data)
	// Output: os-release after stacking: ID=myapp
}

// Example: Engineer 1 (CLI) — docksmith rmi
// ExampleDeleteLayer shows how the rmi command removes layer files.  
// All layers belonging to the removed image manifest are deleted in a loop.
func ExampleDeleteLayer() {
	storePath, err := DefaultStorePath()
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	if err := EnsureStoreExists(storePath); err != nil {
		fmt.Println("error:", err)
		return
	}
	// Create a disposable layer to demonstrate deletion.
	tmpDir, err := os.MkdirTemp("", "docksmith-del-*")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	defer os.RemoveAll(tmpDir)
	if err := os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte("delete me"), 0644); err != nil {
		fmt.Println("error:", err)
		return
	}
	meta, err := CreateLayer(tmpDir, storePath, "COPY . /app")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("exists before delete:", LayerExists(meta.Digest, storePath))
	if err := DeleteLayer(meta.Digest, storePath); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("exists after delete:", LayerExists(meta.Digest, storePath))
	// Output:
	// exists before delete: true
	// exists after delete: false
}