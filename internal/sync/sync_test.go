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
