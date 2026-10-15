package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sdougbrown/git-tend/internal/status"
)

func TestRunStatusAgentStates(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	// Force paths.StateDir() to resolve under HOME regardless of the runner's
	// XDG_STATE_HOME, so the state dir we create is the one status reads.
	t.Setenv("XDG_STATE_HOME", "")

	stateDir := filepath.Join(tmpHome, ".local", "state", "git-tend")
	os.MkdirAll(stateDir, 0755)

	sf := &status.StatusFile{
		Repos: map[string]status.RepoStatus{
			"/repos/agent-pending":   {CurrentState: "stuck", AgentAttempts: 1, AgentMaxAttempts: 2},
			"/repos/agent-exhausted": {CurrentState: "stuck", AgentAttempts: 2, AgentMaxAttempts: 2},
			"/repos/plain-stuck":     {CurrentState: "stuck"},
		},
	}
	status.Write(filepath.Join(stateDir, "status.json"), sf)

	out := captureStatus(t)
	// Group each repo's output: its row plus any indented detail lines that
	// follow it (agent detail renders outside the fixed-width grid).
	blocks := map[string]string{}
	current := ""
	for _, line := range strings.Split(out, "\n") {
		matched := ""
		for repo := range sf.Repos {
			if strings.HasPrefix(line, repo) {
				matched = repo
				break
			}
		}
		if matched != "" {
			current = matched
			blocks[current] = line
			continue
		}
		if current != "" && strings.TrimSpace(line) != "" {
			blocks[current] += "\n" + line
		}
	}

	if got := blocks["/repos/agent-pending"]; !strings.Contains(got, "stuck") || !strings.Contains(got, "agent pending, attempts 1/2") {
		t.Errorf("pending block = %q, want it to contain stuck with agent pending, attempts 1/2", got)
	}
	if got := blocks["/repos/agent-exhausted"]; !strings.Contains(got, "agent exhausted, attempts 2/2") {
		t.Errorf("exhausted block = %q, want it to contain agent exhausted, attempts 2/2", got)
	}
	// The agent detail must not break the column grid: the STATE column of the
	// row itself stays short.
	if got := blocks["/repos/agent-pending"]; strings.Count(got, "\n") != 1 {
		t.Errorf("pending block = %q, want exactly one detail line", got)
	}
	if got := blocks["/repos/plain-stuck"]; strings.Contains(got, "agent") {
		t.Errorf("no-agent block = %q, want plain stuck (AgentMaxAttempts 0 guard)", got)
	}
}

func captureStatus(t *testing.T) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStdout := os.Stdout
	os.Stdout = w

	err = runStatus(&cobra.Command{}, nil)

	w.Close()
	os.Stdout = origStdout
	if err != nil {
		t.Fatalf("runStatus failed: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRunStatusEmptyRepos(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	stateDir := filepath.Join(tmpHome, ".local", "state", "git-tend")
	os.MkdirAll(stateDir, 0755)

	sf := &status.StatusFile{
		Repos: map[string]status.RepoStatus{},
	}
	status.Write(filepath.Join(stateDir, "status.json"), sf)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStdout := os.Stdout
	os.Stdout = w

	err = runStatus(&cobra.Command{}, nil)
	if err != nil {
		t.Fatalf("runStatus failed: %v", err)
	}

	w.Close()
	os.Stdout = origStdout

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(data), "No managed repos found yet") {
		t.Errorf("output = %q, want it to contain %q", string(data), "No managed repos found yet")
	}
}
