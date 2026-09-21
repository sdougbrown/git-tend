package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCommitEditRecent(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "init", "-q")
	gitDir, err := GitDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	msgPath := filepath.Join(gitDir, "COMMIT_EDITMSG")

	// No file yet: never recent.
	if recent, err := CommitEditRecent(repo, time.Hour); err != nil || recent {
		t.Fatalf("absent file should not be recent (recent=%v err=%v)", recent, err)
	}

	// Written just now within a 1s window.
	if err := os.WriteFile(msgPath, []byte("draft"), 0644); err != nil {
		t.Fatal(err)
	}
	if recent, err := CommitEditRecent(repo, time.Hour); err != nil || !recent {
		t.Fatalf("fresh file should be recent (recent=%v err=%v)", recent, err)
	}

	// Stale beyond the window.
	past := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(msgPath, past, past); err != nil {
		t.Fatal(err)
	}
	if recent, err := CommitEditRecent(repo, time.Minute); err != nil || recent {
		t.Fatalf("stale file should not be recent (recent=%v err=%v)", recent, err)
	}
}

func TestGitDir(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "init", "-q")

	gitDir, err := GitDir(repo)
	if err != nil {
		t.Fatalf("expected git dir in repo, got error: %v", err)
	}
	if fi, err := os.Stat(gitDir); err != nil || !fi.IsDir() {
		t.Fatalf("GitDir returned %q, want an existing directory", gitDir)
	}

	// Outside any repository the underlying git command must fail.
	notARepo := t.TempDir()
	if _, err := GitDir(notARepo); err == nil {
		t.Fatal("expected error for a path that is not a git repository")
	}
}

func TestClearCommitEditMsg(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "init", "-q")
	gitDir, err := GitDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	msgPath := filepath.Join(gitDir, "COMMIT_EDITMSG")

	// Removing an absent sentinel is a no-op.
	if err := ClearCommitEditMsg(repo); err != nil {
		t.Fatalf("clear with no COMMIT_EDITMSG should succeed, got %v", err)
	}

	// Removing an existing sentinel deletes the file.
	if err := os.WriteFile(msgPath, []byte("draft"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ClearCommitEditMsg(repo); err != nil {
		t.Fatalf("clear with COMMIT_EDITMSG should succeed, got %v", err)
	}
	if _, err := os.Stat(msgPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("COMMIT_EDITMSG should be gone after clear, stat err=%v", err)
	}
}

func TestActiveLocks(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "init", "-q")
	gitDir, err := GitDir(repo)
	if err != nil {
		t.Fatal(err)
	}

	if locks, err := ActiveLocks(repo); err != nil || locks {
		t.Fatalf("clean repo should have no locks (locks=%v err=%v)", locks, err)
	}

	if err := os.WriteFile(filepath.Join(gitDir, "index.lock"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if locks, err := ActiveLocks(repo); err != nil || !locks {
		t.Fatalf("index.lock should be detected (locks=%v err=%v)", locks, err)
	}
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestIsNetworkError(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		stderr string
		want   bool
	}{
		{
			name:   "nil error and empty stderr",
			err:    nil,
			stderr: "",
			want:   false,
		},
		{
			name:   "nil error with unrelated stderr",
			err:    nil,
			stderr: "CONFLICT (content): Merge conflict in foo.txt",
			want:   false,
		},
		{
			name:   "non-network git error",
			err:    errors.New("some git error"),
			stderr: "CONFLICT (content): Merge conflict in foo.txt",
			want:   false,
		},
		{
			name:   "could not resolve host",
			err:    errors.New("remote error"),
			stderr: "fatal: Could not resolve host: github.com",
			want:   true,
		},
		{
			name:   "could not resolve host case insensitive",
			err:    errors.New("remote error"),
			stderr: "FATAL: COULD NOT RESOLVE HOST: github.com",
			want:   true,
		},
		{
			name:   "connection refused",
			err:    errors.New("remote error"),
			stderr: "ssh: connect to host github.com port 22: Connection refused",
			want:   true,
		},
		{
			name:   "connection timed out",
			err:    errors.New("remote error"),
			stderr: "ssh: Connection timed out",
			want:   true,
		},
		{
			name:   "network is unreachable",
			err:    errors.New("remote error"),
			stderr: "Network is unreachable",
			want:   true,
		},
		{
			name:   "operation timed out",
			err:    errors.New("remote error"),
			stderr: "Operation timed out after 30000 ms",
			want:   true,
		},
		{
			name:   "unable to access",
			err:    errors.New("remote error"),
			stderr: "fatal: unable to access 'https://github.com/foo/bar.git/'",
			want:   true,
		},
		{
			name:   "could not read from remote repository",
			err:    errors.New("remote error"),
			stderr: "fatal: Could not read from remote repository.",
			want:   true,
		},
		{
			name:   "failed to connect",
			err:    errors.New("remote error"),
			stderr: "Failed to connect to github.com port 443",
			want:   true,
		},
		{
			name:   "ssl connect error",
			err:    errors.New("remote error"),
			stderr: "SSL connect error",
			want:   true,
		},
		{
			name:   "temporary failure in name resolution",
			err:    errors.New("remote error"),
			stderr: "Temporary failure in name resolution",
			want:   true,
		},
		{
			name: "context deadline exceeded",
			err: func() error {
				ctx, cancel := context.WithTimeout(context.Background(), 0)
				defer cancel()
				return ctx.Err()
			}(),
			stderr: "",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsNetworkError(tt.err, tt.stderr)
			if got != tt.want {
				t.Errorf("IsNetworkError() = %v, want %v", got, tt.want)
			}
		})
	}
}
