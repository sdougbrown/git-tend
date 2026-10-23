package git

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func Fetch(ctx context.Context, repoPath string, timeout time.Duration) (string, string, error, bool) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "fetch", "--quiet")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	isNet := IsNetworkError(err, stderr.String())
	return stdout.String(), stderr.String(), err, isNet
}

func PullRebase(ctx context.Context, repoPath string, timeout time.Duration) (string, string, error, bool) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "pull", "--rebase", "--autostash")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	isNet := IsNetworkError(err, stderr.String())
	return stdout.String(), stderr.String(), err, isNet
}

func Push(ctx context.Context, repoPath string, timeout time.Duration) (string, string, error, bool) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "push")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	isNet := IsNetworkError(err, stderr.String())
	return stdout.String(), stderr.String(), err, isNet
}

func StatusPorcelain(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "status", "--porcelain")
	out, err := cmd.Output()
	return string(out), err
}

func Add(repoPath string, args ...string) error {
	cmdArgs := append([]string{"-C", repoPath, "add"}, args...)
	cmd := exec.Command("git", cmdArgs...)
	return cmd.Run()
}

// Unstage removes the given pathspecs from the index, leaving the working tree
// alone. It unstages exactly what the pathspecs match, so the caller decides the
// scope. A whole-tree spec unstages everything the spec covers, including work
// someone else staged inside it. A caller that must preserve foreign staging
// narrows the spec accordingly.
func Unstage(repoPath string, specs ...string) error {
	args := append([]string{"-C", repoPath, "reset", "-q", "--"}, specs...)
	return exec.Command("git", args...).Run()
}

func Commit(repoPath, message string, noVerify bool) error {
	args := []string{"-C", repoPath, "commit", "-m", message}
	if noVerify {
		args = append(args, "--no-verify")
	}
	cmd := exec.Command("git", args...)
	return cmd.Run()
}

func RebaseAbort(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "rebase", "--abort")
	return cmd.Run()
}

func CurrentBranch(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func DiffCachedNameStatus(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "diff", "--cached", "--name-status", "-M")
	out, err := cmd.Output()
	return string(out), err
}

// StagedOutsideScope reports whether the index holds staged changes that the
// write scope does not cover. scopeSpecs is the composition git-tend stages with,
// so git evaluates includes, excludes and the sentinel alike. The answer cannot
// drift from what the staging does. Nothing staged means nothing to protect.
func StagedOutsideScope(repoPath string, scopeSpecs []string) (bool, error) {
	staged, err := stagedNames(repoPath, nil)
	if err != nil || len(staged) == 0 {
		return false, err
	}

	names, err := stagedNames(repoPath, scopeSpecs)
	if err != nil {
		return false, err
	}
	inScope := make(map[string]struct{}, len(names))
	for _, p := range names {
		inScope[p] = struct{}{}
	}

	for _, p := range staged {
		if _, ok := inScope[p]; !ok {
			return true, nil
		}
	}
	return false, nil
}

// stagedNames returns the staged paths matching the given pathspecs, with no
// pathspecs meaning every staged path. git evaluates the pathspecs, so a
// negative entry counts paths out exactly as it does for `git add`.
func stagedNames(repoPath string, specs []string) ([]string, error) {
	args := []string{"-C", repoPath, "diff", "--cached", "--name-only", "-z", "--"}
	args = append(args, specs...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimRight(string(out), "\x00")
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\x00"), nil
}

func DiffCached(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "diff", "--cached")
	out, err := cmd.Output()
	return string(out), err
}

// DiffCachedMax is like DiffCached but buffers at most maxBytes of the staged
// diff, so a large staged change cannot grow the daemon's memory before the
// caller caps it. The read stops at maxBytes and the remainder of the pipe is
// drained so git can exit.
func DiffCachedMax(repoPath string, maxBytes int) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "diff", "--cached")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, stdout, int64(maxBytes)); err != nil && err != io.EOF {
		return "", err
	}
	// Drain the remainder so git can exit; the daemon keeps at most maxBytes.
	io.Copy(io.Discard, stdout)
	if err := cmd.Wait(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func GitDir(repoPath string) (string, error) {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--absolute-git-dir")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ActiveLocks reports whether a *.lock file exists anywhere under the repo's
// .git directory. Git creates these while a command is mid-write (e.g.
// index.lock during `git add`/`git commit` finalization), so their presence
// means the repo is being modified by git right now.
func ActiveLocks(repoPath string) (bool, error) {
	gitDir, err := GitDir(repoPath)
	if err != nil {
		return false, err
	}
	found := false
	err = filepath.WalkDir(gitDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// objects/ and logs/ are the bulk of a large .git and never hold
			// lock files; skipping them keeps the per-tick scan cheap.
			if d.Name() == "objects" || d.Name() == "logs" {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".lock") {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return found, nil
}

// CommitEditRecent reports whether .git/COMMIT_EDITMSG — the file git holds
// while a commit message is being composed — has been touched within window.
// It is the only signal available during the editor-open phase (git holds no
// lock while waiting for an editor to close).
func CommitEditRecent(repoPath string, window time.Duration) (bool, error) {
	gitDir, err := GitDir(repoPath)
	if err != nil {
		return false, err
	}
	fi, err := os.Stat(filepath.Join(gitDir, "COMMIT_EDITMSG"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return time.Since(fi.ModTime()) <= window, nil
}

// ClearCommitEditMsg removes the leftover .git/COMMIT_EDITMSG after git-tend's
// own auto-commit, so the file reflects only human activity and does not cause
// git-tend to back off against its own commits.
func ClearCommitEditMsg(repoPath string) error {
	gitDir, err := GitDir(repoPath)
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(gitDir, "COMMIT_EDITMSG")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func ListTrackedFiles(repoPath string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoPath, "ls-files", "-z")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimRight(string(out), "\x00")
	if raw == "" {
		return nil, nil
	}
	return strings.Split(raw, "\x00"), nil
}

var networkErrorPatterns = []string{
	"could not resolve host",
	"connection refused",
	"connection timed out",
	"network is unreachable",
	"operation timed out",
	"unable to access",
	"could not read from remote repository",
	"failed to connect",
	"ssl connect error",
	"temporary failure in name resolution",
}

func IsNetworkError(err error, stderr string) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	lower := strings.ToLower(stderr)
	for _, pat := range networkErrorPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}
