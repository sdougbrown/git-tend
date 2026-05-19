package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/sdougbrown/git-tend/internal/paths"
	"github.com/sdougbrown/git-tend/internal/status"
)

func TestGreetDateStamp(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("NO_COLOR", "1")

	stateDir := paths.StateDir()
	os.MkdirAll(stateDir, 0755)

	configDir := filepath.Join(tmpHome, ".config", "git-tend")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("escalate_after_days = 3\n"), 0644)

	sf := &status.StatusFile{
		Version:         1,
		LastTickAt:      time.Now().UTC().Format(time.RFC3339),
		IntervalSeconds: 60,
		Repos: map[string]status.RepoStatus{
			"/test/repo": {
				Mode:         "read-write",
				CurrentState: "stuck",
				StuckSince:   time.Now().Add(-4 * 24 * time.Hour).UTC().Format(time.RFC3339),
			},
		},
	}
	status.Write(filepath.Join(stateDir, "status.json"), sf)

	cmd := &cobra.Command{}
	err := runGreet(cmd, nil)
	if err != nil {
		t.Fatalf("first greet failed: %v", err)
	}

	greetLastPath := filepath.Join(stateDir, "greet.last")
	data, err := os.ReadFile(greetLastPath)
	if err != nil {
		t.Fatal("greet.last was not created")
	}
	today := time.Now().Format("2006-01-02")
	if strings.TrimSpace(string(data)) != today {
		t.Errorf("greet.last = %q, want %q", strings.TrimSpace(string(data)), today)
	}

	err = runGreet(cmd, nil)
	if err != nil {
		t.Fatalf("second greet failed: %v", err)
	}

	data2, err := os.ReadFile(greetLastPath)
	if err != nil {
		t.Fatal("greet.last missing after second greet")
	}
	if strings.TrimSpace(string(data2)) != today {
		t.Errorf("greet.last after second greet = %q, want %q", strings.TrimSpace(string(data2)), today)
	}
}
