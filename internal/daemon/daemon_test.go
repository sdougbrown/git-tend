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
	"time"

	"github.com/sdougbrown/git-tend/internal/config"
	"github.com/sdougbrown/git-tend/internal/status"
	gitSync "github.com/sdougbrown/git-tend/internal/sync"
	"golang.org/x/sys/unix"
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

// agentEntry parses agentTOML (a [repo.agent] table) as a user-level [[repo]]
// entry for repoDir, so tests configure the agent the way production does —
// through the user config, never the repo's .gittend.
func agentEntry(t *testing.T, repoDir, agentTOML string) []config.RepoEntry {
	t.Helper()
	path := filepath.Join(t.TempDir(), "user-config.toml")
	doc := fmt.Sprintf("[[repo]]\npath = %q\n%s", repoDir, agentTOML)
	if err := os.WriteFile(path, []byte(doc), 0644); err != nil {
		t.Fatal(err)
	}
	uc, err := config.ParseUserConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	return uc.Repos
}

// setupStuckAgentRepo builds a read-write repo with a failing pre-commit
// hook and an agent configured to fail (exit 1), plus a daemon watching it.
// The first tick marks the repo stuck (hook_failed) and runs the failing
// agent. Returns the repo path, the agent's marker file, the pre-commit
// hook path, the daemon's state dir, and the daemon.
func setupStuckAgentRepo(t *testing.T) (repoDir, marker, hookPath, stateDir string, d *Daemon) {
	t.Helper()

	tempRoot := t.TempDir()
	remote := setupBareRemote(t)
	repoDir = filepath.Join(tempRoot, "agent-repo")

	initRepoWithRemote(t, repoDir, remote, "read-write", "0s", "work.txt")

	marker = filepath.Join(t.TempDir(), "marker")

	hookPath = filepath.Join(repoDir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.UserConfig{
		Roots:          []string{tempRoot},
		Interval:       "1s",
		ScanDepth:      2,
		NetworkTimeout: "5s",
	}
	// The agent command is user-level only, so it goes into the daemon's
	// user config, not the repo's .gittend.
	cfg.Repos = agentEntry(t, repoDir, fmt.Sprintf("[repo.agent]\non_stuck_cmd = \"echo ran >> %s; exit 1\"\n", marker))
	stateDir = t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d = New(cfg, filepath.Join(stateDir, "config.toml"), stateDir, logger)
	d.rescanRoots()

	return repoDir, marker, hookPath, stateDir, d
}

func TestDaemonTickRunsAgentAndResetsOnOk(t *testing.T) {
	repoDir, marker, hookPath, stateDir, d := setupStuckAgentRepo(t)

	ctx := context.Background()

	// Tick 1: sync fails at the hook, repo goes stuck, and the agent starts
	// (asynchronously) and fails.
	d.tick(ctx)
	waitAgentBudget(t, stateDir, repoDir, 1)

	sf := status.Read(filepath.Join(stateDir, "status.json"))
	if sf == nil {
		t.Fatal("status file not found after tick 1")
	}
	rs := sf.Repos[repoDir]
	if rs.CurrentState != "stuck" {
		t.Fatalf("tick 1: state = %s (%s), want stuck", rs.CurrentState, rs.LastError)
	}
	if rs.AgentAttempts != 1 || rs.AgentMaxAttempts != 2 {
		t.Errorf("tick 1: agent budget = %d/%d, want 1/2", rs.AgentAttempts, rs.AgentMaxAttempts)
	}
	if n := countMarkerRuns(marker); n != 1 {
		t.Errorf("tick 1: agent command ran %d times, want 1", n)
	}

	// Simulate the agent's own recovery: flag cleared, hook fixed. Both
	// records (attempt and provenance) are deliberately left in place so the
	// successful tick's cleanup is observed to remove them.
	if err := os.Remove(filepath.Join(repoDir, ".gittend.stuck")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	// Tick 2: sync succeeds; the ok branch resets the displayed budget and
	// clears the attempt and provenance records on disk.
	d.tick(ctx)

	sf2 := status.Read(filepath.Join(stateDir, "status.json"))
	rs2 := sf2.Repos[repoDir]
	if rs2.CurrentState != "ok" {
		t.Fatalf("tick 2: state = %s (%s), want ok", rs2.CurrentState, rs2.LastError)
	}
	if rs2.AgentAttempts != 0 || rs2.AgentMaxAttempts != 0 {
		t.Errorf("tick 2: agent budget = %d/%d, want 0/0 after ok", rs2.AgentAttempts, rs2.AgentMaxAttempts)
	}
	if _, err := os.Stat(gitSync.AgentStatePath(stateDir, repoDir)); !os.IsNotExist(err) {
		t.Error("tick 2: a successful sync should remove the attempt record")
	}
	if _, err := os.Stat(gitSync.StuckRecordPath(stateDir, repoDir)); !os.IsNotExist(err) {
		t.Error("tick 2: a successful sync should remove the provenance record")
	}
}

func countMarkerRuns(marker string) int {
	data, err := os.ReadFile(marker)
	if err != nil {
		return 0
	}
	return len(strings.Split(strings.TrimRight(string(data), "\n"), "\n"))
}

// waitAgentBudget polls the on-disk status until the repo's agent budget
// reaches at least wantAttempts. The agent run is asynchronous: its status
// update lands via the run's own writeStatus, after the tick has returned.
func waitAgentBudget(t *testing.T, stateDir, repoDir string, wantAttempts int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if sf := status.Read(filepath.Join(stateDir, "status.json")); sf != nil {
			if got := sf.Repos[repoDir].AgentAttempts; got >= wantAttempts {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for agent budget >= %d on disk", wantAttempts)
}

func TestDaemonTickClearsAgentBudgetWhenAgentDisabled(t *testing.T) {
	repoDir, _, _, stateDir, d := setupStuckAgentRepo(t)

	ctx := context.Background()

	// Tick 1: the hook fails, the repo goes stuck, and the agent starts
	// (asynchronously) and fails.
	d.tick(ctx)
	waitAgentBudget(t, stateDir, repoDir, 1)

	sf := status.Read(filepath.Join(stateDir, "status.json"))
	if sf == nil {
		t.Fatal("status file not found after tick 1")
	}
	rs := sf.Repos[repoDir]
	if rs.CurrentState != "stuck" {
		t.Fatalf("tick 1: state = %s (%s), want stuck", rs.CurrentState, rs.LastError)
	}
	if rs.AgentAttempts != 1 || rs.AgentMaxAttempts != 2 {
		t.Errorf("tick 1: agent budget = %d/%d, want 1/2", rs.AgentAttempts, rs.AgentMaxAttempts)
	}

	// The operator removes the agent from the user config; the repo stays
	// stuck.
	d.userCfg.Repos = nil
	d.rescanRoots()

	// Tick 2: the repo is still stuck, but with the agent disabled the stale
	// budget must not linger — status shows plain stuck, not "agent pending".
	d.tick(ctx)

	sf2 := status.Read(filepath.Join(stateDir, "status.json"))
	rs2 := sf2.Repos[repoDir]
	if rs2.CurrentState != "stuck" {
		t.Fatalf("tick 2: state = %s, want stuck", rs2.CurrentState)
	}
	if rs2.AgentAttempts != 0 || rs2.AgentMaxAttempts != 0 {
		t.Errorf("tick 2: agent budget = %d/%d, want 0/0 after agent disabled", rs2.AgentAttempts, rs2.AgentMaxAttempts)
	}
}

// A repo that configures the agent in its own .gittend gets nothing: the
// whole [agent] section is user-level only, so the daemon resolves no agent
// config, never engages the agent, and the command never runs.
func TestDaemonIgnoresGittendAgentSection(t *testing.T) {
	tempRoot := t.TempDir()
	remote := setupBareRemote(t)
	repoDir := filepath.Join(tempRoot, "agent-repo")

	initRepoWithRemote(t, repoDir, remote, "read-write", "0s", "work.txt")

	marker := filepath.Join(t.TempDir(), "marker")
	// The repo tries to arm the agent itself. The daemon must ignore it.
	gittend := fmt.Sprintf("mode = \"read-write\"\ndebounce = \"0s\"\n\n[agent]\non_stuck_cmd = \"echo ran >> %s; exit 1\"\n", marker)
	if err := os.WriteFile(filepath.Join(repoDir, ".gittend"), []byte(gittend), 0644); err != nil {
		t.Fatal(err)
	}
	testGitOK(t, repoDir, "add", ".gittend")
	testGitOK(t, repoDir, "commit", "-m", "agent config")
	testGitOK(t, repoDir, "push", "-u", "origin", "main")

	hookPath := filepath.Join(repoDir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}

	// User config deliberately carries no [repo.agent] entry.
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

	ctx := context.Background()
	d.tick(ctx)

	// The repo is stuck, but with no agent engaged.
	sf := status.Read(filepath.Join(stateDir, "status.json"))
	if sf == nil {
		t.Fatal("status file not found after tick 1")
	}
	rs := sf.Repos[repoDir]
	if rs.CurrentState != "stuck" {
		t.Fatalf("state = %s (%s), want stuck", rs.CurrentState, rs.LastError)
	}
	if rs.AgentAttempts != 0 || rs.AgentMaxAttempts != 0 {
		t.Errorf("agent budget = %d/%d, want 0/0 (the .gittend [agent] section is ignored)", rs.AgentAttempts, rs.AgentMaxAttempts)
	}

	// Give a wrongly-started agent run time to write its marker.
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("agent command ran although only the .gittend configured it")
	}
}

// A repeat stuck tick must persist the budget transition (1/2 -> 2/2) to the
// on-disk status: the tick only bumps UpdatedAt on the ok->stuck transition,
// so without the bump in maybeRunAgent the merge keeps the stale on-disk
// budget and the display freezes at 1/2.
func TestDaemonTickAgentBudgetReachesCapOnDisk(t *testing.T) {
	repoDir, _, _, stateDir, d := setupStuckAgentRepo(t)

	ctx := context.Background()

	// Tick 1: the hook fails, the repo goes stuck, and the agent starts
	// (asynchronously) and fails.
	d.tick(ctx)
	waitAgentBudget(t, stateDir, repoDir, 1)

	sf := status.Read(filepath.Join(stateDir, "status.json"))
	if sf == nil {
		t.Fatal("status file not found after tick 1")
	}
	rs := sf.Repos[repoDir]
	if rs.CurrentState != "stuck" {
		t.Fatalf("tick 1: state = %s (%s), want stuck", rs.CurrentState, rs.LastError)
	}
	if rs.AgentAttempts != 1 || rs.AgentMaxAttempts != 2 {
		t.Errorf("tick 1: agent budget = %d/%d, want 1/2", rs.AgentAttempts, rs.AgentMaxAttempts)
	}

	// Tick 2: the repo is still stuck; the agent runs its final attempt and
	// fails. The 1/2 -> 2/2 transition must reach the on-disk status, or the
	// display freezes at 1/2 and "agent exhausted" never shows.
	d.tick(ctx)
	waitAgentBudget(t, stateDir, repoDir, 2)

	sf2 := status.Read(filepath.Join(stateDir, "status.json"))
	rs2 := sf2.Repos[repoDir]
	if rs2.CurrentState != "stuck" {
		t.Fatalf("tick 2: state = %s, want stuck", rs2.CurrentState)
	}
	if rs2.AgentAttempts != 2 || rs2.AgentMaxAttempts != 2 {
		t.Errorf("tick 2: agent budget on disk = %d/%d, want 2/2", rs2.AgentAttempts, rs2.AgentMaxAttempts)
	}
}

// The agent run must not block the serial per-repo tick loop: the tick that
// starts a slow agent command returns promptly, and a later tick for a
// different repo proceeds without waiting for the agent.
func TestDaemonTickAgentRunDoesNotBlockTick(t *testing.T) {
	tempRoot := t.TempDir()
	remote := setupBareRemote(t)

	// agent-repo: read-write with a failing pre-commit hook and a slow
	// agent that blocks until the release file exists (capped at 10s), then
	// exits 1.
	agentRepo := filepath.Join(tempRoot, "agent-repo")
	initRepoWithRemote(t, agentRepo, remote, "read-write", "0s", "work.txt")

	marker := filepath.Join(t.TempDir(), "marker")
	release := filepath.Join(t.TempDir(), "release")
	slowAgent := fmt.Sprintf("i=0; while [ ! -e %s ] && [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done; echo ran >> %s; exit 1", release, marker)
	agentCfg := fmt.Sprintf("mode = \"read-write\"\ndebounce = \"0s\"\n\n[agent]\non_stuck_cmd = %q\n", slowAgent)
	if err := os.WriteFile(filepath.Join(agentRepo, ".gittend"), []byte(agentCfg), 0644); err != nil {
		t.Fatal(err)
	}
	testGitOK(t, agentRepo, "add", ".gittend")
	testGitOK(t, agentRepo, "commit", "-m", "agent config")
	testGitOK(t, agentRepo, "push", "-u", "origin", "main")

	hookPath := filepath.Join(agentRepo, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}

	// other-repo: a healthy read-write repo that must not be delayed by the
	// agent run. Its own bare remote: both repos push to main.
	otherRepo := filepath.Join(tempRoot, "other-repo")
	initRepoWithRemote(t, otherRepo, setupBareRemote(t), "read-write", "0s", "")

	cfg := &config.UserConfig{
		Roots:          []string{tempRoot},
		Interval:       "1s",
		ScanDepth:      2,
		NetworkTimeout: "5s",
	}
	cfg.Repos = agentEntry(t, agentRepo, fmt.Sprintf("[repo.agent]\non_stuck_cmd = %q\n", slowAgent))
	stateDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := New(cfg, filepath.Join(stateDir, "config.toml"), stateDir, logger)
	d.rescanRoots()

	if len(d.repos) != 2 {
		t.Fatalf("expected 2 repos after rescan, got %d", len(d.repos))
	}

	ctx := context.Background()
	t.Cleanup(func() {
		os.WriteFile(release, nil, 0644)
		d.agentRuns.Wait()
	})

	// Tick 1: agent-repo goes stuck and starts the slow agent. The tick must
	// return while the agent is still blocked (it cannot finish before the
	// release file exists, and the 10s cap is far above the bound).
	start := time.Now()
	d.tick(ctx)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("tick 1 took %v, want < 5s (the agent run must not block the tick)", elapsed)
	}
	if countMarkerRuns(marker) != 0 {
		t.Fatal("agent finished before tick 1 returned; the tick may have blocked on it")
	}

	// Tick 2, while the agent is still running: other-repo syncs and the
	// tick returns promptly. The repeat agent run is refused by the per-repo
	// flock and returns immediately.
	start = time.Now()
	d.tick(ctx)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("tick 2 took %v, want < 5s (the in-flight agent must not delay other repos)", elapsed)
	}

	sf := status.Read(filepath.Join(stateDir, "status.json"))
	if sf == nil {
		t.Fatal("status file not found")
	}
	if rs := sf.Repos[otherRepo]; rs.CurrentState != "ok" {
		t.Errorf("other repo state = %s (%s), want ok", rs.CurrentState, rs.LastError)
	}

	// The slow agent's budget lands on disk once it finishes.
	if err := os.WriteFile(release, nil, 0644); err != nil {
		t.Fatal(err)
	}
	waitAgentBudget(t, stateDir, agentRepo, 1)
	d.agentRuns.Wait()
	if err := os.Remove(release); err != nil {
		t.Fatal(err)
	}

	// Tick 3 while no run is in flight: the agent runs again (attempt 2 of
	// 2, blocked on the release file). Tick 4 is refused by the in-flight
	// run; its result reports MaxAttempts 0 (not engaged) and must not wipe
	// the displayed budget back to 1/0.
	d.tick(ctx)
	waitForAgentLockHeld(t, stateDir, agentRepo)
	doneBefore := d.agentRunsDone.Load()
	d.tick(ctx)
	deadline := time.Now().Add(10 * time.Second)
	for d.agentRunsDone.Load() == doneBefore {
		if time.Now().After(deadline) {
			t.Fatal("refused agent run did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	sf = status.Read(filepath.Join(stateDir, "status.json"))
	if sf == nil {
		t.Fatal("status file not found")
	}
	rs := sf.Repos[agentRepo]
	if rs.AgentAttempts != 1 || rs.AgentMaxAttempts != 2 {
		t.Errorf("budget after refused tick = %d/%d, want 1/2 (a refused run must not clobber the budget)", rs.AgentAttempts, rs.AgentMaxAttempts)
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

func TestIsOwnProcess(t *testing.T) {
	if !isOwnProcess(os.Getpid()) {
		t.Error("isOwnProcess(os.Getpid()) should return true")
	}

	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	if isOwnProcess(cmd.Process.Pid) {
		t.Errorf("isOwnProcess(%d) should return false for a non-git-tend process", cmd.Process.Pid)
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
		Roots:     []string{tempRoot},
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

// Disabling the agent while a run is in flight must leave the cleared budget
// alone: the in-flight run's completion re-checks the live config under
// d.mu and defers to the disable path, so the stale result never re-appears
// as "agent pending" on a repo with no agent configured.
func TestDaemonTickAgentDisabledMidRunKeepsBudgetCleared(t *testing.T) {
	repoDir, marker, _, stateDir, d := setupStuckAgentRepo(t)

	// A slow agent command so the run is verifiably in flight across ticks.
	cfg := d.repos[0].Config
	cfg.Agent.OnStuckCmd = fmt.Sprintf("sleep 2; echo ran >> %s; exit 1", marker)

	ctx := context.Background()

	// Tick 1: the repo goes stuck and the slow agent starts.
	d.tick(ctx)

	// Wait until the run holds the agent lock, then disable the agent while
	// it is in flight: rescan with a user config that carries no [repo.agent]
	// entry for the repo.
	waitForAgentLockHeld(t, stateDir, repoDir)
	d.userCfg.Repos = nil
	d.rescanRoots()

	// The next tick runs the disabled-agent clear path (0/0) while the run
	// is still in flight.
	d.tick(ctx)

	// Wait for the in-flight run's completion goroutine to apply (or skip)
	// its result.
	d.agentRuns.Wait()

	sf := status.Read(filepath.Join(stateDir, "status.json"))
	if sf == nil {
		t.Fatal("status file not found")
	}
	rs := sf.Repos[repoDir]
	if rs.AgentAttempts != 0 || rs.AgentMaxAttempts != 0 {
		t.Errorf("agent budget after disable-mid-run = %d/%d, want 0/0 (the stale in-flight result must not re-appear)", rs.AgentAttempts, rs.AgentMaxAttempts)
	}
}

// A repo removed by a rescan while its agent run is in flight must stay
// removed: the completion must not re-add a zero-valued status entry, which
// status renders as "ok".
func TestDaemonAgentCompletionDoesNotResurrectRemovedRepo(t *testing.T) {
	repoDir, marker, _, stateDir, d := setupStuckAgentRepo(t)

	release := filepath.Join(t.TempDir(), "release")
	d.repos[0].Config.Agent.OnStuckCmd = fmt.Sprintf("while [ ! -e %s ]; do sleep 0.05; done; echo ran >> %s; exit 1", release, marker)

	d.tick(context.Background())
	waitForAgentLockHeld(t, stateDir, repoDir)

	d.userCfg.Roots = []string{t.TempDir()}
	d.userCfg.Repos = nil
	d.rescanRoots()
	if _, ok := d.repoStatus[repoDir]; ok {
		t.Fatal("rescan did not remove the repo from repoStatus")
	}

	if err := os.WriteFile(release, nil, 0644); err != nil {
		t.Fatal(err)
	}
	d.agentRuns.Wait()

	d.mu.Lock()
	_, tracked := d.repoStatus[repoDir]
	d.mu.Unlock()
	if tracked {
		t.Error("repoStatus re-added the removed repo after the agent run completed")
	}
	if sf := status.Read(filepath.Join(stateDir, "status.json")); sf != nil {
		if _, ok := sf.Repos[repoDir]; ok {
			t.Error("status file lists the removed repo after the agent run completed")
		}
	}
}

// waitForAgentLockHeld blocks until the agent run holds the per-repo agent
// lock, proven by a non-blocking flock probe failing on a separate fd.
func waitForAgentLockHeld(t *testing.T, stateDir, repoPath string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		probe, err := os.OpenFile(gitSync.AgentLockPath(stateDir, repoPath), os.O_CREATE|os.O_RDONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		err = unix.Flock(int(probe.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		probe.Close()
		if err != nil {
			return // held: the probe failed
		}
		if time.Now().After(deadline) {
			t.Fatal("agent never acquired the lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
