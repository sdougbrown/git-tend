package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sdougbrown/git-tend/internal/paths"
	gitSync "github.com/sdougbrown/git-tend/internal/sync"
)

func TestRemoveStuckFlagResetsAgentState(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	stateDir := paths.StateDir()

	repo := t.TempDir()
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

func testGitOK(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
