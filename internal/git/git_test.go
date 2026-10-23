package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	// Locks also appear in subdirectories, e.g. refs/heads/<branch>.lock.
	refsHeads := filepath.Join(gitDir, "refs", "heads")
	if err := os.MkdirAll(refsHeads, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(gitDir, "index.lock")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsHeads, "main.lock"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if locks, err := ActiveLocks(repo); err != nil || !locks {
		t.Fatalf("refs/heads/main.lock should be detected (locks=%v err=%v)", locks, err)
	}

	// Non-lock files must not trigger detection.
	if err := os.Remove(filepath.Join(refsHeads, "main.lock")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"foo.locks", "xlock", "COMMIT_EDITMSG"} {
		if err := os.WriteFile(filepath.Join(gitDir, name), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if locks, err := ActiveLocks(repo); err != nil || locks {
		t.Fatalf("non-lock files should not be detected (locks=%v err=%v)", locks, err)
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

func TestDiffCachedMaxCapsAtSource(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "init", "-q")

	// Stage a file whose diff is well over the cap. No commit is needed:
	// `git diff --cached` compares the index against the empty tree.
	big := strings.Repeat("line of change content\n", 1000) // ~22 KiB
	if err := os.WriteFile(filepath.Join(repo, "big.txt"), []byte(big), 0644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "add", "big.txt")

	const cap = 1024
	got, err := DiffCachedMax(repo, cap)
	if err != nil {
		t.Fatalf("DiffCachedMax() error: %v", err)
	}
	if len(got) != cap {
		t.Errorf("DiffCachedMax() returned %d bytes, want exactly %d (the cap)", len(got), cap)
	}

	// The capped output must be a prefix of the full diff.
	full, err := DiffCached(repo)
	if err != nil {
		t.Fatalf("DiffCached() error: %v", err)
	}
	if len(full) <= cap {
		t.Fatalf("test setup: expected full diff > cap, got %d bytes", len(full))
	}
	if !strings.HasPrefix(full, got) {
		t.Errorf("DiffCachedMax() output is not a prefix of the full diff")
	}
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

func TestStagedOutsideScope(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "init", "-q")
	// CI runners ship without a default identity, so the commit below needs one.
	run(t, repo, "config", "user.name", "git-tend test")
	run(t, repo, "config", "user.email", "test@gittend.local")
	for _, d := range []string{"memory", "notes", "src/secrets", "projects/x/memory"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"memory/a.txt", "notes/b.txt", "src/secrets/key.pem", "projects/x/memory/c.txt"} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte(f), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// The write scope is memory/ plus a magic-prefixed glob, minus src/secrets/,
	// composed exactly as the staging composes it.
	outside := func() bool {
		t.Helper()
		got, err := StagedOutsideScope(repo,
			[]string{"memory/", ":(glob)projects/*/memory/*", ":!.gittend.stuck", ":(exclude)src/secrets/"})
		if err != nil {
			t.Fatalf("StagedOutsideScope() error: %v", err)
		}
		return got
	}

	// Nothing staged: nothing to protect.
	if outside() {
		t.Error("StagedOutsideScope() = true with an empty index")
	}

	// Only the write scope staged, plain and glob.
	run(t, repo, "add", "memory/a.txt")
	if outside() {
		t.Error("StagedOutsideScope() = true for a staged path inside the scope")
	}
	run(t, repo, "add", "projects/x/memory/c.txt")
	if outside() {
		t.Error("StagedOutsideScope() = true for a glob-scoped staged path")
	}

	// A file outside the scope, on top of in-scope staging.
	run(t, repo, "add", "notes/b.txt")
	if !outside() {
		t.Error("StagedOutsideScope() = false for a staged path outside the scope")
	}
	run(t, repo, "reset", "-q", "notes/b.txt")

	// A path git-tend deliberately excludes is outside the scope even when the
	// include paths cover it, because its own `git add` subtracts the exclude.
	run(t, repo, "add", "src/secrets/key.pem")
	if !outside() {
		t.Error("StagedOutsideScope() = false for a staged path matching [exclude]")
	}
	run(t, repo, "reset", "-q", "src/secrets/key.pem")

	// A staged deletion carries the deleted path, so it is caught too.
	run(t, repo, "add", "notes/b.txt")
	run(t, repo, "commit", "-q", "-m", "track notes/b.txt")
	if err := os.Remove(filepath.Join(repo, "notes", "b.txt")); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "add", "-A", "notes/b.txt")
	if !outside() {
		t.Error("StagedOutsideScope() = false for a staged deletion outside the scope")
	}
}

// With no [include] the staging is everything minus the [exclude] paths. An
// empty inversion therefore has to mean "everything is inside the scope", not
// "nothing is".
func TestStagedOutsideScopeWithoutInclude(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "init", "-q")
	for _, d := range []string{"src", "secrets"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"src/a.txt", "secrets/key.pem"} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte(f), 0644); err != nil {
			t.Fatal(err)
		}
	}

	outside := func() bool {
		t.Helper()
		got, err := StagedOutsideScope(repo, []string{".", ":(exclude)secrets/"})
		if err != nil {
			t.Fatalf("StagedOutsideScope() error: %v", err)
		}
		return got
	}

	run(t, repo, "add", "src/a.txt")
	if outside() {
		t.Error("StagedOutsideScope() = true for a staged path outside the exclude, want false")
	}

	run(t, repo, "add", "secrets/key.pem")
	if !outside() {
		t.Error("StagedOutsideScope() = false for a staged exclude path, want true")
	}
}

// A staged rename has two sides. With rename detection on, git reports only the
// destination, so renaming a file into the scope would hide the out-of-scope
// deletion of its source.
func TestStagedOutsideScopeRename(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "init", "-q")
	run(t, repo, "config", "user.name", "git-tend test")
	run(t, repo, "config", "user.email", "test@gittend.local")
	for _, d := range []string{"src", "outside"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "outside", "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "add", "-A")
	run(t, repo, "commit", "-q", "-m", "init")

	// Move an out-of-scope file into the scope: the deletion of outside/a.txt is
	// not git-tend's to commit.
	run(t, repo, "mv", "outside/a.txt", "src/a.txt")
	got, err := StagedOutsideScope(repo, []string{"src/", ":!.gittend.stuck"})
	if err != nil {
		t.Fatalf("StagedOutsideScope() error: %v", err)
	}
	if !got {
		t.Error("StagedOutsideScope() = false for a rename whose source is out of scope")
	}
}

// Unstage removes the given paths from the index and nothing else.
func TestUnstage(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "init", "-q")
	run(t, repo, "config", "user.name", "git-tend test")
	run(t, repo, "config", "user.email", "test@gittend.local")
	for _, f := range []string{"one.txt", "two.txt"} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte(f), 0644); err != nil {
			t.Fatal(err)
		}
	}
	run(t, repo, "add", "-A")

	if err := Unstage(repo, "one.txt"); err != nil {
		t.Fatalf("Unstage() error: %v", err)
	}

	staged := strings.TrimSpace(run(t, repo, "diff", "--cached", "--name-only"))
	if staged != "two.txt" {
		t.Errorf("staged = %q, want only two.txt", staged)
	}
	// The working tree is untouched by the unstage.
	if b, err := os.ReadFile(filepath.Join(repo, "one.txt")); err != nil || string(b) != "one.txt" {
		t.Errorf("one.txt working tree changed: %q, %v", string(b), err)
	}
	// A pathspec that matches nothing is a no-op, not an error.
	if err := Unstage(repo, "missing.txt"); err != nil {
		t.Errorf("Unstage() on an unmatched path = %v, want nil", err)
	}
}

// A negative include spec means "everything except", which the guard has to
// honour rather than inverting into a pathspec that matches nothing.
func TestStagedOutsideScopeNegativeInclude(t *testing.T) {
	repo := t.TempDir()
	run(t, repo, "init", "-q")
	for _, d := range []string{"docs", "src", "tmp"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"docs/a.md", "src/b.go", "tmp/c.log"} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte(f), 0644); err != nil {
			t.Fatal(err)
		}
	}
	outside := func() bool {
		t.Helper()
		got, err := StagedOutsideScope(repo, []string{":!docs/", ":(exclude,glob)tmp/*"})
		if err != nil {
			t.Fatalf("StagedOutsideScope() error: %v", err)
		}
		return got
	}

	// src/b.go is the only staged path left after "everything except docs/ and
	// tmp/*", so it is inside the scope.
	run(t, repo, "add", "src/b.go")
	if outside() {
		t.Error("StagedOutsideScope() = true for a staged path the negative include keeps")
	}

	// A staged path the negative include names is outside the scope.
	run(t, repo, "add", "docs/a.md")
	if !outside() {
		t.Error("StagedOutsideScope() = false for a staged path the negative include excludes")
	}
	run(t, repo, "reset", "-q", "docs/a.md")

	// So is a staged path the magic negative leaves out.
	run(t, repo, "add", "tmp/c.log")
	if !outside() {
		t.Error("StagedOutsideScope() = false for a staged path a magic negative excludes")
	}
}

func TestStagedOutsideScopeErrorsOutsideRepo(t *testing.T) {
	if _, err := StagedOutsideScope(t.TempDir(), []string{"src/"}); err == nil {
		t.Error("StagedOutsideScope() = nil error outside a git repo, want an error")
	}
}
