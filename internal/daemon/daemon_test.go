package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sdougbrown/git-tend/internal/config"
	"github.com/sdougbrown/git-tend/internal/status"
)

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("git %s: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, out, stderr)
	}
	return string(out)
}

func testGitOK(t *testing.T, dir string, args ...string) {
	t.Helper()
	testGit(t, dir, args...)
}

func setupBareRemote(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testGit(t, dir, "init", "--bare", "--initial-branch=main")
	return dir
}

func initRepoWithRemote(t *testing.T, repoDir, remote, mode, debounce, extraFile string) {
	t.Helper()

	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	testGit(t, repoDir, "init", "--initial-branch=main")
	testGit(t, repoDir, "remote", "add", "origin", remote)
	testGit(t, repoDir, "config", "user.email", "test@gittend.local")
	testGit(t, repoDir, "config", "user.name", "git-tend test")

	var configLines string
	if mode != "" {
		configLines += fmt.Sprintf("mode = %q\n", mode)
	}
	if debounce != "" {
		configLines += fmt.Sprintf("debounce = %q\n", debounce)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".gittend"), []byte(configLines), 0644); err != nil {
		t.Fatal(err)
	}
	testGitOK(t, repoDir, "add", ".gittend")
	testGitOK(t, repoDir, "commit", "-m", "add .gittend")
	testGitOK(t, repoDir, "push", "-u", "origin", "main")

	if extraFile != "" {
		if err := os.WriteFile(filepath.Join(repoDir, extraFile), []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPIDRefusal(t *testing.T) {
	stateDir := t.TempDir()
	pidPath := filepath.Join(stateDir, "daemon.pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.UserConfig{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	d := New(cfg, filepath.Join(stateDir, "config.toml"), stateDir, logger)

	err := d.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("expected 'already running' error, got: %v", err)
	}
}

func TestDaemonTickIntegration(t *testing.T) {
	tempRoot := t.TempDir()

	remoteRW := setupBareRemote(t)
	remoteRO := setupBareRemote(t)

	repoRW := filepath.Join(tempRoot, "rw-repo")
	repoRO := filepath.Join(tempRoot, "ro-repo")

	initRepoWithRemote(t, repoRW, remoteRW, "read-write", "0s", "work.txt")
	initRepoWithRemote(t, repoRO, remoteRO, "read-only", "", "")

	cfg := &config.UserConfig{
		Roots:          []string{tempRoot},
		Interval:       "1s",
		ScanDepth:      2,
		NetworkTimeout: "5s",
	}
	stateDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := New(cfg, filepath.Join(stateDir, "config.toml"), stateDir, logger)
	d.rescanRoots()

	if len(d.repos) != 2 {
		t.Fatalf("expected 2 repos after rescan, got %d", len(d.repos))
	}

	ctx := context.Background()

	d.tick(ctx)

	sf := status.Read(filepath.Join(stateDir, "status.json"))
	if sf == nil {
		t.Fatal("status file not found after first tick")
	}

	rsRW, ok := sf.Repos[repoRW]
	if !ok {
		t.Error("rw repo not found in status")
	} else {
		if rsRW.CurrentState != "ok" {
			t.Errorf("rw repo state: got %q, want ok (error: %s)", rsRW.CurrentState, rsRW.LastError)
		}
		if rsRW.LastSyncAt == "" {
			t.Error("rw repo LastSyncAt not set")
		}
		if rsRW.Mode != "read-write" {
			t.Errorf("rw repo mode: got %q, want read-write", rsRW.Mode)
		}
	}

	rsRO, ok := sf.Repos[repoRO]
	if !ok {
		t.Error("ro repo not found in status")
	} else {
		if rsRO.CurrentState != "ok" {
			t.Errorf("ro repo state: got %q, want ok (error: %s)", rsRO.CurrentState, rsRO.LastError)
		}
		if rsRO.LastSyncAt == "" {
			t.Error("ro repo LastSyncAt not set")
		}
		if rsRO.Mode != "read-only" {
			t.Errorf("ro repo mode: got %q, want read-only", rsRO.Mode)
		}
	}

	d.tick(ctx)

	sf2 := status.Read(filepath.Join(stateDir, "status.json"))
	if sf2 == nil {
		t.Fatal("status file not found after second tick")
	}

	rsRW2 := sf2.Repos[repoRW]
	if rsRW2.PriorState != "ok" {
		t.Errorf("rw repo PriorState after second tick: got %q, want ok", rsRW2.PriorState)
	}
	if rsRW2.CurrentState != "ok" {
		t.Errorf("rw repo CurrentState after second tick: got %q, want ok", rsRW2.CurrentState)
	}

	rsRO2 := sf2.Repos[repoRO]
	if rsRO2.PriorState != "ok" {
		t.Errorf("ro repo PriorState after second tick: got %q, want ok", rsRO2.PriorState)
	}
	if rsRO2.CurrentState != "ok" {
		t.Errorf("ro repo CurrentState after second tick: got %q, want ok", rsRO2.CurrentState)
	}

	d.tick(ctx)

	sf3 := status.Read(filepath.Join(stateDir, "status.json"))
	if sf3 == nil {
		t.Fatal("status file not found after third tick")
	}
	rsRW3 := sf3.Repos[repoRW]
	if rsRW3.PriorState != "ok" {
		t.Errorf("rw repo PriorState after third tick: got %q, want ok", rsRW3.PriorState)
	}
	if rsRW3.CurrentState != "ok" {
		t.Errorf("rw repo CurrentState after third tick: got %q, want ok", rsRW3.CurrentState)
	}
}

func TestRescanRoots(t *testing.T) {
	tempRoot := t.TempDir()

	repoDir1 := filepath.Join(tempRoot, "repo1")
	if err := os.MkdirAll(filepath.Join(repoDir1, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir1, ".gittend"), []byte("mode = \"read-only\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.UserConfig{
		Roots:    []string{tempRoot},
		ScanDepth: 2,
	}
	stateDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	d := New(cfg, filepath.Join(stateDir, "config.toml"), stateDir, logger)

	d.rescanRoots()
	if len(d.repos) != 1 {
		t.Fatalf("expected 1 repo after first rescan, got %d", len(d.repos))
	}

	repoDir2 := filepath.Join(tempRoot, "repo2")
	if err := os.MkdirAll(filepath.Join(repoDir2, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir2, ".gittend"), []byte("mode = \"read-only\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	d.rescanRoots()
	if len(d.repos) != 2 {
		t.Fatalf("expected 2 repos after second rescan, got %d", len(d.repos))
	}

	if err := os.RemoveAll(repoDir1); err != nil {
		t.Fatal(err)
	}

	d.rescanRoots()
	if len(d.repos) != 1 {
		t.Fatalf("expected 1 repo after third rescan (repo1 removed), got %d", len(d.repos))
	}
	if d.repos[0].Path != repoDir2 {
		t.Errorf("expected repo2 to remain, got %s", d.repos[0].Path)
	}
}

func TestOfflineBackoff(t *testing.T) {
	tempRoot := t.TempDir()
	repoDir := filepath.Join(tempRoot, "offline-repo")

	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	testGit(t, repoDir, "init", "--initial-branch=main")
	testGit(t, repoDir, "config", "user.email", "test@gittend.local")
	testGit(t, repoDir, "config", "user.name", "git-tend test")
	testGit(t, repoDir, "remote", "add", "origin", "http://127.0.0.1:1/repo.git")

	if err := os.WriteFile(filepath.Join(repoDir, ".gittend"), []byte("mode = \"read-only\"\ninterval = \"1s\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testGitOK(t, repoDir, "add", ".gittend")
	testGitOK(t, repoDir, "commit", "-m", "initial")

	cfg := &config.UserConfig{
		Roots:             []string{tempRoot},
		Interval:          "100ms",
		NetworkTimeout:    "1s",
		OfflineBackoffCap: "5s",
		ScanDepth:         2,
	}
	stateDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := New(cfg, filepath.Join(stateDir, "config.toml"), stateDir, logger)
	d.rescanRoots()

	if len(d.repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(d.repos))
	}

	ctx := context.Background()

	d.tick(ctx)

	sf := status.Read(filepath.Join(stateDir, "status.json"))
	if sf == nil {
		t.Fatal("status file not found")
	}
	rs := sf.Repos[repoDir]
	if rs.CurrentState != "offline" {
		t.Errorf("tick 1: expected offline, got %s (error: %s)", rs.CurrentState, rs.LastError)
	}
	if rs.ConsecutiveOfflineFailures != 1 {
		t.Errorf("tick 1: expected 1 failure, got %d", rs.ConsecutiveOfflineFailures)
	}
	if rs.OfflineSince == "" {
		t.Error("tick 1: OfflineSince not set")
	}

	d.tick(ctx)

	sf2 := status.Read(filepath.Join(stateDir, "status.json"))
	rs2 := sf2.Repos[repoDir]
	if rs2.ConsecutiveOfflineFailures != 2 {
		t.Errorf("tick 2: expected 2 failures, got %d", rs2.ConsecutiveOfflineFailures)
	}
	if rs2.CurrentState != "offline" {
		t.Errorf("tick 2: expected offline, got %s", rs2.CurrentState)
	}

	d.tick(ctx)

	sf3 := status.Read(filepath.Join(stateDir, "status.json"))
	rs3 := sf3.Repos[repoDir]
	if rs3.ConsecutiveOfflineFailures != 3 {
		t.Errorf("tick 3: expected 3 failures, got %d", rs3.ConsecutiveOfflineFailures)
	}

	off := d.offline[repoDir]
	if off == nil {
		t.Fatal("offline state not tracked")
	}
	if off.nextAttemptAt.IsZero() {
		t.Error("tick 3: expected nextAttemptAt to be set (backoff applied)")
	}

	if off.consecutiveFailures != 3 {
		t.Errorf("expected 3 consecutive failures in daemon state, got %d", off.consecutiveFailures)
	}
}
