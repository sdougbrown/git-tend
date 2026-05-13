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

	if cfg.Mode == "read-only" {
		return syncReadOnly(ctx, repoPath, timeout)
	}

	return syncReadWrite(ctx, repoPath, cfg, timeout)
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

func syncReadOnly(ctx context.Context, repoPath string, timeout time.Duration) SyncResult {
	_, stderr, err, isNet := git.Fetch(ctx, repoPath, timeout)
	if isNet {
		return SyncResult{State: "offline", Error: err.Error()}
	}
	if err != nil {
		writeStuck(repoPath, "other", "git fetch", exitCodeFromErr(err), stderr)
		return SyncResult{State: "stuck", Error: fmt.Sprintf("fetch: %v", err)}
	}

	stdout, stderr, err, isNet := git.PullRebase(ctx, repoPath, timeout)
	if isNet {
		return SyncResult{State: "offline", Error: err.Error()}
	}
	if err != nil {
		git.RebaseAbort(repoPath)
		writeStuck(repoPath, "rebase_conflict", "git pull --rebase", exitCodeFromErr(err), combineOutput(stdout, stderr))
		return SyncResult{State: "stuck", Error: fmt.Sprintf("pull: %v", err)}
	}

	return SyncResult{State: "ok"}
}

func syncReadWrite(ctx context.Context, repoPath string, cfg *config.Config, timeout time.Duration) SyncResult {
	debounceDur := parseDebounce(cfg.Debounce)

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
	if !maxMtime.IsZero() && now.Sub(maxMtime) < debounceDur {
		return SyncResult{State: "skipped", Error: "debounce"}
	}

	excludeSpecs := translateExcludePaths(cfg.Exclude.Paths)

	if len(cfg.Include.Paths) > 0 {
		args := append([]string{"--"}, cfg.Include.Paths...)
		args = append(args, ":!.gittend.stuck")
		args = append(args, excludeSpecs...)
		if err := git.Add(repoPath, args...); err != nil {
			writeStuck(repoPath, "other", "git add", exitCodeFromErr(err), err.Error())
			return SyncResult{State: "stuck", Error: fmt.Sprintf("staging: %v", err)}
		}
	} else {
		args := append([]string{"-A", "--", ":!.gittend.stuck"}, excludeSpecs...)
		if err := git.Add(repoPath, args...); err != nil {
			writeStuck(repoPath, "other", "git add", exitCodeFromErr(err), err.Error())
			return SyncResult{State: "stuck", Error: fmt.Sprintf("staging: %v", err)}
		}
	}

	diff, err := git.DiffCachedNameStatus(repoPath)
	if err != nil {
		writeStuck(repoPath, "other", "git diff --cached", exitCodeFromErr(err), err.Error())
		return SyncResult{State: "stuck", Error: fmt.Sprintf("diff cached: %v", err)}
	}

	if strings.TrimSpace(diff) != "" {
		var msg string
		if cfg.Commit.ModelCmd != "" {
			modelTimeout := 30 * time.Second
			if cfg.Commit.ModelTimeout != "" {
				if d, err := time.ParseDuration(cfg.Commit.ModelTimeout); err == nil {
					modelTimeout = d
				}
			}
			fullDiff, fullErr := git.DiffCached(repoPath)
			if fullErr == nil {
				msg = commit.GenerateWithModel(diff, cfg.Commit.Emoji, cfg.Commit.FallbackThresh, cfg.Commit.ModelCmd, modelTimeout, fullDiff)
			} else {
				msg = commit.Generate(diff, cfg.Commit.Emoji, cfg.Commit.FallbackThresh)
			}
		} else {
			msg = commit.Generate(diff, cfg.Commit.Emoji, cfg.Commit.FallbackThresh)
		}
		if err := git.Commit(repoPath, msg, cfg.Commit.NoVerify); err != nil {
			writeStuck(repoPath, "hook_failed", "git commit", exitCodeFromErr(err), err.Error())
			return SyncResult{State: "stuck", Error: fmt.Sprintf("commit: %v", err)}
		}
	}

	stdout, stderr, err, isNet := git.PullRebase(ctx, repoPath, timeout)
	if isNet {
		return SyncResult{State: "offline", Error: err.Error()}
	}
	if err != nil {
		git.RebaseAbort(repoPath)
		writeStuck(repoPath, "rebase_conflict", "git pull --rebase", exitCodeFromErr(err), combineOutput(stdout, stderr))
		return SyncResult{State: "stuck", Error: fmt.Sprintf("pull: %v", err)}
	}

	stdout, stderr, err, isNet = git.Push(ctx, repoPath, timeout)
	if isNet {
		return SyncResult{State: "offline", Error: err.Error()}
	}
	if err != nil {
		writeStuck(repoPath, "push_rejected", "git push", exitCodeFromErr(err), combineOutput(stdout, stderr))
		return SyncResult{State: "stuck", Error: fmt.Sprintf("push: %v", err)}
	}

	return SyncResult{State: "ok"}
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

func writeStuck(repoPath, reason, lastCommand string, exitCode int, output string) error {
	path := filepath.Join(repoPath, ".gittend.stuck")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if len(output) > 4096 {
		output = output[len(output)-4096:]
	}

	_, err = fmt.Fprintf(f, `stuck_at = %q
reason = %q
last_command = %q
last_exit_code = %d
last_output = """
%s"""
`, time.Now().UTC().Format(time.RFC3339), reason, lastCommand, exitCode, output)

	return err
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
