package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sdougbrown/git-tend/internal/paths"
	gitSync "github.com/sdougbrown/git-tend/internal/sync"
)

// setupStuckRepoWithAgentState creates a repo with a stuck flag and both
// agent state records present, returning the repo and state-dir paths.
func setupStuckRepoWithAgentState(t *testing.T) (repo, stateDir string) {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	stateDir = paths.StateDir()

	repo = t.TempDir()
	testGitOK(t, repo, "init")
	testGitOK(t, repo, "config", "user.email", "test@gittend.local")
	testGitOK(t, repo, "config", "user.name", "git-tend test")
	testGitOK(t, repo, "commit", "--allow-empty", "-m", "init")

	if err := os.WriteFile(filepath.Join(repo, ".gittend.stuck"), []byte("stuck_at = \"now\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		gitSync.AgentStatePath(stateDir, repo),
		gitSync.StuckRecordPath(stateDir, repo),
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(fmt.Sprintf(`{"repo":%q}`, repo)), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return repo, stateDir
}

func TestRemoveStuckFlagResetsAgentState(t *testing.T) {
	repo, stateDir := setupStuckRepoWithAgentState(t)

	if err := removeStuckFlag(repo); err != nil {
		t.Fatalf("removeStuckFlag: %v", err)
	}

	if _, err := os.Stat(filepath.Join(repo, ".gittend.stuck")); !os.IsNotExist(err) {
		t.Error("stuck flag should be removed")
	}
	for name, p := range map[string]string{
		"attempt record": gitSync.AgentStatePath(stateDir, repo),
		"stuck record":   gitSync.StuckRecordPath(stateDir, repo),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should be reset by unstick", name)
		}
	}
}

func TestRemoveStuckFlagRefusesWhileAgentLockHeld(t *testing.T) {
	repo, stateDir := setupStuckRepoWithAgentState(t)

	// Hold the agent lock (simulating an in-flight agent run).
	lock, err := gitSync.AcquireAgentLock(stateDir, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	// removeStuckFlag must fail with a contention error.
	if err := removeStuckFlag(repo); err == nil || !strings.Contains(err.Error(), "agent run in flight") {
		t.Fatalf("removeStuckFlag: want a contention error, got: %v", err)
	}

	// The flag and both records must still exist on disk.
	if _, err := os.Stat(filepath.Join(repo, ".gittend.stuck")); err != nil {
		t.Error("stuck flag should still exist")
	}
	for name, p := range map[string]string{
		"attempt record": gitSync.AgentStatePath(stateDir, repo),
		"stuck record":   gitSync.StuckRecordPath(stateDir, repo),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s should still exist", name)
		}
	}
}

// A repo stuck before the agent feature existed has a stuck flag but no
// agent state or stuck record files. Unstick must still succeed and remove
// the flag, tolerating the absent state files.
func TestRemoveStuckFlagWithoutAgentState(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	repo := t.TempDir()
	testGitOK(t, repo, "init")
	testGitOK(t, repo, "config", "user.email", "test@gittend.local")
	testGitOK(t, repo, "config", "user.name", "git-tend test")
	testGitOK(t, repo, "commit", "--allow-empty", "-m", "init")

	// Only the stuck flag exists; no agent state or stuck record files.
	if err := os.WriteFile(filepath.Join(repo, ".gittend.stuck"), []byte("stuck_at = \"now\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := removeStuckFlag(repo); err != nil {
		t.Fatalf("removeStuckFlag: %v", err)
	}

	if _, err := os.Stat(filepath.Join(repo, ".gittend.stuck")); !os.IsNotExist(err) {
		t.Error("stuck flag should be removed")
	}
}

func testGitOK(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
