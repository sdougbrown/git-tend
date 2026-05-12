package main

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/dbrown/git-tend/internal/status"
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
