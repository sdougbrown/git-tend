package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sdougbrown/git-tend/internal/config"
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

	repos := ScanRoots(cfgFor(root, 3))
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

	repos := ScanRoots(cfgFor(root, 3))
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

	repos := ScanRoots(cfgFor(root, 2))
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

	repos := ScanRoots(cfgFor(root, 3))
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

	repos := ScanRoots(cfgFor(root, 3))
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo (valid-repo only, .git subdirs skipped), got %d", len(repos))
	}
	if repos[0].Path != repoDir {
		t.Errorf("expected valid-repo, got %s", repos[0].Path)
	}
}

func cfgFor(root string, depth int) *config.UserConfig {
	return &config.UserConfig{
		Roots:     []string{root},
		ScanDepth: depth,
	}
}

func makeGitDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
}

func TestScanUserEntryOptsInWithoutGittend(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "my-repo")
	makeGitDir(t, repoDir)

	cfg := cfgFor(t.TempDir(), 3) // root without the repo
	cfg.Repos = []config.RepoEntry{{Path: repoDir}}

	repos := ScanRoots(cfg)
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if repos[0].Path != repoDir {
		t.Errorf("wrong path: %s", repos[0].Path)
	}
	if repos[0].Config.Mode != "read-only" {
		t.Errorf("expected default mode, got %q", repos[0].Config.Mode)
	}
}

func TestScanUserEntryOutsideRoots(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "elsewhere")
	makeGitDir(t, repoDir)
	if err := os.WriteFile(filepath.Join(repoDir, ".gittend"), []byte("mode = \"read-write\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := cfgFor(t.TempDir(), 3) // root without the repo
	cfg.Repos = []config.RepoEntry{{Path: repoDir, Config: config.Config{SyncBranch: "dev"}}}

	repos := ScanRoots(cfg)
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	if repos[0].Config.Mode != "read-write" || repos[0].Config.SyncBranch != "dev" {
		t.Errorf("unexpected merged config: %+v", repos[0].Config)
	}
}

func TestScanMergesUserEntryOverGittend(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "my-repo")
	makeRepo(t, repoDir, "mode = \"read-write\"\ninterval = \"2m\"\n")

	cfg := cfgFor(root, 3)
	cfg.Repos = []config.RepoEntry{{
		Path: repoDir,
		Config: config.Config{
			Interval:   "7m",
			SyncBranch: "dev",
		},
	}}

	repos := ScanRoots(cfg)
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}
	c := repos[0].Config
	// User-level value wins per key; unset keys fall back to .gittend.
	if c.Interval != "7m" {
		t.Errorf("Interval = %q, want 7m (user level)", c.Interval)
	}
	if c.Mode != "read-write" {
		t.Errorf("Mode = %q, want read-write (from .gittend)", c.Mode)
	}
	if c.SyncBranch != "dev" {
		t.Errorf("SyncBranch = %q, want dev (user level)", c.SyncBranch)
	}
}

func TestScanSkipsUserEntryForNonGitPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-a-repo")

	cfg := cfgFor(t.TempDir(), 3)
	cfg.Repos = []config.RepoEntry{{Path: missing}}

	repos := ScanRoots(cfg)
	if len(repos) != 0 {
		t.Fatalf("expected 0 repos, got %d", len(repos))
	}
}
