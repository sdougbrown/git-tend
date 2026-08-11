package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/sdougbrown/git-tend/internal/paths"
	"github.com/sdougbrown/git-tend/internal/status"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func setupRunRepo(t *testing.T) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", "--initial-branch=main", remote).CombinedOutput(); err != nil {
		t.Fatalf("creating bare remote: %v\n%s", err, out)
	}

	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "--initial-branch=main")
	runGit(t, repo, "config", "user.email", "test@gittend.local")
	runGit(t, repo, "config", "user.name", "git-tend test")
	runGit(t, repo, "remote", "add", "origin", remote)
	if err := os.WriteFile(filepath.Join(repo, ".gittend"), []byte("mode = \"read-write\"\nsync_branch = \"main\"\ndebounce = \"1h\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".gittend", "work.txt")
	runGit(t, repo, "commit", "-m", "initial")
	runGit(t, repo, "push", "-u", "origin", "main")
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("manual change"), 0644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestRunAfterUnstickBypassesDebounceAndPreservesStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := setupRunRepo(t)
	stateDir := paths.StateDir()
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(stateDir, "status.json")
	stale := status.RepoStatus{
		Mode:         "read-write",
		CurrentState: "stuck",
		UpdatedAt:    time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano),
	}
	if err := status.Write(statusPath, &status.StatusFile{Repos: map[string]status.RepoStatus{repo: stale}}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(repo, ".gittend.stuck"), []byte("stuck"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := removeStuckFlag(repo); err != nil {
		t.Fatalf("unstick: %v", err)
	}
	t.Chdir(repo)
	if err := runRepo(&cobra.Command{}, []string{"."}); err != nil {
		t.Fatalf("run: %v", err)
	}

	got := status.Read(statusPath).Repos[repo]
	if got.CurrentState != "ok" {
		t.Fatalf("status after manual run = %q, want ok (error: %s)", got.CurrentState, got.LastError)
	}
	if got.LastSyncAt == "" {
		t.Fatal("manual run did not record last sync time")
	}

	// This models a daemon tick that began before the manual run and writes its
	// stale in-memory snapshot afterwards.
	if err := status.MergeAndWrite(statusPath, &status.StatusFile{Repos: map[string]status.RepoStatus{repo: stale}}); err != nil {
		t.Fatal(err)
	}
	if got := status.Read(statusPath).Repos[repo].CurrentState; got != "ok" {
		t.Errorf("stale daemon write replaced manual status with %q, want ok", got)
	}

	out, err := exec.Command("git", "-C", repo, "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("manual change was not committed: %s", out)
	}
}
