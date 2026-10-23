package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/sys/unix"

	"github.com/sdougbrown/git-tend/internal/commit"
	"github.com/sdougbrown/git-tend/internal/config"
	"github.com/sdougbrown/git-tend/internal/git"
)

type SyncResult struct {
	State  string
	Error  string
	Ahead  int
	Behind int
}

func Sync(ctx context.Context, repoPath string, cfg *config.Config, stateDir string) SyncResult {
	return sync(ctx, repoPath, cfg, stateDir, false)
}

// SyncManual runs an explicit user-requested sync. Unlike periodic daemon
// syncs, it does not defer read-write work for the debounce interval.
func SyncManual(ctx context.Context, repoPath string, cfg *config.Config, stateDir string) SyncResult {
	return sync(ctx, repoPath, cfg, stateDir, true)
}

func sync(ctx context.Context, repoPath string, cfg *config.Config, stateDir string, bypassDebounce bool) SyncResult {
	hash := sha256.Sum256([]byte(repoPath))
	lockDir := filepath.Join(stateDir, "locks")
	lockPath := filepath.Join(lockDir, hex.EncodeToString(hash[:])+".lock")

	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return SyncResult{State: "stuck", Error: fmt.Sprintf("creating lock dir: %v", err)}
	}

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return SyncResult{State: "stuck", Error: fmt.Sprintf("opening lock file: %v", err)}
	}
	defer lockFile.Close()

	fd := int(lockFile.Fd())
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			return SyncResult{State: "skipped", Error: "locked"}
		}
		return SyncResult{State: "stuck", Error: fmt.Sprintf("locking: %v", err)}
	}
	defer unix.Flock(fd, unix.LOCK_UN)

	branch, err := git.CurrentBranch(repoPath)
	if err != nil {
		return SyncResult{State: "stuck", Error: fmt.Sprintf("getting current branch: %v", err)}
	}
	if branch != cfg.SyncBranch {
		return SyncResult{State: "skipped", Error: "not on sync branch"}
	}

	if _, err := os.Stat(filepath.Join(repoPath, ".gittend.stuck")); err == nil {
		return SyncResult{State: "skipped", Error: "stuck flag exists"}
	}

	timeout := parseIntervalTimeout(cfg.Interval)

	var result SyncResult
	if cfg.Mode == "read-only" {
		result = syncReadOnly(ctx, repoPath, stateDir, timeout)
	} else {
		result = syncReadWrite(ctx, repoPath, cfg, stateDir, timeout, bypassDebounce, branch)
	}
	if result.State == "ok" {
		clearAgentState(stateDir, repoPath)
	}
	return result
}

// clearAgentState drops the agent state once a stuck episode is truly over: a
// successful sync means the flag is gone and the repo syncs, so the attempt
// budget and the daemon-issued provenance record are both reset. Errors are
// ignored: stale state fails safe — the agent just won't run.
//
// The reset takes the agent lock so it doesn't interleave with a daemon-side
// run's final attempt write. The lock only coordinates daemon callers; the
// agent command itself is not constrained by it. The lock is only try-locked:
// waiting would stall sync for the agent's whole timeout. When a run holds the
// lock the reset is skipped, which only keeps the budget conservative; the
// next successful sync performs it.
//
// With no agent state on disk there is nothing to reset, so the lock is not
// taken and no agent state directory is created.
func clearAgentState(stateDir, repoPath string) {
	_, attErr := os.Stat(AgentStatePath(stateDir, repoPath))
	_, recErr := os.Stat(StuckRecordPath(stateDir, repoPath))
	if os.IsNotExist(attErr) && os.IsNotExist(recErr) {
		return
	}
	lock, err := AcquireAgentLock(stateDir, repoPath)
	if err != nil {
		return
	}
	defer lock.Close()
	_ = os.Remove(AgentStatePath(stateDir, repoPath))
	_ = os.Remove(StuckRecordPath(stateDir, repoPath))
}

func parseIntervalTimeout(interval string) time.Duration {
	if d, err := time.ParseDuration(interval); err == nil {
		return d
	}
	return 30 * time.Second
}

func parseDebounce(debounce string) time.Duration {
	if d, err := time.ParseDuration(debounce); err == nil {
		return d
	}
	return 30 * time.Second
}

func syncReadOnly(ctx context.Context, repoPath, stateDir string, timeout time.Duration) SyncResult {
	_, stderr, err, isNet := git.Fetch(ctx, repoPath, timeout)
	if isNet {
		return SyncResult{State: "offline", Error: err.Error()}
	}
	if err != nil {
		return stuckResult(err, writeStuck(stateDir, repoPath, "other", "git fetch", exitCodeFromErr(err), stderr), "fetch")
	}

	stdout, stderr, err, isNet := git.PullRebase(ctx, repoPath, timeout)
	if isNet {
		return SyncResult{State: "offline", Error: err.Error()}
	}
	if err != nil {
		git.RebaseAbort(repoPath)
		return stuckResult(err, writeStuck(stateDir, repoPath, "rebase_conflict", "git pull --rebase", exitCodeFromErr(err), combineOutput(stdout, stderr)), "pull")
	}

	return SyncResult{State: "ok"}
}

func syncReadWrite(ctx context.Context, repoPath string, cfg *config.Config, stateDir string, timeout time.Duration, bypassDebounce bool, branch string) SyncResult {
	debounceDur := parseDebounce(cfg.Debounce)

	// Back off if git appears busy, so we don't tend the repo out from under a
	// concurrent manual operation. Locks appear while a git command is
	// mid-write; a recently-written COMMIT_EDITMSG means the user is likely
	// composing a commit message in an editor right now (git holds no lock
	// during that phase).
	if locks, err := git.ActiveLocks(repoPath); err != nil {
		return SyncResult{State: "stuck", Error: fmt.Sprintf("checking for active git locks: %v", err)}
	} else if locks {
		return SyncResult{State: "skipped", Error: "git lock present (concurrent operation)"}
	}
	if cfg.Commit.InProgressWindow != "" {
		window := parseDebounce(cfg.Commit.InProgressWindow)
		if recent, err := git.CommitEditRecent(repoPath, window); err != nil {
			return SyncResult{State: "stuck", Error: fmt.Sprintf("checking commit in progress: %v", err)}
		} else if recent {
			return SyncResult{State: "skipped", Error: "commit in progress"}
		}
	}

	// In folder-scoped write mode git-tend adds only the [include] paths minus
	// the [exclude] paths, but commits the whole index. Staged work outside that
	// combination belongs to a commit someone else is preparing. Leave the repo
	// alone for this tick, and resume once the index clears. Staged work inside
	// the scope is indistinguishable from git-tend's own, and is fair game. An
	// [exclude] with no [include] still narrows the staging, so it is checked on
	// its own.
	invertedScope := translateExcludePaths(cfg.Include.Paths)
	stagedOutsideScope := func() (bool, error) {
		if len(invertedScope) == 0 && len(cfg.Exclude.Paths) == 0 {
			return false, nil
		}
		return git.StagedOutsideScope(repoPath, invertedScope, cfg.Exclude.Paths)
	}

	if outside, err := stagedOutsideScope(); err != nil {
		return SyncResult{State: "stuck", Error: fmt.Sprintf("checking for staged paths outside the write scope: %v", err)}
	} else if outside {
		return SyncResult{State: "skipped", Error: "staged changes outside the write scope"}
	}

	files, err := git.ListTrackedFiles(repoPath)
	if err != nil {
		return SyncResult{State: "stuck", Error: fmt.Sprintf("listing tracked files: %v", err)}
	}

	now := time.Now()
	var maxMtime time.Time
	for _, f := range files {
		if f == "" {
			continue
		}
		fi, statErr := os.Stat(filepath.Join(repoPath, f))
		if statErr != nil {
			continue
		}
		if fi.ModTime().After(maxMtime) {
			maxMtime = fi.ModTime()
		}
	}
	if !bypassDebounce && !maxMtime.IsZero() && now.Sub(maxMtime) < debounceDur {
		return SyncResult{State: "skipped", Error: "debounce"}
	}

	if err := git.Add(repoPath, append([]string{"--"}, stagedSpecs(cfg)...)...); err != nil {
		return stuckResult(err, writeStuck(stateDir, repoPath, "other", "git add", exitCodeFromErr(err), err.Error()), "staging")
	}

	diff, err := git.DiffCachedNameStatus(repoPath)
	if err != nil {
		return stuckResult(err, writeStuck(stateDir, repoPath, "other", "git diff --cached", exitCodeFromErr(err), err.Error()), "diff cached")
	}

	if strings.TrimSpace(diff) != "" {
		modelTimeout := 30 * time.Second
		if cfg.Commit.ModelTimeout != "" {
			if d, err := time.ParseDuration(cfg.Commit.ModelTimeout); err == nil {
				modelTimeout = d
			}
		}

		// When a prompt is composed, the model gets the name-status diff
		// followed by the prompt. Without a prompt the model command is
		// treated as self-contained (e.g. it carries its own inline prompt)
		// and receives the full diff, as before.
		modelStdin := ""
		prompt := cfg.Commit.ModelPrompt

		var msg string
		switch {
		case cfg.Commit.Strategy == "agent" && cfg.Commit.ModelCmd != "":
			// The agent gets the full staged diff, repo path, and branch, and
			// authors a possibly multi-line message; the daemon still runs git
			// commit itself, so every guard above (flock, debounce,
			// in_progress_window, lock back-off) stays in the loop. The quiet-
			// time guarantee is not violated by a long agent run here: the
			// debounce check ran before staging and the diff we commit is the
			// one staged at check time, and the per-repo flock is held for the
			// whole sync, so no concurrent git-tend work can interleave.
			if prompt == "" {
				prompt = commit.DefaultAgentPrompt
			}
			// Cap the read at the source so a large staged diff cannot buffer
			// unbounded bytes in the daemon. The +1 keeps an oversized diff
			// strictly over MaxAgentDiffBytes so truncateDiff's truncation note
			// still reaches the agent.
			fullDiff, fullErr := git.DiffCachedMax(repoPath, commit.MaxAgentDiffBytes+1)
			if fullErr != nil {
				msg = commit.Generate(diff, cfg.Commit.Emoji, cfg.Commit.FallbackThresh)
				break
			}
			msg = commit.GenerateAgent(diff, fullDiff, repoPath, branch, prompt, cfg.Commit.Emoji, cfg.Commit.FallbackThresh, cfg.Commit.ModelCmd, modelTimeout)
		case cfg.Commit.Strategy == "model" && cfg.Commit.ModelCmd != "":
			if prompt == "" {
				prompt = commit.DefaultModelPrompt
			}
			modelStdin = diff + "\n\n" + prompt
			msg = commit.GenerateModelFirst(diff, cfg.Commit.Emoji, cfg.Commit.FallbackThresh, cfg.Commit.ModelCmd, modelTimeout, modelStdin)
		case cfg.Commit.ModelCmd != "":
			if prompt != "" {
				modelStdin = diff + "\n\n" + prompt
			} else {
				fullDiff, fullErr := git.DiffCached(repoPath)
				if fullErr != nil {
					msg = commit.Generate(diff, cfg.Commit.Emoji, cfg.Commit.FallbackThresh)
					break
				}
				modelStdin = fullDiff
			}
			msg = commit.GenerateWithModel(diff, cfg.Commit.Emoji, cfg.Commit.FallbackThresh, cfg.Commit.ModelCmd, modelTimeout, modelStdin)
		default:
			msg = commit.Generate(diff, cfg.Commit.Emoji, cfg.Commit.FallbackThresh)
		}
		// A model or agent run can hold the tick open for model_timeout, so
		// staging that happens during it is invisible to the tick-start check.
		// Re-check immediately before committing: the commit takes the whole
		// index, so one staged out-of-scope path can end up in it.
		if outside, err := stagedOutsideScope(); err != nil {
			return SyncResult{State: "stuck", Error: fmt.Sprintf("checking for staged paths outside the write scope: %v", err)}
		} else if outside {
			// This tick already staged its own scope, and a skip must not leave
			// that behind: the human's commit would sweep it in under their
			// message. Unstage the pathspecs this tick staged, never the whole
			// index. In-scope work a human staged returns to the working tree,
			// and the next tick stages it again.
			if err := git.Unstage(repoPath, stagedSpecs(cfg)...); err != nil {
				return SyncResult{State: "stuck", Error: fmt.Sprintf("unstaging after the write-scope re-check: %v", err)}
			}
			return SyncResult{State: "skipped", Error: "staged changes outside the write scope"}
		}
		if err := git.Commit(repoPath, msg, cfg.Commit.NoVerify); err != nil {
			return stuckResult(err, writeStuck(stateDir, repoPath, "hook_failed", "git commit", exitCodeFromErr(err), err.Error()), "commit")
		}
		// The sentinel git writes for manual commits is only meaningful for
		// human activity; clear it after our own commit so we don't back off
		// against ourselves on the next tick.
		_ = git.ClearCommitEditMsg(repoPath)
	}

	stdout, stderr, err, isNet := git.PullRebase(ctx, repoPath, timeout)
	if isNet {
		return SyncResult{State: "offline", Error: err.Error()}
	}
	if err != nil {
		git.RebaseAbort(repoPath)
		return stuckResult(err, writeStuck(stateDir, repoPath, "rebase_conflict", "git pull --rebase", exitCodeFromErr(err), combineOutput(stdout, stderr)), "pull")
	}

	stdout, stderr, err, isNet = git.Push(ctx, repoPath, timeout)
	if isNet {
		return SyncResult{State: "offline", Error: err.Error()}
	}
	if err != nil {
		return stuckResult(err, writeStuck(stateDir, repoPath, "push_rejected", "git push", exitCodeFromErr(err), combineOutput(stdout, stderr)), "push")
	}

	return SyncResult{State: "ok"}
}

// stagedSpecs returns the pathspecs git-tend stages: the [include] paths, or the
// whole tree when unset, minus the [exclude] paths. The stuck sentinel is never
// staged. An unstage after a skip uses this same list, so it removes exactly
// what the tick staged and nothing a human did.
func stagedSpecs(cfg *config.Config) []string {
	specs := []string{"."}
	if len(cfg.Include.Paths) > 0 {
		specs = append([]string{}, cfg.Include.Paths...)
	}
	specs = append(specs, ":!.gittend.stuck")
	return append(specs, translateExcludePaths(cfg.Exclude.Paths)...)
}

func translateExcludePaths(paths []string) []string {
	specs := make([]string, len(paths))
	for i, p := range paths {
		if strings.HasPrefix(p, ":(") {
			specs[i] = ":(" + "exclude," + p[2:]
		} else {
			specs[i] = ":(exclude)" + p
		}
	}
	return specs
}

// writeStuck writes the repo-local .gittend.stuck flag and, alongside it, a
// daemon-issued provenance record in the state dir (see stuckRecord). The
// record is what authorizes the agent to act on the flag. The record is
// written FIRST: a crash between the two writes then leaves a record with no
// flag — the agent has no flag to act on, which is the fail-safe direction.
// Both writes happen under the agent lock so an in-flight agent run's
// cleanup can never interleave with (and mis-delete) a fresh episode's record.
func writeStuck(stateDir, repoPath, reason, lastCommand string, exitCode int, output string) error {
	lock, err := AcquireAgentLock(stateDir, repoPath)
	if err != nil {
		return fmt.Errorf("agent run in flight, not recording stuck state: %w", err)
	}
	defer lock.Close()

	// Nanosecond precision: stuck_at doubles as the nonce the provenance
	// record is matched against, so two episodes must not collide.
	stuckAt := time.Now().UTC().Format(time.RFC3339Nano)
	if err := writeStuckRecord(stateDir, repoPath, stuckAt, reason); err != nil {
		return err
	}

	if len(output) > 4096 {
		output = output[len(output)-4096:]
	}

	path := filepath.Join(repoPath, ".gittend.stuck")
	// O_NOFOLLOW: the path is repo-controlled, so a symlink planted there
	// must not turn this into a write through to an arbitrary target (the
	// output is repo-influenced, up to 4KiB).
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|unix.O_NOFOLLOW, 0644)
	if err != nil {
		return err
	}
	// Marshal (rather than hand-formatting) so the repo-influenced output is
	// escaped: raw """ in it would otherwise break the file's TOML and the
	// agent would never run for that episode.
	data, err := toml.Marshal(stuckFile{
		StuckAt:      stuckAt,
		Reason:       reason,
		LastCommand:  lastCommand,
		LastExitCode: exitCode,
		LastOutput:   output,
	})
	if err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// stuckResult builds the result for a sync that failed and tried to record the
// stuck state. A failure to record it is appended to the error so it is never
// silently dropped — the flag simply isn't written and the next tick retries.
func stuckResult(err, writeErr error, op string) SyncResult {
	msg := fmt.Sprintf("%s: %v", op, err)
	if writeErr != nil {
		msg += fmt.Sprintf(" (recording stuck state: %v)", writeErr)
	}
	return SyncResult{State: "stuck", Error: msg}
}

func combineOutput(stdout, stderr string) string {
	if stdout != "" && stderr != "" {
		return stdout + "\n" + stderr
	}
	if stderr != "" {
		return stderr
	}
	return stdout
}

func exitCodeFromErr(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
