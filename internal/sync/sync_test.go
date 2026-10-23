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

	"github.com/sdougbrown/git-tend/internal/config"
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

func TestSyncSuccessWithoutAgentStateCreatesNoAgentFiles(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	writeTestConfig(t, repo, "read-only", "main", "", nil, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

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
	if result.State != "ok" {
		t.Fatalf("expected ok, got %s: %s", result.State, result.Error)
	}

	if _, err := os.Stat(filepath.Join(stateDir, "agent")); !os.IsNotExist(err) {
		t.Errorf("agent state dir should not be created by a successful sync with no agent episode (stat err: %v)", err)
	}
	if _, err := os.Stat(AgentLockPath(stateDir, repo)); !os.IsNotExist(err) {
		t.Errorf("agent lock file should not be created by a successful sync with no agent episode (stat err: %v)", err)
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

// A read-write sync that fails on a commit hook while the agent lock is held
// must surface the writeStuck failure: the stuck flag can't be recorded (an
// agent run is in flight), so the error carries the "recording stuck state"
// surface rather than silently dropping the stuck record.
func TestSyncReadWriteHookFailedSurfacesWriteStuckFailure(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	writeTestConfig(t, repo, "read-write", "main", "", nil, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	// Dirty a file so there is something to commit.
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("changes"), 0644); err != nil {
		t.Fatal(err)
	}

	// A failing pre-commit hook makes git commit fail with a hook error.
	gitDir := testGit(t, repo, "rev-parse", "--absolute-git-dir")
	gitDir = strings.TrimSpace(gitDir)
	hookPath := filepath.Join(gitDir, "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\necho \"pre-commit hook failed\" >&2\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}

	stateDir, err := os.MkdirTemp("", "gittend-state-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stateDir)

	// Hold the agent lock so writeStuck fails.
	lock, err := AcquireAgentLock(stateDir, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	cfg := &config.Config{
		Mode:       "read-write",
		SyncBranch: "main",
		Interval:   "30s",
		Debounce:   "0s",
		Commit: config.CommitConfig{
			Emoji: "🐌",
		},
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "stuck" {
		t.Fatalf("expected stuck, got %s: %s", result.State, result.Error)
	}
	if !strings.Contains(result.Error, "recording stuck state") {
		t.Errorf("Error = %q, want it to contain \"recording stuck state\"", result.Error)
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

	manual := SyncManual(context.Background(), repo, cfg, stateDir)
	if manual.State != "ok" {
		t.Fatalf("manual sync should bypass debounce, got %s: %s", manual.State, manual.Error)
	}
	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	contents, err := os.ReadFile(filepath.Join(clone2, "recent.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(contents); got != "just modified" {
		t.Errorf("remote recent.txt = %q, want committed manual change", got)
	}
}

func TestSyncSkipCommitInProgress(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	writeTestConfig(t, repo, "read-write", "main", "0s", nil, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	// Dirty a file so there is something to commit.
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("changes"), 0644); err != nil {
		t.Fatal(err)
	}

	// Simulate an in-progress manual commit: git-tend's own setup commits
	// always clear the sentinel, so write COMMIT_EDITMSG directly and leave it
	// fresh.
	gitDir := testGit(t, repo, "rev-parse", "--absolute-git-dir")
	gitDir = strings.TrimSpace(gitDir)
	if err := os.WriteFile(filepath.Join(gitDir, "COMMIT_EDITMSG"), []byte("draft message"), 0644); err != nil {
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
			Emoji:            "🐌",
			InProgressWindow: "10m",
		},
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "skipped" || result.Error != "commit in progress" {
		t.Fatalf("expected skipped (commit in progress), got %s: %s", result.State, result.Error)
	}

	// The change must not have been committed/pushed.
	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	if _, err := os.Stat(filepath.Join(clone2, "work.txt")); err == nil {
		t.Error("work.txt should NOT be committed while a commit is in progress")
	}
}

func TestSyncGitLockSkips(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	writeTestConfig(t, repo, "read-write", "main", "0s", nil, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	// Dirty a file so there is something to commit.
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("changes"), 0644); err != nil {
		t.Fatal(err)
	}

	// Simulate a concurrent git write, e.g. index.lock mid-commit.
	gitDir := testGit(t, repo, "rev-parse", "--absolute-git-dir")
	gitDir = strings.TrimSpace(gitDir)
	if err := os.WriteFile(filepath.Join(gitDir, "index.lock"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(filepath.Join(gitDir, "index.lock"))

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

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "skipped" || result.Error != "git lock present (concurrent operation)" {
		t.Fatalf("expected skipped (git lock present), got %s: %s", result.State, result.Error)
	}

	// The change must not have been committed/pushed.
	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	if _, err := os.Stat(filepath.Join(clone2, "work.txt")); err == nil {
		t.Error("work.txt should NOT be committed while a git lock is present")
	}
}

// Simulates git-tend's own auto-commit backing off against itself. git commit
// writes a fresh COMMIT_EDITMSG, so without the post-commit clear the next tick
// with a window set would skip on its own sentinel.
func TestSyncDoesNotBackOffAgainstOwnCommit(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	writeTestConfig(t, repo, "read-write", "main", "0s", nil, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

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
	if result := Sync(ctx, repo, cfg, stateDir); result.State != "ok" {
		t.Fatalf("first sync expected ok, got %s: %s", result.State, result.Error)
	}

	// The auto-commit left a fresh COMMIT_EDITMSG (cleared by git-tend, but if
	// the clear regresses this window now applies to git-tend's own commit).
	cfg.Commit.InProgressWindow = "10m"
	result := Sync(ctx, repo, cfg, stateDir)
	if result.State == "skipped" && result.Error == "commit in progress" {
		t.Fatal("second tick backed off against git-tend's own commit; ClearCommitEditMsg regressed")
	}
	if result.State != "ok" {
		t.Fatalf("second sync expected ok, got %s: %s", result.State, result.Error)
	}
}

func TestSyncStaleCommitEditMsgProceeds(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	writeTestConfig(t, repo, "read-write", "main", "0s", nil, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("changes"), 0644); err != nil {
		t.Fatal(err)
	}

	// A stale sentinel (older than the window) must not cause a skip.
	gitDir := testGit(t, repo, "rev-parse", "--absolute-git-dir")
	gitDir = strings.TrimSpace(gitDir)
	msgPath := filepath.Join(gitDir, "COMMIT_EDITMSG")
	if err := os.WriteFile(msgPath, []byte("old draft"), 0644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(msgPath, past, past); err != nil {
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
			Emoji:            "🐌",
			InProgressWindow: "10m",
		},
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "ok" {
		t.Fatalf("expected ok with stale COMMIT_EDITMSG, got %s: %s", result.State, result.Error)
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
	release := make(chan struct{})
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
		// Hold the lock until the assertions are done; a fixed sleep would
		// flake if the test goroutine is descheduled past the deadline.
		<-release
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
	if result.State != "skipped" || result.Error != "locked" {
		t.Fatalf("expected skipped (locked), got %s: %s", result.State, result.Error)
	}

	close(release)
	<-done
}

// runSyncCommitTest clones a repo, lets dirty modify the working tree, and
// runs one read-write sync with the given commit config. It returns the
// remote's commit messages in full (subject and body) so multi-line messages
// can be asserted on.
func runSyncCommitTest(t *testing.T, commitCfg config.CommitConfig, dirty func(repo string)) string {
	t.Helper()
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	writeTestConfig(t, repo, "read-write", "main", "", nil, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("changes"), 0644); err != nil {
		t.Fatal(err)
	}
	if dirty != nil {
		dirty(repo)
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
		Commit:     commitCfg,
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "ok" {
		t.Fatalf("expected ok, got %s: %s", result.State, result.Error)
	}

	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	return testGit(t, clone2, "log", "--format=%B")
}

// runModelSyncTest clones a repo, dirties a single file, and runs one
// read-write sync with the given commit config.
func runModelSyncTest(t *testing.T, commitCfg config.CommitConfig) string {
	return runSyncCommitTest(t, commitCfg, nil)
}

func TestSyncAgentStrategyUsesAgent(t *testing.T) {
	log := runSyncCommitTest(t, config.CommitConfig{
		Emoji:    "🐌",
		Strategy: "agent",
		ModelCmd: `if [ "$(grep -c -e '^repo: /' -e '^branch: main$' -e 'Output only the message text itself')" -ge 3 ]; then echo 'agent wrote this subject'; else echo 'agent stdin missing repo/branch context or default prompt'; fi`,
	}, dirtyAcrossDirs)
	if !strings.Contains(log, "agent wrote this subject") {
		t.Errorf("expected agent message in log, got:\n%s", log)
	}
}

func TestSyncAgentStrategyPassesMultiLineMessage(t *testing.T) {
	log := runSyncCommitTest(t, config.CommitConfig{
		Emoji:    "🐌",
		Strategy: "agent",
		ModelCmd: "printf 'agent subject\n\nagent body explaining the why\n'",
	}, dirtyAcrossDirs)
	if !strings.Contains(log, "agent subject") {
		t.Errorf("expected agent subject in log, got:\n%s", log)
	}
	if !strings.Contains(log, "agent body explaining the why") {
		t.Errorf("expected agent body to survive the commit, got:\n%s", log)
	}
}

func TestSyncAgentStrategyReceivesFullDiff(t *testing.T) {
	// The agent runs after staging, so its stdin must carry the full cached
	// diff — this pins the ordering: debounce/lock checks, then git add,
	// then the agent. The '^repo: /', '^branch: main$', and default-prompt
	// greps are the agent-only part of the stdin payload: the non-agent
	// fallthrough path sends the full diff alone, so this test fails if the
	// agent branch is ever bypassed. The repo value is a temp path (not
	// deterministic), so it is asserted as an absolute-path prefix; the branch
	// value is asserted exactly. One grep pass with a match count, because two
	// sequential greps on the same pipe would race over buffered input.
	agentCmd := `if [ "$(grep -c -e '^repo: /' -e '^branch: main$' -e '^diff --git' -e 'Output only the message text itself')" -ge 4 ]; then echo 'agent saw full diff'; else echo 'agent stdin incomplete'; fi`
	log := runSyncCommitTest(t, config.CommitConfig{
		Emoji:    "🐌",
		Strategy: "agent",
		ModelCmd: agentCmd,
	}, dirtyAcrossDirs)
	if !strings.Contains(log, "agent saw full diff") {
		t.Errorf("expected full diff on agent stdin, got:\n%s", log)
	}
}

func TestSyncAgentStrategyFallsBackOnEmptyOutput(t *testing.T) {
	log := runSyncCommitTest(t, config.CommitConfig{
		Emoji:    "🐌",
		Strategy: "agent",
		ModelCmd: "true",
	}, dirtyAcrossDirs)
	if !strings.Contains(log, "sync 2 files") {
		t.Errorf("expected heuristic fallback in log, got:\n%s", log)
	}
}

func TestSyncAgentStrategyFallsBackOnError(t *testing.T) {
	log := runSyncCommitTest(t, config.CommitConfig{
		Emoji:    "🐌",
		Strategy: "agent",
		ModelCmd: "exit 1",
	}, dirtyAcrossDirs)
	if !strings.Contains(log, "sync 2 files") {
		t.Errorf("expected heuristic fallback in log, got:\n%s", log)
	}
}

func TestSyncAgentStrategySkipsAgentForInformativeHeuristic(t *testing.T) {
	// A single-file change yields "add work.txt", which is informative, so
	// the agent must never be invoked (cost guardrail).
	log := runSyncCommitTest(t, config.CommitConfig{
		Emoji:    "🐌",
		Strategy: "agent",
		ModelCmd: "echo 'agent should not run'",
	}, nil)
	if !strings.Contains(log, "add work.txt") {
		t.Errorf("expected heuristic message in log, got:\n%s", log)
	}
	if strings.Contains(log, "agent should not run") {
		t.Errorf("agent should not run for informative heuristic, got:\n%s", log)
	}
}

// dirtyAcrossDirs touches a second file in a different top-level directory so
// the heuristic produces a low-information "sync 2 files" message — the only
// state in which the agent strategy consults the agent.
func dirtyAcrossDirs(repo string) {
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "thing.txt"), []byte("changes"), 0644); err != nil {
		panic(err)
	}
}

func TestSyncModelStrategyUsesModel(t *testing.T) {
	log := runModelSyncTest(t, config.CommitConfig{
		Emoji:    "🐌",
		Strategy: "model",
		ModelCmd: "echo 'model wrote this'",
	})
	if !strings.Contains(log, "model wrote this") {
		t.Errorf("expected model message in log, got:\n%s", log)
	}
}

func TestSyncModelStrategyComposesDefaultPrompt(t *testing.T) {
	// With strategy=model and no model_prompt, the built-in default prompt is
	// appended to the name-status diff on the model's stdin.
	modelCmd := "if grep -q 'name-status listing'; then echo 'prompt reached model'; else echo 'prompt missing'; fi"
	log := runModelSyncTest(t, config.CommitConfig{
		Emoji:    "🐌",
		Strategy: "model",
		ModelCmd: modelCmd,
	})
	if !strings.Contains(log, "prompt reached model") {
		t.Errorf("expected composed default prompt on stdin, got:\n%s", log)
	}
}

func TestSyncModelStrategyFallsBackOnError(t *testing.T) {
	log := runModelSyncTest(t, config.CommitConfig{
		Emoji:    "🐌",
		Strategy: "model",
		ModelCmd: "exit 1",
	})
	if !strings.Contains(log, "add work.txt") {
		t.Errorf("expected heuristic fallback in log, got:\n%s", log)
	}
}

func TestSyncDeterministicStrategyKeepsHeuristic(t *testing.T) {
	// A model_cmd under the default strategy must stay fallback-only: the
	// heuristic message "add work.txt" is informative, so the model is never
	// consulted.
	log := runModelSyncTest(t, config.CommitConfig{
		Emoji:    "🐌",
		ModelCmd: "echo 'model wrote this'",
	})
	if !strings.Contains(log, "add work.txt") {
		t.Errorf("expected heuristic message in log, got:\n%s", log)
	}
	if strings.Contains(log, "model wrote this") {
		t.Errorf("model should not run for informative heuristic message, got:\n%s", log)
	}
}

func TestSyncDeterministicStrategyPromptSendsNameStatusDiff(t *testing.T) {
	// Under the default strategy with a custom model_prompt, the model's
	// stdin must be the name-status diff followed by the prompt — not the
	// full patch. A bug that reuses fullDiff in the prompt branch would send
	// the full patch instead, which this test catches.
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	writeTestConfig(t, repo, "read-write", "main", "", nil, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	// Stage enough files across more top-level directories than the fallback
	// threshold so the heuristic is low-information ("sync N files") and the
	// model is consulted.
	for _, dir := range []string{"src", "lib", "cmd", "pkg"} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, dir, "f.txt"), []byte(dir), 0644); err != nil {
			t.Fatal(err)
		}
	}

	stateDir, err := os.MkdirTemp("", "gittend-state-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stateDir)

	// The model command reports what it sees on stdin: whether the custom
	// prompt is present, whether the full-patch marker is present, and
	// whether a name-status line is present.
	modelCmd := "input=$(cat); p=missing; if printf '%s\\n' \"$input\" | grep -q 'custom prompt marker'; then p=present; fi; f=full; if printf '%s\\n' \"$input\" | grep -q 'diff --git'; then f=full; else f=none; fi; n=none; if printf '%s\\n' \"$input\" | grep -q 'A\tcmd/f.txt'; then n=ns; fi; echo \"prompt=$p full=$f ns=$n\""

	cfg := &config.Config{
		Mode:       "read-write",
		SyncBranch: "main",
		Interval:   "30s",
		Debounce:   "0s",
		Commit: config.CommitConfig{
			Emoji:          "🐌",
			ModelCmd:       modelCmd,
			ModelPrompt:    "custom prompt marker",
			FallbackThresh: 3,
		},
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "ok" {
		t.Fatalf("expected ok, got %s: %s", result.State, result.Error)
	}

	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	log := testGit(t, clone2, "log", "--oneline")
	if !strings.Contains(log, "prompt=present full=none ns=ns") {
		t.Errorf("expected custom prompt + name-status diff (no full patch) on model stdin, got:\n%s", log)
	}
}

func TestSyncDeterministicStrategyNoPromptSendsFullDiff(t *testing.T) {
	// Backward compat: under the default strategy with no model_prompt, the
	// model's stdin must be the full patch — no prompt composed. This is the
	// inline-prompt model_cmd scenario: a command carrying its own prompt in
	// its argv keeps receiving the full diff.
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	writeTestConfig(t, repo, "read-write", "main", "", nil, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	for _, dir := range []string{"src", "lib", "cmd", "pkg"} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, dir, "f.txt"), []byte(dir), 0644); err != nil {
			t.Fatal(err)
		}
	}

	stateDir, err := os.MkdirTemp("", "gittend-state-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stateDir)

	modelCmd := "input=$(cat); p=missing; if printf '%s\\n' \"$input\" | grep -q 'name-status listing'; then p=present; fi; f=none; if printf '%s\\n' \"$input\" | grep -q 'diff --git'; then f=full; fi; echo \"prompt=$p full=$f\""

	cfg := &config.Config{
		Mode:       "read-write",
		SyncBranch: "main",
		Interval:   "30s",
		Debounce:   "0s",
		Commit: config.CommitConfig{
			Emoji:          "🐌",
			ModelCmd:       modelCmd,
			FallbackThresh: 3,
		},
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "ok" {
		t.Fatalf("expected ok, got %s: %s", result.State, result.Error)
	}

	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	log := testGit(t, clone2, "log", "--oneline")
	if !strings.Contains(log, "prompt=missing full=full") {
		t.Errorf("expected full diff with no composed prompt on model stdin, got:\n%s", log)
	}
}

// In folder-scoped write mode, git-tend's `git add` is limited to [include]
// paths but its commit takes the whole index. A human's staged work otherwise
// rides along under a generated message. A tick that finds staged paths outside
// the scope leaves the repo alone and resumes once the index clears.
func TestSyncSkipsWhenStagedOutsideScope(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	for _, d := range []string{"src", "outside"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestConfig(t, repo, "read-write", "main", "0s", []string{"src/"}, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	// Someone stages work of their own outside the write scope.
	if err := os.WriteFile(filepath.Join(repo, "outside", "notes.txt"), []byte("mine"), 0644); err != nil {
		t.Fatal(err)
	}
	testGitOK(t, repo, "add", "outside/notes.txt")

	// The scope is dirty too, so this tick has something of its own to commit.
	if err := os.WriteFile(filepath.Join(repo, "src", "keep.txt"), []byte("keep"), 0644); err != nil {
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
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "skipped" || result.Error != "staged changes outside the write scope" {
		t.Fatalf("expected skipped (staged outside write scope), got %s: %s", result.State, result.Error)
	}

	// Nothing leaves the repo: neither the foreign staging nor our scope.
	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	for _, p := range []string{"outside/notes.txt", "src/keep.txt"} {
		if _, err := os.Stat(filepath.Join(clone2, p)); err == nil {
			t.Errorf("%s should not be committed while a foreign staging is present", p)
		}
	}

	// The index must be left exactly as the human staged it, not topped up
	// with our scope.
	staged := strings.TrimSpace(testGit(t, repo, "diff", "--cached", "--name-only"))
	if staged != "outside/notes.txt" {
		t.Errorf("staged = %q, want only the human's staging", staged)
	}

	// Once their commit lands the next tick carries on as normal.
	testGitOK(t, repo, "commit", "-m", "manual commit")
	result = Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "ok" {
		t.Fatalf("expected ok once the index cleared, got %s: %s", result.State, result.Error)
	}

	clone3 := gitClone(t, remote)
	defer os.RemoveAll(clone3)
	if _, err := os.Stat(filepath.Join(clone3, "src", "keep.txt")); err != nil {
		t.Error("src/keep.txt should be committed once the foreign staging is gone")
	}
}

// Staging inside the write scope is indistinguishable from git-tend's own work
// and is fair game: the tick commits it.
func TestSyncCommitsWhenStagedInScopeOnly(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	if err := os.MkdirAll(filepath.Join(repo, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	writeTestConfig(t, repo, "read-write", "main", "0s", []string{"src/"}, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	if err := os.WriteFile(filepath.Join(repo, "src", "keep.txt"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	testGitOK(t, repo, "add", "src/keep.txt")

	// Untracked work outside the scope is not staged work, so it must not block
	// the tick.
	if err := os.WriteFile(filepath.Join(repo, "outside-scope.txt"), []byte("untracked"), 0644); err != nil {
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
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "ok" {
		t.Fatalf("expected ok, got %s: %s", result.State, result.Error)
	}

	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	if _, err := os.Stat(filepath.Join(clone2, "src", "keep.txt")); err != nil {
		t.Error("src/keep.txt should be committed when all staging is in scope")
	}
}

// A path git-tend deliberately excludes is outside its write scope, so staged
// work there is someone else's too. Without the exclude probe the tick proceeds
// and the commit, which takes the whole index, pushes it.
func TestSyncSkipsWhenStagedExcluded(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	for _, d := range []string{"src", "src/secrets"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestConfig(t, repo, "read-write", "main", "0s", []string{"src/"}, []string{"src/secrets/"})
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	// Inside the include paths, but excluded from git-tend's staging.
	if err := os.WriteFile(filepath.Join(repo, "src", "secrets", "key.pem"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	testGitOK(t, repo, "add", "src/secrets/key.pem")

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
		Exclude: config.ExcludeConfig{Paths: []string{"src/secrets/"}},
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "skipped" {
		t.Fatalf("expected skipped (staged exclude path), got %s: %s", result.State, result.Error)
	}

	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	if _, err := os.Stat(filepath.Join(clone2, "src", "secrets", "key.pem")); err == nil {
		t.Error("excluded src/secrets/key.pem must not be committed")
	}
}

// Magic-prefixed include pathspecs must survive the inversion the guard builds
// from them; a regression there makes every scoped tick fail.
func TestSyncGlobIncludeScope(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	for _, d := range []string{"notes", "projects/alpha/memory"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	include := []string{":(glob)projects/*/memory/*"}
	writeTestConfig(t, repo, "read-write", "main", "0s", include, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	if err := os.WriteFile(filepath.Join(repo, "notes", "scratch.md"), []byte("mine"), 0644); err != nil {
		t.Fatal(err)
	}
	testGitOK(t, repo, "add", "notes/scratch.md")
	if err := os.WriteFile(filepath.Join(repo, "projects", "alpha", "memory", "note.md"), []byte("scoped"), 0644); err != nil {
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
		Include: config.IncludeConfig{Paths: include},
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "skipped" {
		t.Fatalf("expected skipped (staged outside glob scope), got %s: %s", result.State, result.Error)
	}

	testGitOK(t, repo, "commit", "-m", "manual commit")
	result = Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "ok" {
		t.Fatalf("expected ok once the index cleared, got %s: %s", result.State, result.Error)
	}

	// Staged work matching the glob is in scope, so the tick commits it. This
	// is the tick that catches a broken inversion: a pathspec that matches
	// nothing reports every staged path as outside, which skips here.
	if err := os.WriteFile(filepath.Join(repo, "projects", "alpha", "memory", "staged.md"), []byte("staged"), 0644); err != nil {
		t.Fatal(err)
	}
	testGitOK(t, repo, "add", "projects/alpha/memory/staged.md")
	result = Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "ok" {
		t.Fatalf("expected ok for in-scope staged work, got %s: %s", result.State, result.Error)
	}

	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	for _, p := range []string{"projects/alpha/memory/note.md", "projects/alpha/memory/staged.md"} {
		if _, err := os.Stat(filepath.Join(clone2, p)); err != nil {
			t.Errorf("%s should be committed under the glob scope", p)
		}
	}
}

// A model or agent run keeps the tick open long enough for a human to stage
// out-of-scope work after the tick-start check. The commit takes the whole
// index, so the guard runs again immediately before it.
func TestSyncRechecksStagedPathsBeforeCommit(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	for _, d := range []string{"src", "outside"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestConfig(t, repo, "read-write", "main", "0s", []string{"src/"}, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	// Work in git-tend's own scope, so this tick has something to commit.
	if err := os.WriteFile(filepath.Join(repo, "src", "keep.txt"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	// The human stages outside the scope while the model is authoring the
	// message: the model command performs the staging as a side effect.
	if err := os.WriteFile(filepath.Join(repo, "outside", "notes.txt"), []byte("mine"), 0644); err != nil {
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
			Emoji:    "🐌",
			Strategy: "model",
			ModelCmd: "git -C " + repo + " add outside/notes.txt && echo 'sync things'",
		},
		Include: config.IncludeConfig{Paths: []string{"src/"}},
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "skipped" {
		t.Fatalf("expected skipped (staging appeared mid-tick), got %s: %s", result.State, result.Error)
	}

	// The tick staged its own scope before the re-check. Leaving that behind
	// would sweep it into the human's commit under their message, so the index
	// goes back to holding only their staging.
	staged := strings.TrimSpace(testGit(t, repo, "diff", "--cached", "--name-only"))
	if staged != "outside/notes.txt" {
		t.Errorf("staged = %q, want only the human's staging", staged)
	}

	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	for _, p := range []string{"outside/notes.txt", "src/keep.txt"} {
		if _, err := os.Stat(filepath.Join(clone2, p)); err == nil {
			t.Errorf("%s should not be committed when staging appeared mid-tick", p)
		}
	}
}

// [exclude] without [include] narrows the staging too: git-tend stages
// everything but those paths, and the commit still takes the whole index.
func TestSyncSkipsWhenStagedExcludedWithoutInclude(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	for _, d := range []string{"src", "secrets"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestConfig(t, repo, "read-write", "main", "0s", nil, []string{"secrets/"})
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	if err := os.WriteFile(filepath.Join(repo, "secrets", "key.pem"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	testGitOK(t, repo, "add", "secrets/key.pem")
	if err := os.WriteFile(filepath.Join(repo, "src", "keep.txt"), []byte("keep"), 0644); err != nil {
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
		Exclude: config.ExcludeConfig{Paths: []string{"secrets/"}},
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "skipped" {
		t.Fatalf("expected skipped (staged exclude path), got %s: %s", result.State, result.Error)
	}

	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	for _, p := range []string{"secrets/key.pem", "src/keep.txt"} {
		if _, err := os.Stat(filepath.Join(clone2, p)); err == nil {
			t.Errorf("%s should not be committed while an exclude path is staged", p)
		}
	}
}

// A negative include spec means "everything except". The guard has to read it
// the way git does rather than treating the scope as empty.
func TestSyncNegativeIncludeScope(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	for _, d := range []string{"notes", "src"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	include := []string{":!notes/"}
	writeTestConfig(t, repo, "read-write", "main", "0s", include, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

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
		Include: config.IncludeConfig{Paths: include},
	}

	// Staged work under the excluded-by-negation path is someone else's.
	if err := os.WriteFile(filepath.Join(repo, "notes", "scratch.md"), []byte("mine"), 0644); err != nil {
		t.Fatal(err)
	}
	testGitOK(t, repo, "add", "notes/scratch.md")
	if err := os.WriteFile(filepath.Join(repo, "src", "keep.txt"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "skipped" {
		t.Fatalf("expected skipped (staged path the negative include leaves out), got %s: %s", result.State, result.Error)
	}

	// Everything else is in scope, so the tick commits it once the index clears.
	testGitOK(t, repo, "commit", "-m", "manual commit")
	result = Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "ok" {
		t.Fatalf("expected ok, got %s: %s", result.State, result.Error)
	}

	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	if _, err := os.Stat(filepath.Join(clone2, "src", "keep.txt")); err != nil {
		t.Error("src/keep.txt is in the negative include scope and should be committed")
	}
}

// The same re-check in exclude-only mode. The unstage must remove the tick's own
// staging, which here is the whole tree minus the excludes, without touching the
// human's excluded staging. Its `git reset` pathspec set differs from the
// include-scoped variant.
func TestSyncRechecksStagedPathsWithExcludeOnly(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	for _, d := range []string{"notes", "src"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestConfig(t, repo, "read-write", "main", "0s", nil, []string{"notes/"})
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	// In git-tend's scope, so this tick has something of its own to stage.
	if err := os.WriteFile(filepath.Join(repo, "src", "keep.txt"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	// The human stages an excluded path while the model authors the message.
	if err := os.WriteFile(filepath.Join(repo, "notes", "scratch.md"), []byte("mine"), 0644); err != nil {
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
			Emoji:    "🐌",
			Strategy: "model",
			ModelCmd: "git -C " + repo + " add notes/scratch.md && echo 'sync things'",
		},
		Exclude: config.ExcludeConfig{Paths: []string{"notes/"}},
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "skipped" {
		t.Fatalf("expected skipped (staging appeared mid-tick), got %s: %s", result.State, result.Error)
	}

	staged := strings.TrimSpace(testGit(t, repo, "diff", "--cached", "--name-only"))
	if staged != "notes/scratch.md" {
		t.Errorf("staged = %q, want only the human's excluded staging", staged)
	}

	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	for _, p := range []string{"notes/scratch.md", "src/keep.txt"} {
		if _, err := os.Stat(filepath.Join(clone2, p)); err == nil {
			t.Errorf("%s should not be committed when staging appeared mid-tick", p)
		}
	}
}

// The re-check + unstage with a negative include spec. `git reset -- :!notes/`
// has to honour the negation, or the unstage would revert the human's staging
// instead of git-tend's.
func TestSyncRechecksStagedPathsWithNegativeInclude(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	for _, d := range []string{"notes", "src"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestConfig(t, repo, "read-write", "main", "0s", []string{":!notes/"}, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	if err := os.WriteFile(filepath.Join(repo, "src", "keep.txt"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "notes", "mid.md"), []byte("mine"), 0644); err != nil {
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
			Emoji:    "🐌",
			Strategy: "model",
			ModelCmd: "git -C " + repo + " add notes/mid.md && echo 'sync things'",
		},
		Include: config.IncludeConfig{Paths: []string{":!notes/"}},
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "skipped" {
		t.Fatalf("expected skipped (staging appeared mid-tick), got %s: %s", result.State, result.Error)
	}

	staged := strings.TrimSpace(testGit(t, repo, "diff", "--cached", "--name-only"))
	if staged != "notes/mid.md" {
		t.Errorf("staged = %q, want only the human's negated-out staging", staged)
	}

	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	for _, p := range []string{"notes/mid.md", "src/keep.txt"} {
		if _, err := os.Stat(filepath.Join(clone2, p)); err == nil {
			t.Errorf("%s should not be committed when staging appeared mid-tick", p)
		}
	}
}

// A magic-prefixed [exclude] entry is translated for the staging and probed with
// its original spec. A broken translation would let git-tend stage the excluded
// path itself and commit it, so the tick is checked with nothing staged by hand.
func TestSyncMagicExcludeScope(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	for _, d := range []string{"secrets", "src"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestConfig(t, repo, "read-write", "main", "0s", nil, []string{":(glob)secrets/*.pem"})
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

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
		Exclude: config.ExcludeConfig{Paths: []string{":(glob)secrets/*.pem"}},
	}

	// Dirty, and nothing staged: the tick stages its own scope, which must not
	// include the magic-excluded path.
	if err := os.WriteFile(filepath.Join(repo, "secrets", "key.pem"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "keep.txt"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "ok" {
		t.Fatalf("expected ok, got %s: %s", result.State, result.Error)
	}

	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	if _, err := os.Stat(filepath.Join(clone2, "src", "keep.txt")); err != nil {
		t.Error("src/keep.txt should be committed")
	}
	if _, err := os.Stat(filepath.Join(clone2, "secrets", "key.pem")); err == nil {
		t.Error("the magic-excluded secrets/key.pem must not be staged or committed")
	}

	// Staged by hand, the same path is someone else's work, so the tick skips.
	testGitOK(t, repo, "add", "secrets/key.pem")
	if err := os.WriteFile(filepath.Join(repo, "src", "later.txt"), []byte("later"), 0644); err != nil {
		t.Fatal(err)
	}
	result = Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "skipped" {
		t.Fatalf("expected skipped (staged magic-excluded path), got %s: %s", result.State, result.Error)
	}

	clone3 := gitClone(t, remote)
	defer os.RemoveAll(clone3)
	if _, err := os.Stat(filepath.Join(clone3, "secrets", "key.pem")); err == nil {
		t.Error("a hand-staged magic-excluded path must not be committed")
	}
}

// A malformed include pathspec is reachable input: `finalizeConfig` validates
// exclude paths, not include ones. The tick must report stuck rather than carry
// on with an empty staging set, which would stop the scope committing silently.
func TestSyncMalformedIncludeSticks(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	if err := os.MkdirAll(filepath.Join(repo, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	bad := []string{":(typo)src/"}
	writeTestConfig(t, repo, "read-write", "main", "0s", bad, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "origin", "main")

	if err := os.WriteFile(filepath.Join(repo, "src", "keep.txt"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	// Stage it so the guard parses the scope: with an empty index the guard
	// short-circuits and a malformed spec only surfaces at the `git add`.
	testGitOK(t, repo, "add", "src/keep.txt")

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
		Include: config.IncludeConfig{Paths: bad},
	}

	result := Sync(context.Background(), repo, cfg, stateDir)
	if result.State != "stuck" {
		t.Fatalf("expected stuck for an unparseable include pathspec, got %s: %s", result.State, result.Error)
	}
	if !strings.Contains(result.Error, "outside the write scope") {
		t.Errorf("Error = %q, want it to name the scope probe", result.Error)
	}
	// A probe failure is a stuck episode, not a quiet skip. The agent gets the same
	// chance it had when this input failed at the `git add`.
	if _, err := os.Stat(filepath.Join(repo, ".gittend.stuck")); err != nil {
		t.Errorf("stuck flag should be written for a probe failure: %v", err)
	}

	clone2 := gitClone(t, remote)
	defer os.RemoveAll(clone2)
	if _, err := os.Stat(filepath.Join(clone2, "src", "keep.txt")); err == nil {
		t.Error("nothing should be committed when the scope cannot be read")
	}
}
