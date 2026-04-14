package operations

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseDocksmithfileRejectsShellFormCMD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Docksmithfile")
	content := "FROM ubuntu:latest\nCMD echo hello\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write Docksmithfile: %v", err)
	}

	instructions, err := parseDocksmithfile(path)
	if err != nil {
		t.Fatalf("parseDocksmithfile returned unexpected error: %v", err)
	}

	b := &builderState{env: map[string]string{}}
	if err := b.handleCmd(instructions[1]); err == nil {
		t.Fatal("expected shell-form CMD to be rejected")
	}
}

func TestResolveCopySourcesSupportsDoubleStar(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "assets", "nested")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	for _, rel := range []string{"assets/a.txt", "assets/nested/b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(rel), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	matches, isPattern, err := resolveCopySources(dir, "assets/**/*.txt")
	if err != nil {
		t.Fatalf("resolveCopySources returned error: %v", err)
	}
	if !isPattern {
		t.Fatal("expected pattern source to be marked as pattern")
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	if matches[0].Rel != "a.txt" || matches[1].Rel != "nested/b.txt" {
		t.Fatalf("unexpected relative targets: %+v", matches)
	}
}

func TestResolveCopySourcesRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := resolveCopySources(dir, "../secret.txt"); err == nil {
		t.Fatal("expected traversal COPY source to be rejected")
	}
}
