package imagestore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParseReference(t *testing.T) {
	name, tag, err := ParseReference("myapp:latest")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if name != "myapp" || tag != "latest" {
		t.Fatalf("unexpected parse result: %s %s", name, tag)
	}
}

func TestParseReferenceRejectsInvalid(t *testing.T) {
	if _, _, err := ParseReference("not-a-ref"); err == nil {
		t.Fatal("expected error for missing tag")
	}
	if _, _, err := ParseReference("../bad:tag"); err == nil {
		t.Fatal("expected error for traversal")
	}
}

func TestSaveManifestSetsCanonicalDigest(t *testing.T) {
	tmp := t.TempDir()
	manifest := Manifest{
		Name:    "myapp",
		Tag:     "latest",
		Created: "2026-04-05T10:00:00Z",
		Config: ManifestConfig{
			Env:        []string{"KEY=value"},
			Cmd:        []string{"python", "main.py"},
			WorkingDir: "/app",
		},
		Layers: []LayerEntry{{
			Digest:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Size:      2048,
			CreatedBy: "COPY . /app",
		}},
	}

	saved, err := SaveManifest(tmp, manifest)
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}
	if saved.Digest == "" {
		t.Fatal("digest must be set")
	}
	recomputed, err := ComputeManifestDigest(saved)
	if err != nil {
		t.Fatalf("recompute failed: %v", err)
	}
	if recomputed != saved.Digest {
		t.Fatalf("digest mismatch: got %s want %s", saved.Digest, recomputed)
	}
}

func TestSaveAndLoadManifest(t *testing.T) {
	tmp := t.TempDir()
	manifest := Manifest{Name: "repo/myapp", Tag: "v1", Created: "2026-04-05T10:00:00Z"}
	if _, err := SaveManifest(tmp, manifest); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, path, err := LoadManifest(tmp, "repo/myapp:v1")
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if loaded.Name != "repo/myapp" || loaded.Tag != "v1" {
		t.Fatalf("unexpected loaded manifest: %+v", loaded)
	}
	if filepath.Ext(path) != ".json" {
		t.Fatalf("unexpected manifest path: %s", path)
	}
}

func TestListAndDeleteManifests(t *testing.T) {
	tmp := t.TempDir()
	_, _ = SaveManifest(tmp, Manifest{Name: "a", Tag: "1", Created: "2026-04-05T10:00:00Z"})
	_, _ = SaveManifest(tmp, Manifest{Name: "b", Tag: "1", Created: "2026-04-05T10:00:00Z"})

	items, err := ListManifests(tmp)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("unexpected manifest count: %d", len(items))
	}

	if err := DeleteManifest(tmp, "a:1"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	items, err = ListManifests(tmp)
	if err != nil {
		t.Fatalf("list failed after delete: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("unexpected manifest count after delete: %d", len(items))
	}
}

func TestManifestDigestMatchesCanonicalJSONRule(t *testing.T) {
	m := Manifest{
		Name:    "myapp",
		Tag:     "latest",
		Digest:  "sha256:will-be-cleared",
		Created: "2026-04-05T10:00:00Z",
	}
	wantDigest, err := ComputeManifestDigest(m)
	if err != nil {
		t.Fatalf("compute digest failed: %v", err)
	}

	m.Digest = wantDigest
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("manifest json should not be empty")
	}

	canonical := m
	canonical.Digest = ""
	canonBytes, err := json.Marshal(canonical)
	if err != nil {
		t.Fatalf("marshal canonical failed: %v", err)
	}
	if string(data) == string(canonBytes) {
		t.Fatal("final json must differ from canonical bytes because digest is populated")
	}
}

func TestLoadManifestNotFound(t *testing.T) {
	_, _, err := LoadManifest(t.TempDir(), "none:latest")
	if err == nil {
		t.Fatal("expected not found error")
	}
}

func TestDeleteManifestNotFound(t *testing.T) {
	err := DeleteManifest(t.TempDir(), "none:latest")
	if err == nil {
		t.Fatal("expected not found error")
	}
}

func TestEnsureImagesPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "images")
	if err := EnsureImagesPath(dir); err != nil {
		t.Fatalf("ensure images path failed: %v", err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if !st.IsDir() {
		t.Fatal("expected directory")
	}
}
