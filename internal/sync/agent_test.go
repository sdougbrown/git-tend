package sync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sdougbrown/git-tend/internal/config"
)

// writeStuckState mimics a daemon-observed stuck transition: it writes both
// the repo-local flag and the matching daemon-issued provenance record.
func writeStuckState(t *testing.T, stateDir, repoPath, reason string) {
	t.Helper()
	if err := writeStuck(stateDir, repoPath, reason, "git commit", 1, "hook blew up\n"); err != nil {
		t.Fatal(err)
	}
}

// writeForgedStuckFile writes only the repo-local flag, without a matching
// daemon-issued record — what a malicious repo could do on its own.
func writeForgedStuckFile(t *testing.T, repoPath, reason string) {
	t.Helper()
	content := "stuck_at = \"2026-01-01T00:00:00Z\"\n" +
		"reason = \"" + reason + "\"\n" +
		"last_command = \"git commit\"\n" +
		"last_exit_code = 1\n" +
		"last_output = \"\"\"\nhook blew up\n\"\"\"\n"
	if err := os.WriteFile(filepath.Join(repoPath, ".gittend.stuck"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// rewriteStuckReason rewrites the reason in the repo-local flag, leaving the
// daemon-issued record (and its stuck_at) untouched.
func rewriteStuckReason(t *testing.T, repoPath, reason string) {
	t.Helper()
	path := filepath.Join(repoPath, ".gittend.stuck")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	lines[1] = "reason = \"" + reason + "\""
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		t.Fatal(err)
	}
}

// rewriteStuckAt rewrites the stuck_at in the repo-local flag, leaving the
// daemon-issued record untouched — a flag whose nonce no longer matches any
// daemon-issued record.
func rewriteStuckAt(t *testing.T, repoPath, stuckAt string) {
	t.Helper()
	path := filepath.Join(repoPath, ".gittend.stuck")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	lines[0] = "stuck_at = \"" + stuckAt + "\""
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		t.Fatal(err)
	}
}

func agentTestConfig(cmd string, allowedReasons []string, maxAttempts int, timeout string) *config.Config {
	return &config.Config{
		Mode:       "read-only",
		SyncBranch: "main",
		Agent: config.AgentConfig{
			OnStuckCmd:     cmd,
			AllowedReasons: allowedReasons,
			MaxAttempts:    maxAttempts,
			Timeout:        timeout,
		},
	}
}

// markerCmd returns a command that appends "ran" to markerPath and exits with
// the given code, so tests can observe whether and how often it ran.
func markerCmd(markerPath string, code int) string {
	return "echo ran >> " + markerPath + "; exit " + strconv.Itoa(code)
}

func markerRuns(t *testing.T, markerPath string) int {
	t.Helper()
	data, err := os.ReadFile(markerPath)
	if err != nil {
		return 0
	}
	return len(strings.Split(strings.TrimRight(string(data), "\n"), "\n"))
}

func TestAgentAllowlistFiltering(t *testing.T) {
	repo := t.TempDir()
	stateDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "marker")

	// Default allowlist: rebase_conflict excluded, hook_failed included.
	writeStuckState(t, stateDir, repo, "rebase_conflict")
	res := RunStuckAgent(context.Background(), repo, agentTestConfig(markerCmd(marker, 0), nil, 2, "10s"), stateDir, nil)
	if res.Ran {
		t.Fatal("agent should not run for rebase_conflict under the default allowlist")
	}
	if markerRuns(t, marker) != 0 {
		t.Error("agent command should not have executed")
	}

	writeStuckState(t, stateDir, repo, "hook_failed")
	res = RunStuckAgent(context.Background(), repo, agentTestConfig(markerCmd(marker, 0), nil, 2, "10s"), stateDir, nil)
	if !res.Ran || !res.Recovered {
		t.Fatalf("expected run+recovery for hook_failed, got ran=%v recovered=%v err=%v", res.Ran, res.Recovered, res.Err)
	}

	// A forged reason in the file doesn't matter: the allowlist applies to the
	// reason the daemon observed (rebase_conflict in the record), so a file
	// claiming hook_failed can't smuggle a rebase_conflict episode past it.
	writeStuckState(t, stateDir, repo, "rebase_conflict")
	rewriteStuckReason(t, repo, "hook_failed")
	res = RunStuckAgent(context.Background(), repo, agentTestConfig(markerCmd(marker, 0), nil, 2, "10s"), stateDir, nil)
	if res.Ran {
		t.Fatal("agent should not run when the daemon-observed reason is not allowed, regardless of the file's reason")
	}

	// A forged stuck file with no matching daemon-issued record never runs
	// the agent, whatever reason it claims.
	writeForgedStuckFile(t, repo, "hook_failed")
	res = RunStuckAgent(context.Background(), repo, agentTestConfig(markerCmd(marker, 0), nil, 2, "10s"), stateDir, nil)
	if res.Ran {
		t.Fatal("agent should not run for a forged stuck file with no daemon-issued record")
	}

	// Explicit allowlist: only push_rejected.
	writeStuckState(t, stateDir, repo, "hook_failed")
	res = RunStuckAgent(context.Background(), repo, agentTestConfig(markerCmd(marker, 0), []string{"push_rejected"}, 2, "10s"), stateDir, nil)
	if res.Ran {
		t.Fatal("agent should not run when reason is not in the explicit allowlist")
	}

	// Agent not configured: nothing runs.
	writeStuckState(t, stateDir, repo, "hook_failed")
	res = RunStuckAgent(context.Background(), repo, &config.Config{Mode: "read-only", SyncBranch: "main"}, stateDir, nil)
	if res.Ran {
		t.Fatal("agent should not run without on_stuck_cmd")
	}
}

func TestCorruptAttemptRecordFailsClosed(t *testing.T) {
	repo := t.TempDir()
	stateDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "marker")

	writeStuckState(t, stateDir, repo, "hook_failed")
	recordPath := AgentStatePath(stateDir, repo)
	if err := os.MkdirAll(filepath.Dir(recordPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recordPath, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}

	// Unparseable state must not re-arm the agent from an effectively-fresh
	// counter; the budget is treated as spent until the record is cleared.
	res := RunStuckAgent(context.Background(), repo, agentTestConfig(markerCmd(marker, 0), nil, 2, "10s"), stateDir, nil)
	if res.Ran {
		t.Fatal("agent must not run from an unparseable attempt record")
	}
	if res.MaxAttempts != 2 || res.Attempts != 2 {
		t.Errorf("got MaxAttempts=%d Attempts=%d, want budget reported as spent (2/2)", res.MaxAttempts, res.Attempts)
	}
	if markerRuns(t, marker) != 0 {
		t.Error("agent command should not have executed")
	}
}

func TestAgentMaxAttemptsOnlyWhenEngaged(t *testing.T) {
	repo := t.TempDir()
	stateDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "marker")
	cfg := agentTestConfig(markerCmd(marker, 0), nil, 2, "10s")

	// Agent configured but no stuck file: not engaged, no budget to show.
	if res := RunStuckAgent(context.Background(), repo, cfg, stateDir, nil); res.MaxAttempts != 0 {
		t.Errorf("MaxAttempts = %d with no stuck file, want 0", res.MaxAttempts)
	}

	// Stuck, but the daemon-observed reason is not allowed: not engaged.
	writeStuckState(t, stateDir, repo, "rebase_conflict")
	if res := RunStuckAgent(context.Background(), repo, cfg, stateDir, nil); res.MaxAttempts != 0 {
		t.Errorf("MaxAttempts = %d for a never-allowed reason, want 0", res.MaxAttempts)
	}

	// Engaged: the budget is reported, and the first attempt is spent.
	writeStuckState(t, stateDir, repo, "hook_failed")
	res := RunStuckAgent(context.Background(), repo, cfg, stateDir, nil)
	if res.MaxAttempts != 2 || res.Attempts != 1 {
		t.Errorf("got MaxAttempts=%d Attempts=%d, want 2/1", res.MaxAttempts, res.Attempts)
	}
}

func TestOversizedStuckFileIsIgnored(t *testing.T) {
	repo := t.TempDir()
	stateDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "marker")

	// A repo-controlled stuck file larger than the read cap must be treated as
	// unreadable, not slurped into memory whole.
	big := make([]byte, 2<<20)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(repo, ".gittend.stuck"), big, 0644); err != nil {
		t.Fatal(err)
	}

	res := RunStuckAgent(context.Background(), repo, agentTestConfig(markerCmd(marker, 0), nil, 2, "10s"), stateDir, nil)
	if res.Ran {
		t.Fatal("agent should not run off an oversized stuck file")
	}

	// Same cap for a file that would parse if a truncated prefix were read:
	// a valid stuck_at plus padding past the cap must still be refused.
	padded := `stuck_at = "2026-01-01T00:00:00Z"
reason = "hook_failed"
last_output = """
` + strings.Repeat("a", 2<<20) + `"""
`
	if err := os.WriteFile(filepath.Join(repo, ".gittend.stuck"), []byte(padded), 0644); err != nil {
		t.Fatal(err)
	}
	res = RunStuckAgent(context.Background(), repo, agentTestConfig(markerCmd(marker, 0), nil, 2, "10s"), stateDir, nil)
	if res.Ran {
		t.Fatal("agent should not run off an oversized but parseable stuck file")
	}
}

func TestAgentExitZeroRecovers(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)
	stateDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "marker")

	// The clone of an empty remote has an unborn HEAD; commit the .gittend
	// file so the repo is on main and syncable.
	writeTestConfig(t, repo, "read-only", "main", "", nil, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")
	testGitOK(t, repo, "push", "-u", "origin", "main")

	writeStuckState(t, stateDir, repo, "hook_failed")
	// Pre-spend an attempt so the recovery path's persistence is observable:
	// an exit-0 recovery keeps (and increments) the attempt record rather
	// than resetting the budget — only a successful sync or unstick clears it.
	res := RunStuckAgent(context.Background(), repo, agentTestConfig(markerCmd(marker, 1), nil, 2, "10s"), stateDir, nil)
	if !res.Ran || res.Recovered || res.Attempts != 1 {
		t.Fatalf("expected a failed pre-spending run, got ran=%v recovered=%v attempts=%d", res.Ran, res.Recovered, res.Attempts)
	}

	res = RunStuckAgent(context.Background(), repo, agentTestConfig(markerCmd(marker, 0), nil, 2, "10s"), stateDir, nil)
	if !res.Recovered {
		t.Fatalf("expected recovery, got ran=%v recovered=%v err=%v", res.Ran, res.Recovered, res.Err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gittend.stuck")); !os.IsNotExist(err) {
		t.Error("stuck flag should be removed on exit 0")
	}
	if _, err := os.Stat(AgentStatePath(stateDir, repo)); err != nil {
		t.Fatal("attempt record should survive recovery (budget is not re-armed)")
	}
	data, err := os.ReadFile(AgentStatePath(stateDir, repo))
	if err != nil {
		t.Fatal(err)
	}
	var att agentAttempt
	if err := json.Unmarshal(data, &att); err != nil {
		t.Fatal(err)
	}
	if att.Attempts != 2 {
		t.Errorf("attempt record has Attempts=%d after recovery, want 2 (increment persisted)", att.Attempts)
	}
	if _, err := os.Stat(StuckRecordPath(stateDir, repo)); !os.IsNotExist(err) {
		t.Error("provenance record should be removed on recovery")
	}

	// Recovery means the next sync proceeds and actually succeeds — a TempDir
	// would fail at CurrentBranch and make this assertion vacuously pass.
	cfg := &config.Config{Mode: "read-only", SyncBranch: "main"}
	if result := Sync(context.Background(), repo, cfg, stateDir); result.State != "ok" {
		t.Errorf("sync after agent recovery: got %s (%s), want ok", result.State, result.Error)
	}
}

func TestAgentAttemptCap(t *testing.T) {
	repo := t.TempDir()
	stateDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "marker")

	writeStuckState(t, stateDir, repo, "hook_failed")
	cfg := agentTestConfig(markerCmd(marker, 1), nil, 2, "10s")

	for i := 1; i <= 2; i++ {
		res := RunStuckAgent(context.Background(), repo, cfg, stateDir, nil)
		if !res.Ran {
			t.Fatalf("run %d: expected agent to run", i)
		}
		if res.Recovered {
			t.Fatalf("run %d: command exits 1, should not recover", i)
		}
		if res.Attempts != i {
			t.Fatalf("run %d: Attempts = %d, want %d", i, res.Attempts, i)
		}
		if _, err := os.Stat(filepath.Join(repo, ".gittend.stuck")); err != nil {
			t.Fatalf("run %d: stuck flag should remain after failure", i)
		}
	}

	// Cap reached: no further runs, repo waits for a human.
	res := RunStuckAgent(context.Background(), repo, cfg, stateDir, nil)
	if res.Ran {
		t.Fatal("agent should not run once max_attempts is reached")
	}
	if runs := markerRuns(t, marker); runs != 2 {
		t.Errorf("command ran %d times, want 2", runs)
	}

	// A new episode (a fresh daemon-issued record) does NOT reset the counter:
	// the budget is per repo, and only a successful sync or a human unstick
	// clears it. Otherwise an agent that recovers without fixing anything
	// would re-arm itself on every failed sync.
	writeStuckState(t, stateDir, repo, "hook_failed")
	res = RunStuckAgent(context.Background(), repo, cfg, stateDir, nil)
	if res.Ran {
		t.Fatal("a fresh stuck episode should not re-arm a spent budget")
	}
	if runs := markerRuns(t, marker); runs != 2 {
		t.Errorf("command ran %d times, want 2", runs)
	}

	// A successful sync is what resets the budget (the flag is gone by then,
	// so the repo actually recovers).
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	syncable := gitClone(t, remote)
	defer os.RemoveAll(syncable)
	writeTestConfig(t, syncable, "read-only", "main", "", nil, nil)
	testGitOK(t, syncable, "add", ".gittend")
	testGitOK(t, syncable, "commit", "-m", "initial config")
	testGitOK(t, syncable, "push", "-u", "origin", "main")

	writeStuckState(t, stateDir, syncable, "hook_failed")
	res = RunStuckAgent(context.Background(), syncable, cfg, stateDir, nil)
	if !res.Ran || res.Recovered || res.Attempts != 1 {
		t.Fatalf("expected failed run on the syncable repo, got ran=%v recovered=%v attempts=%d", res.Ran, res.Recovered, res.Attempts)
	}
	if _, err := os.Stat(AgentStatePath(stateDir, syncable)); err != nil {
		t.Fatal("attempt record should exist before the successful sync")
	}

	// Fail-then-succeed in one episode: the recovery keeps the record (only
	// the flag and the provenance record are cleared), and the record is
	// finally removed by the successful sync that follows.
	cfgOK := agentTestConfig("exit 0", nil, 2, "10s")
	res = RunStuckAgent(context.Background(), syncable, cfgOK, stateDir, nil)
	if !res.Ran || !res.Recovered || res.Attempts != 2 {
		t.Fatalf("expected successful recovery, got ran=%v recovered=%v attempts=%d", res.Ran, res.Recovered, res.Attempts)
	}
	if _, err := os.Stat(AgentStatePath(stateDir, syncable)); err != nil {
		t.Fatal("recovery should keep the attempt record (budget is not re-armed)")
	}
	if result := Sync(context.Background(), syncable, &config.Config{Mode: "read-only", SyncBranch: "main"}, stateDir); result.State != "ok" {
		t.Fatalf("sync: got %s (%s), want ok", result.State, result.Error)
	}
	if _, err := os.Stat(AgentStatePath(stateDir, syncable)); !os.IsNotExist(err) {
		t.Error("a successful sync should reset the attempt record")
	}
	writeStuckState(t, stateDir, syncable, "hook_failed")
	res = RunStuckAgent(context.Background(), syncable, cfgOK, stateDir, nil)
	if !res.Ran || res.Attempts != 1 {
		t.Fatalf("expected a fresh budget after a successful sync, got ran=%v attempts=%d", res.Ran, res.Attempts)
	}
}

func TestAgentTimeout(t *testing.T) {
	repo := t.TempDir()
	stateDir := t.TempDir()

	writeStuckState(t, stateDir, repo, "hook_failed")
	cfg := agentTestConfig("sleep 30", nil, 2, "100ms")

	start := time.Now()
	res := RunStuckAgent(context.Background(), repo, cfg, stateDir, nil)
	if !res.Ran {
		t.Fatal("expected agent to run")
	}
	if res.Recovered {
		t.Fatal("timed-out command must not count as recovery")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("timeout not enforced, took %s", elapsed)
	}
	if res.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1 after timeout", res.Attempts)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gittend.stuck")); err != nil {
		t.Error("stuck flag should remain after timeout")
	}
}

func TestAgentTimeoutKillsProcessGroup(t *testing.T) {
	repo := t.TempDir()
	stateDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "marker")

	writeStuckState(t, stateDir, repo, "hook_failed")
	// The command runs under `sh -c`, so the sleep (and the marker write) is
	// a direct child of the shell, not of the daemon. A timeout that kills
	// only the sh wrapper would leave that child running; a process-group
	// kill must not.
	cfg := agentTestConfig("sleep 1; echo ran >> "+marker, nil, 2, "100ms")

	res := RunStuckAgent(context.Background(), repo, cfg, stateDir, nil)
	if !res.Ran {
		t.Fatal("expected agent to run")
	}
	if res.Recovered {
		t.Fatal("timed-out command must not count as recovery")
	}

	// Give any surviving orphan ample time to write the marker.
	time.Sleep(1500 * time.Millisecond)
	if markerRuns(t, marker) != 0 {
		t.Error("shell's child survived the timeout; process group was not killed")
	}
}

// A second RunStuckAgent must not start while one is already in flight for
// the same repo — otherwise a daemon restart could run two agents
// concurrently on the same working tree.
func TestAgentRunLockSerializes(t *testing.T) {
	repo := t.TempDir()
	stateDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "marker")

	writeStuckState(t, stateDir, repo, "hook_failed")
	cfg := agentTestConfig("sleep 1; exit 1", nil, 5, "10s")

	done := make(chan AgentRunResult, 1)
	go func() {
		done <- RunStuckAgent(context.Background(), repo, cfg, stateDir, nil)
	}()
	time.Sleep(200 * time.Millisecond)

	cfgB := agentTestConfig(markerCmd(marker, 0), nil, 5, "10s")
	res := RunStuckAgent(context.Background(), repo, cfgB, stateDir, nil)
	if res.Ran {
		t.Error("second agent run should not start while the first holds the lock")
	}
	if markerRuns(t, marker) != 0 {
		t.Error("second agent command should not have executed")
	}

	first := <-done
	if !first.Ran {
		t.Fatal("expected the first agent run to proceed")
	}

	// Once the lock is released, the next run goes through.
	res = RunStuckAgent(context.Background(), repo, cfgB, stateDir, nil)
	if !res.Ran || !res.Recovered {
		t.Fatalf("expected run+recovery after lock release, got ran=%v recovered=%v err=%v", res.Ran, res.Recovered, res.Err)
	}
}

// The agent run must not hold the per-repo flock: while it runs, another sync
// must be able to acquire the lock. The sync still skips on the stuck flag,
// but only after taking the lock — "locked" would mean the agent held it.
func TestAgentDoesNotHoldSyncLock(t *testing.T) {
	remote := gitInitBare(t)
	defer os.RemoveAll(remote)
	repo := gitClone(t, remote)
	defer os.RemoveAll(repo)

	writeTestConfig(t, repo, "read-only", "main", "", nil, nil)
	testGitOK(t, repo, "add", ".gittend")
	testGitOK(t, repo, "commit", "-m", "initial config")

	stateDir := t.TempDir()
	writeStuckState(t, stateDir, repo, "hook_failed")
	cfg := agentTestConfig("sleep 2; exit 1", nil, 2, "10s")

	done := make(chan AgentRunResult, 1)
	go func() {
		done <- RunStuckAgent(context.Background(), repo, cfg, stateDir, nil)
	}()

	// Give the agent time to enter its command before checking the lock.
	time.Sleep(200 * time.Millisecond)

	cfgSync := &config.Config{Mode: "read-only", SyncBranch: "main"}
	result := Sync(context.Background(), repo, cfgSync, stateDir)
	if result.State == "skipped" && result.Error == "locked" {
		t.Error("sync could not acquire the flock while the agent run was in flight")
	}
	if result.State != "skipped" || result.Error != "stuck flag exists" {
		t.Fatalf("expected skipped (stuck flag exists), got %s: %s", result.State, result.Error)
	}

	res := <-done
	if res.Recovered {
		t.Error("sleep command should not count as recovery")
	}
}

func TestAgentContextOnStdin(t *testing.T) {
	repo := t.TempDir()
	stateDir := t.TempDir()
	stdinFile := filepath.Join(t.TempDir(), "stdin")

	writeStuckState(t, stateDir, repo, "hook_failed")
	cfg := agentTestConfig("cat > "+stdinFile, nil, 2, "10s")

	res := RunStuckAgent(context.Background(), repo, cfg, stateDir, nil)
	if !res.Recovered {
		t.Fatalf("expected recovery, got err=%v", res.Err)
	}

	data, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		"repo = \"" + repo + "\"",
		"sync_branch = \"main\"",
		"reason = \"hook_failed\"",
		"last_command = \"git commit\"",
		"last_exit_code = 1",
		"hook blew up",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("stdin context missing %q, got:\n%s", want, s)
		}
	}
}

// An exit-0 recovery persists the incremented attempt counter, so a new
// stuck episode right after continues the same budget: attempt 2 of 2 runs,
// then the cap is exhausted. Without the persisted increment the agent would
// re-arm a full budget on every episode.
func TestAgentBudgetCarriesOverAfterExitZeroRecovery(t *testing.T) {
	repo := t.TempDir()
	stateDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "marker")
	cfg := agentTestConfig(markerCmd(marker, 0), nil, 2, "10s")

	// Episode 1: the agent "recovers" (exit 0) without fixing anything.
	writeStuckState(t, stateDir, repo, "hook_failed")
	res := RunStuckAgent(context.Background(), repo, cfg, stateDir, nil)
	if !res.Ran || !res.Recovered || res.Attempts != 1 {
		t.Fatalf("episode 1: got ran=%v recovered=%v attempts=%d, want run+recovery at attempt 1", res.Ran, res.Recovered, res.Attempts)
	}

	// Episode 2: a fresh flag + record; the budget must carry over, so this
	// is attempt 2 of 2 (and it exits 0, so it "recovers" again).
	writeStuckState(t, stateDir, repo, "hook_failed")
	res = RunStuckAgent(context.Background(), repo, cfg, stateDir, nil)
	if !res.Ran || !res.Recovered || res.Attempts != 2 {
		t.Fatalf("episode 2: got ran=%v recovered=%v attempts=%d, want run at attempt 2 of 2", res.Ran, res.Recovered, res.Attempts)
	}

	// Budget exhausted: no further runs, whatever the episode.
	writeStuckState(t, stateDir, repo, "hook_failed")
	res = RunStuckAgent(context.Background(), repo, cfg, stateDir, nil)
	if res.Ran {
		t.Fatal("episode 3: agent should not run once the carried-over budget is spent")
	}
	if runs := markerRuns(t, marker); runs != 2 {
		t.Errorf("command ran %d times, want 2", runs)
	}
}

// A flag whose stuck_at doesn't match the daemon-issued record must not run
// the agent, even though a record exists — the provenance match is per
// episode, not just record-presence.
func TestStuckRecordMismatchDoesNotRun(t *testing.T) {
	repo := t.TempDir()
	stateDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "marker")

	writeStuckState(t, stateDir, repo, "hook_failed")
	rewriteStuckAt(t, repo, "2000-01-01T00:00:00Z")

	res := RunStuckAgent(context.Background(), repo, agentTestConfig(markerCmd(marker, 0), nil, 2, "10s"), stateDir, nil)
	if res.Ran {
		t.Fatal("agent must not run when the flag's stuck_at mismatches the record")
	}
	if markerRuns(t, marker) != 0 {
		t.Error("agent command should not have executed")
	}
}

// writeStuck refuses to create the stuck flag through a symlink: the path is
// repo-controlled, and following it would write repo-influenced content to an
// arbitrary target.
func TestWriteStuckRefusesSymlinkedFlag(t *testing.T) {
	repo := t.TempDir()
	stateDir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(repo, ".gittend.stuck")); err != nil {
		t.Fatal(err)
	}

	if err := writeStuck(stateDir, repo, "hook_failed", "git commit", 1, "boom"); err == nil {
		t.Fatal("writeStuck must refuse a symlinked stuck path")
	}
	data, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Errorf("wrote through the symlink: victim now holds %q", data)
	}
}

// writeStuck takes the agent lock, so a write racing an in-flight agent run
// fails instead of interleaving with that run's record/flag cleanup.
func TestWriteStuckRefusesWhileAgentLockHeld(t *testing.T) {
	repo := t.TempDir()
	stateDir := t.TempDir()

	lock, err := AcquireAgentLock(stateDir, repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeStuck(stateDir, repo, "hook_failed", "git commit", 1, "boom"); err == nil {
		t.Error("writeStuck must fail while the agent lock is held elsewhere")
	}
	lock.Close()

	if err := writeStuck(stateDir, repo, "hook_failed", "git commit", 1, "boom"); err != nil {
		t.Errorf("writeStuck should succeed once the lock is released: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gittend.stuck")); err != nil {
		t.Error("stuck flag should exist after a successful writeStuck")
	}
}
