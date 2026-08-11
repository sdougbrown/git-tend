package paths

import (
	"path/filepath"
	"testing"
)

func TestExpandPathMakesRelativePathsAbsolute(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	got := ExpandPath(".")
	want, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("ExpandPath(\".\") = %q, want %q", got, want)
	}
}
