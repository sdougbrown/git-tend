package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sdougbrown/cli-contract"

	"github.com/sdougbrown/git-tend/internal/status"
)

func BenchmarkPrompt(b *testing.B) {
	dir := b.TempDir()

	sf := &status.StatusFile{
		Version:         1,
		LastTickAt:      "2026-01-01T00:00:00Z",
		IntervalSeconds: 60,
		Repos:           make(map[string]status.RepoStatus),
	}
	for i := 0; i < 10; i++ {
		sf.Repos[fmt.Sprintf("/path/to/repo-%d", i)] = status.RepoStatus{
			Mode:         "read-write",
			CurrentState: "ok",
		}
	}
	path := filepath.Join(dir, "status.json")
	if err := status.Write(path, sf); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sf := status.Read(path)
		_ = sf
	}
}

// TestPromptStuckExitCode pins the declared exit-code convention: the
// handler returns the stuck count and the adapter derives exit 1 for
// `prompt --any-stuck` when it is nonzero — no os.Exit inside the handler.
func TestPromptStuckExitCode(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("NO_COLOR", "1")

	stateDir := filepath.Join(os.Getenv("XDG_STATE_HOME"), "git-tend")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	sf := &status.StatusFile{
		Version:         1,
		LastTickAt:      time.Now().UTC().Format(time.RFC3339),
		IntervalSeconds: 60,
		Repos: map[string]status.RepoStatus{
			"/test/repo": {
				Mode:         "read-write",
				CurrentState: "stuck",
				StuckSince:   time.Now().Add(-2 * 24 * time.Hour).UTC().Format(time.RFC3339),
			},
		},
	}
	if err := status.Write(filepath.Join(stateDir, "status.json"), sf); err != nil {
		t.Fatal(err)
	}

	// With --any-stuck: the handler returns the stuck count and the adapter
	// maps it to exit 1.
	stuck, err := runPrompt(promptOpts{anyStuck: true})
	if err != nil {
		t.Fatalf("runPrompt failed: %v", err)
	}
	if stuck != 1 {
		t.Fatalf("stuck count = %d, want 1", stuck)
	}
	adapter := tendAdapter{}
	if code := adapter.ExitCode(clicontract.Invocation{Command: "prompt", Bools: map[string]bool{"any-stuck": true}}, stuck, nil); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	// Without --any-stuck: exit 0 whatever the count.
	stuck, err = runPrompt(promptOpts{})
	if err != nil {
		t.Fatalf("runPrompt failed: %v", err)
	}
	if stuck != 1 {
		t.Fatalf("stuck count = %d, want 1", stuck)
	}
	if code := adapter.ExitCode(clicontract.Invocation{Command: "prompt"}, stuck, nil); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}
