package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/dbrown/git-tend/internal/config"
)

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func testGitOK(t *testing.T, dir string, args ...string) {
	t.Helper()
	testGit(t, dir, args...)
}

func writeTestConfig(t *testing.T, dir, mode, syncBranch, debounce string, includePaths, excludePaths []string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("mode = ")
	b.WriteString("\"" + mode + "\"\n")
	if syncBranch != "" {
		b.WriteString("sync_branch = ")
		b.WriteString("\"" + syncBranch + "\"\n")
	}
	if debounce != "" {
		b.WriteString("debounce = ")
		b.WriteString("\"" + debounce + "\"\n")
	}
	if len(includePaths) > 0 {
		b.WriteString("[include]\n")
		b.WriteString("paths = [")
		for i, p := range includePaths {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("\"" + p + "\"")
		}
		b.WriteString("]\n")
	}
	if len(excludePaths) > 0 {
		b.WriteString("[exclude]\n")
		b.WriteString("paths = [")
		for i, p := range excludePaths {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("\"" + p + "\"")
		}
		b.WriteString("]\n")
	}
	if err := os.WriteFile(filepath.Join(dir, ".gittend"), []byte(b.String()), 0644); err != nil {
		t.Fatalf("writing .gittend: %v", err)
	}
}

func gitInitBare(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gittend-test-remote-*")
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "init", "--bare", "--initial-branch=main")
	return dir
}

func gitClone(t *testing.T, remote string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gittend-test-repo-*")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "clone", remote, dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	testGit(t, dir, "config", "user.email", "test@gittend.local")
	testGit(t, dir, "config", "user.name", "git-tend test")
	return dir
}

func TestSyncReadOnly(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	// Setup .gittend and push an initial commit
	writeTestConfig(t, repo, "read-only", "main", "", nil, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	// Make a change upstream
	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	if err := os.WriteFile(filepath.Join(clone2, "upstream.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	testGitOK(t, clone2, "add", "upstream.txt")
	testGitOK(t, clone2, "commit", "-m", "upstream change")
	testGitOK(t, clone2, "push", "origin", "main")

	stateDir, err := os.MkdirTemp("", "gittend-state-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stateDir)

	cfg := &config.Config{
		Mode:       "read-only",
		SyncBranch: "main",
		Interval:   "30s",
	}

	ctx := context.Background()
	result := Sync(ctx, repo, cfg, stateDir)
	if result.State != "ok" {
		t.Fatalf("expected ok, got %s: %s", result.State, result.Error)
	}

	// Verify the commit was pulled
	log := testGit(t, repo, "log", "--oneline")
	if !strings.Contains(log, "upstream change") {
		t.Errorf("expected upstream change in log, got:\n%s", log)
	}
}

func TestSyncReadWrite(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	writeTestConfig(t, repo, "read-write", "main", "", nil, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	// Dirty a file
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("changes"), 0644); err != nil {
		t.Fatal(err)
	}

	stateDir, err := os.MkdirTemp("", "gittend-state-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stateDir)

	cfg := &config.Config{
		Mode:       "read-write",
		SyncBranch: "main",
		Interval:   "30s",
		Debounce:   "0s",
		Commit: config.CommitConfig{
			Emoji: "🐌",
		},
	}

	ctx := context.Background()
	result := Sync(ctx, repo, cfg, stateDir)
	if result.State != "ok" {
		t.Fatalf("expected ok, got %s: %s", result.State, result.Error)
	}

	// Verify commit was pushed
	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	log := testGit(t, clone2, "log", "--oneline")
	if !strings.Contains(log, "add work.txt") {
		t.Errorf("expected 'add work.txt' in remote log, got:\n%s", log)
	}
}

func TestSyncConflictStuck(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo1 := gitClone(t, remote)
	defer os.RemoveAll(repo1)

	writeTestConfig(t, repo1, "read-write", "main", "", nil, nil)
	testGitOK(t, repo1, "add", ".gittend")
	testGitOK(t, repo1, "commit", "-m", "initial config")
	testGitOK(t, repo1, "push", "origin", "main")

	repo2 := gitClone(t, remote)
	defer os.RemoveAll(repo2)

	// Make conflicting changes on the same file
	conflictFile := filepath.Join(repo1, "conflict.txt")
	if err := os.WriteFile(conflictFile, []byte("change from repo1"), 0644); err != nil {
		t.Fatal(err)
	}

	conflictFile2 := filepath.Join(repo2, "conflict.txt")
	if err := os.WriteFile(conflictFile2, []byte("change from repo2"), 0644); err != nil {
		t.Fatal(err)
	}
	testGitOK(t, repo2, "add", "conflict.txt")
	testGitOK(t, repo2, "commit", "-m", "repo2 change")
	testGitOK(t, repo2, "push", "origin", "main")

	stateDir, err := os.MkdirTemp("", "gittend-state-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stateDir)

	cfg := &config.Config{
		Mode:       "read-write",
		SyncBranch: "main",
		Interval:   "30s",
		Debounce:   "0s",
		Commit: config.CommitConfig{
			Emoji: "🐌",
		},
	}

	ctx := context.Background()
	result := Sync(ctx, repo1, cfg, stateDir)
	if result.State != "stuck" {
		t.Fatalf("expected stuck, got %s: %s", result.State, result.Error)
	}

	// Verify .gittend.stuck exists
	stuckPath := filepath.Join(repo1, ".gittend.stuck")
	if _, err := os.Stat(stuckPath); os.IsNotExist(err) {
		t.Fatal(".gittend.stuck does not exist")
	}

	content, err := os.ReadFile(stuckPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(content)
	if !strings.Contains(s, "rebase_conflict") {
		t.Errorf("expected rebase_conflict in stuck file, got:\n%s", s)
	}
	if !strings.Contains(s, "git pull --rebase") {
		t.Errorf("expected last_command in stuck file, got:\n%s", s)
	}
}

func TestSyncIncludeExclude(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	if err := os.MkdirAll(filepath.Join(repo, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "outside"), 0755); err != nil {
		t.Fatal(err)
	}

	writeTestConfig(t, repo, "read-write", "main", "", []string{"src/"}, []string{"src/ignore.txt"})
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	// Dirty files in various locations
	if err := os.WriteFile(filepath.Join(repo, "src", "keep.txt"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "ignore.txt"), []byte("ignore"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "outside", "other.txt"), []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}

	stateDir, err := os.MkdirTemp("", "gittend-state-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stateDir)

	cfg := &config.Config{
		Mode:       "read-write",
		SyncBranch: "main",
		Interval:   "30s",
		Debounce:   "0s",
		Commit: config.CommitConfig{
			Emoji: "🐌",
		},
		Include: config.IncludeConfig{Paths: []string{"src/"}},
		Exclude: config.ExcludeConfig{Paths: []string{"src/ignore.txt"}},
	}

	ctx := context.Background()
	result := Sync(ctx, repo, cfg, stateDir)
	if result.State != "ok" {
		t.Fatalf("expected ok, got %s: %s", result.State, result.Error)
	}

	// Clone remote and check what was committed
	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)

	// Check that src/keep.txt was committed
	if _, err := os.Stat(filepath.Join(clone2, "src", "keep.txt")); os.IsNotExist(err) {
		t.Error("src/keep.txt should exist in remote")
	}

	// Check that outside/other.txt was NOT committed
	if _, err := os.Stat(filepath.Join(clone2, "outside", "other.txt")); err == nil {
		t.Error("outside/other.txt should NOT exist in remote")
	}

	// Check that src/ignore.txt was NOT committed
	if _, err := os.Stat(filepath.Join(clone2, "src", "ignore.txt")); err == nil {
		t.Error("src/ignore.txt should NOT exist in remote")
	}
}

func TestSyncBranchGuardSkips(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	writeTestConfig(t, repo, "read-only", "feature", "", nil, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")

	stateDir, err := os.MkdirTemp("", "gittend-state-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stateDir)

	cfg := &config.Config{
		Mode:       "read-only",
		SyncBranch: "feature",
		Interval:   "30s",
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "skipped" {
		t.Fatalf("expected skipped, got %s: %s", result.State, result.Error)
	}
}

func TestSyncStuckFlagSkips(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	writeTestConfig(t, repo, "read-only", "main", "", nil, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")

	// Create stuck flag
	stuckPath := filepath.Join(repo, ".gittend.stuck")
	if err := os.WriteFile(stuckPath, []byte("stuck"), 0644); err != nil {
		t.Fatal(err)
	}

	stateDir, err := os.MkdirTemp("", "gittend-state-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stateDir)

	cfg := &config.Config{
		Mode:       "read-only",
		SyncBranch: "main",
		Interval:   "30s",
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "skipped" {
		t.Fatalf("expected skipped, got %s: %s", result.State, result.Error)
	}
}

func TestSyncWithDebounce(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	writeTestConfig(t, repo, "read-write", "main", "1h", nil, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	// Create a tracked file (modified now, so within the 1h debounce)
	if err := os.WriteFile(filepath.Join(repo, "recent.txt"), []byte("recent"), 0644); err != nil {
		t.Fatal(err)
	}
	// We need this file to be tracked for the debounce check to apply.
	// git ls-files lists tracked files. New untracked files won't be in that list.
	// To have a tracked file modified recently, we can add a file, commit, then
	// modify it immediately after — the mtime will be within 1h.
	testGitOK(t, repo, "add", "recent.txt")
	testGitOK(t, repo, "commit", "-m", "add tracked file")
	testGitOK(t, repo, "push", "origin", "main")
	// Now modify it
	if err := os.WriteFile(filepath.Join(repo, "recent.txt"), []byte("just modified"), 0644); err != nil {
		t.Fatal(err)
	}

	stateDir, err := os.MkdirTemp("", "gittend-state-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stateDir)

	cfg := &config.Config{
		Mode:       "read-write",
		SyncBranch: "main",
		Interval:   "30s",
		Debounce:   "1h",
		Commit: config.CommitConfig{
			Emoji: "🐌",
		},
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "skipped" {
		t.Fatalf("expected skipped, got %s: %s", result.State, result.Error)
	}
}

func TestSyncLockContentionSkips(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	writeTestConfig(t, repo, "read-only", "main", "", nil, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")

	stateDir, err := os.MkdirTemp("", "gittend-state-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stateDir)

	hash := sha256.Sum256([]byte(repo))
	lockDir := filepath.Join(stateDir, "locks")
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(lockDir, hex.EncodeToString(hash[:])+".lock")

	ready := make(chan struct{})
	done := make(chan struct{})

	go func() {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			t.Errorf("opening lock: %v", err)
			close(ready)
			return
		}
		defer f.Close()
		if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
			t.Errorf("flock: %v", err)
			close(ready)
			return
		}
		close(ready)
		time.Sleep(3 * time.Second)
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		close(done)
	}()

	<-ready

	cfg := &config.Config{
		Mode:       "read-only",
		SyncBranch: "main",
		Interval:   "30s",
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "skipped" {
		t.Fatalf("expected skipped, got %s: %s", result.State, result.Error)
	}

	<-done
}
