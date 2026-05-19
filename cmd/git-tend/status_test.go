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
