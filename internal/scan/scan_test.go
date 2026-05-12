package scan

import (
	"os"
	"path/filepath"
	"testing"
)

func makeRepo(t *testing.T, dir, gittendContent string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gittend"), []byte(gittendContent), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestScanRoots(t *testing.T) {
	root := t.TempDir()
	makeRepo(t, filepath.Join(root, "my-repo"), "mode = \"read-write\"\n")

	repos := ScanRoots([]string{root}, 3)
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if repos[0].Config.Mode != "read-write" {
		t.Errorf("wrong mode: %s", repos[0].Config.Mode)
	}
}

func TestScanIgnoresHiddenDirs(t *testing.T) {
	root := t.TempDir()
	makeRepo(t, filepath.Join(root, ".hidden-dir"), "mode = \"read-only\"\n")

	repos := ScanRoots([]string{root}, 3)
	if len(repos) != 0 {
		t.Fatalf("expected 0 repos, got %d", len(repos))
	}
}

func TestScanRespectsDepthLimit(t *testing.T) {
	root := t.TempDir()

	shallowDir := filepath.Join(root, "shallow-repo")
	makeRepo(t, shallowDir, "mode = \"read-only\"\n")

	deepDir := filepath.Join(root, "a", "b", "c", "d", "deep-repo")
	makeRepo(t, deepDir, "mode = \"read-only\"\n")

	repos := ScanRoots([]string{root}, 2)
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo (shallow only), got %d", len(repos))
	}
	if repos[0].Path != shallowDir {
		t.Errorf("expected shallow repo, got %s", repos[0].Path)
	}
}

func TestScanSkipsExcludedNames(t *testing.T) {
	root := t.TempDir()
	makeRepo(t, filepath.Join(root, "node_modules"), "mode = \"read-only\"\n")

	repos := ScanRoots([]string{root}, 3)
	if len(repos) != 0 {
		t.Fatalf("expected 0 repos (node_modules excluded), got %d", len(repos))
	}
}

func TestScanSkipsDotGitDirs(t *testing.T) {
	root := t.TempDir()

	repoDir := filepath.Join(root, "valid-repo")
	makeRepo(t, repoDir, "mode = \"read-only\"\n")

	if err := os.MkdirAll(filepath.Join(repoDir, ".git", "subdir"), 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repoDir, ".git", "subdir", ".gittend"), []byte("mode = \"read-only\"\n"), 0644)

	repos := ScanRoots([]string{root}, 3)
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo (valid-repo only, .git subdirs skipped), got %d", len(repos))
	}
	if repos[0].Path != repoDir {
		t.Errorf("expected valid-repo, got %s", repos[0].Path)
	}
}
