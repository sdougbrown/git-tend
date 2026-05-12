package status

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadMissing(t *testing.T) {
	sf := Read("/nonexistent/path/status.json")
	if sf != nil {
		t.Error("expected nil for missing file")
	}
}

func TestWriteReadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	sf := &StatusFile{
		Version:         1,
		DaemonStartedAt: "2026-01-01T00:00:00Z",
		LastTickAt:      "2026-01-01T01:00:00Z",
		IntervalSeconds: 60,
		Repos: map[string]RepoStatus{
			"/test/repo": {Mode: "read-write", CurrentState: "ok"},
		},
	}

	err := Write(path, sf)
	if err != nil {
		t.Fatal(err)
	}

	read := Read(path)
	if read == nil {
		t.Fatal("expected non-nil")
	}
	if read.Version != 1 {
		t.Errorf("version = %d, want 1", read.Version)
	}
	if rs, ok := read.Repos["/test/repo"]; !ok || rs.Mode != "read-write" {
		t.Error("repo not found or wrong mode")
	}
}

func TestAtomicWriteNoTmpFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	sf := &StatusFile{
		Version: 1,
		Repos:   map[string]RepoStatus{},
	}

	err := Write(path, sf)
	if err != nil {
		t.Fatal(err)
	}

	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); err == nil {
		t.Error(".tmp file should not exist after atomic write")
	}
}
