package git

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
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
