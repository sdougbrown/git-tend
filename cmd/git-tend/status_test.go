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
	lines := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		for repo := range sf.Repos {
			if strings.HasPrefix(line, repo) {
				lines[repo] = line
			}
		}
	}

	if got := lines["/repos/agent-pending"]; !strings.Contains(got, "stuck (agent pending, attempts 1/2)") {
		t.Errorf("pending row = %q, want it to contain \"stuck (agent pending, attempts 1/2)\"", got)
	}
	if got := lines["/repos/agent-exhausted"]; !strings.Contains(got, "stuck (agent exhausted, attempts 2/2)") {
		t.Errorf("exhausted row = %q, want it to contain \"stuck (agent exhausted, attempts 2/2)\"", got)
	}
	if got := lines["/repos/plain-stuck"]; strings.Contains(got, "agent") {
		t.Errorf("no-agent row = %q, want plain stuck (AgentMaxAttempts 0 guard)", got)
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
