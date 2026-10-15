package paths

import (
	"os"
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

func TestSamePath(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "repo")
	if err := os.MkdirAll(real, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "identical", a: real, b: real, want: true},
		{name: "trailing slash", a: real, b: real + "/", want: true},
		{name: "symlink vs real", a: link, b: real, want: true},
		{name: "different dirs", a: real, b: dir, want: false},
		{name: "both missing, equal", a: filepath.Join(dir, "nope"), b: filepath.Join(dir, "nope"), want: true},
		{name: "both missing, different", a: filepath.Join(dir, "nope"), b: filepath.Join(dir, "nope2"), want: false},
		{name: "missing vs existing", a: filepath.Join(dir, "nope"), b: real, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SamePath(tt.a, tt.b); got != tt.want {
				t.Errorf("SamePath(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
			// Symmetry.
			if got := SamePath(tt.b, tt.a); got != tt.want {
				t.Errorf("SamePath(%q, %q) = %v, want %v", tt.b, tt.a, got, tt.want)
			}
		})
	}
}

func TestSamePathTildeExpansion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if !SamePath("~/Code", os.Getenv("HOME")+"/Code") {
		t.Errorf("tilde path should match expanded path")
	}
}

func TestSamePathCaseSensitivity(t *testing.T) {
	// Case-insensitive matching is only enabled on darwin at runtime, but the
	// comparison logic itself is exercised here on every platform.
	if !samePath("/tmp/Repo", "/tmp/repo", true) {
		t.Error("case-insensitive comparison should match differing case")
	}
	if samePath("/tmp/Repo", "/tmp/repo", false) {
		t.Error("case-sensitive comparison should not match differing case")
	}

	dir := t.TempDir()
	real := filepath.Join(dir, "repo")
	if err := os.MkdirAll(real, 0755); err != nil {
		t.Fatal(err)
	}
	if !samePath(filepath.Join(dir, "REPO"), real, true) {
		t.Error("case-insensitive comparison should match an existing directory")
	}
	if samePath(filepath.Join(dir, "REPO"), real, false) {
		t.Error("case-sensitive comparison should not match an existing directory")
	}
}
