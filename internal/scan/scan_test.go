package scan

import (
	"fmt"
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

func cfgFor(t *testing.T, root string, depth int, repos string) *config.UserConfig {
	t.Helper()
	data := fmt.Sprintf("roots = [%q]\nscan_depth = %d\n%s", root, depth, repos)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseUserConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestScanRoots(t *testing.T) {
	root := t.TempDir()
	makeRepo(t, filepath.Join(root, "my-repo"), "mode = \"read-write\"\n")

	repos := ScanRoots(cfgFor(t, root, 3, ""))
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

	repos := ScanRoots(cfgFor(t, root, 3, ""))
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

	repos := ScanRoots(cfgFor(t, root, 2, ""))
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

	repos := ScanRoots(cfgFor(t, root, 3, ""))
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

	repos := ScanRoots(cfgFor(t, root, 3, ""))
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo (valid-repo only, .git subdirs skipped), got %d", len(repos))
	}
	if repos[0].Path != repoDir {
		t.Errorf("expected valid-repo, got %s", repos[0].Path)
	}
}

func TestScanSkipsWorktrees(t *testing.T) {
	root := t.TempDir()

	// A worktree has a .git file, not a .git directory.
	worktree := filepath.Join(root, "my-worktree")
	if err := os.MkdirAll(worktree, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: /elsewhere\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".gittend"), []byte("mode = \"read-write\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	repos := ScanRoots(cfgFor(t, root, 3, ""))
	if len(repos) != 0 {
		t.Fatalf("expected 0 repos (worktree not synced), got %d", len(repos))
	}

	// The same applies to a [[repo]] entry pointing at a worktree.
	repos = ScanRoots(cfgFor(t, t.TempDir(), 3, fmt.Sprintf("[[repo]]\npath = %q\n", worktree)))
	if len(repos) != 0 {
		t.Fatalf("expected 0 repos (worktree entry skipped), got %d", len(repos))
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

	cfg := cfgFor(t, t.TempDir(), 3, fmt.Sprintf("[[repo]]\npath = %q\n", repoDir)) // root without the repo

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

	cfg := cfgFor(t, t.TempDir(), 3, fmt.Sprintf("[[repo]]\npath = %q\nsync_branch = \"dev\"\n", repoDir)) // root without the repo

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

	cfg := cfgFor(t, root, 3, fmt.Sprintf("[[repo]]\npath = %q\ninterval = \"7m\"\nsync_branch = \"dev\"\n", repoDir))

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

	cfg := cfgFor(t, t.TempDir(), 3, fmt.Sprintf("[[repo]]\npath = %q\n", missing))

	repos := ScanRoots(cfg)
	if len(repos) != 0 {
		t.Fatalf("expected 0 repos, got %d", len(repos))
	}
}

func TestScanUserEntrySymlinkLoadedOnce(t *testing.T) {
	// An entry pointing at a symlink to a repo under a root must not load the
	// repo twice: the walk finds the real path, and SamePath resolves the
	// entry's symlink to the same directory.
	root := t.TempDir()
	repoDir := filepath.Join(root, "my-repo")
	makeRepo(t, repoDir, "mode = \"read-write\"\n")
	link := filepath.Join(t.TempDir(), "link-to-my-repo")
	if err := os.Symlink(repoDir, link); err != nil {
		t.Fatal(err)
	}

	cfg := cfgFor(t, root, 3, fmt.Sprintf("[[repo]]\npath = %q\ninterval = \"7m\"\n", link))

	repos := ScanRoots(cfg)
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo (symlink entry merged with walked repo), got %d", len(repos))
	}
	if repos[0].Path != repoDir {
		t.Errorf("Path = %q, want walked real path %q", repos[0].Path, repoDir)
	}
	// The entry must be merged into the walked repo, not just skipped.
	if repos[0].Config.Interval != "7m" || repos[0].Config.Mode != "read-write" {
		t.Errorf("unexpected merged config: %+v", repos[0].Config)
	}
}
