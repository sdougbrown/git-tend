package commit

import (
	"strings"
	"testing"
	"time"
)

func TestGenerate(t *testing.T) {
	tests := []struct {
		name             string
		diffOutput       string
		emoji            string
		fallbackThreshold int
		want             string
	}{
		{
			name:             "empty diff",
			diffOutput:       "",
			emoji:            "🐌",
			fallbackThreshold: 5,
			want:             "",
		},
		{
			name:             "whitespace-only diff",
			diffOutput:       "  \n\t\n ",
			emoji:            "🐌",
			fallbackThreshold: 5,
			want:             "",
		},
		{
			name:             "single file added",
			diffOutput:       "A\tfoo.txt",
			emoji:            "🐌",
			fallbackThreshold: 5,
			want:             "🐌 add foo.txt",
		},
		{
			name:             "single file modified",
			diffOutput:       "M\tbar.go",
			emoji:            "🐌",
			fallbackThreshold: 5,
			want:             "🐌 update bar.go",
		},
		{
			name:             "single file deleted",
			diffOutput:       "D\told.txt",
			emoji:            "🐌",
			fallbackThreshold: 5,
			want:             "🐌 remove old.txt",
		},
		{
			name:             "single file renamed R100",
			diffOutput:       "R100\told\tnew",
			emoji:            "🐌",
			fallbackThreshold: 5,
			want:             "🐌 rename old → new",
		},
		{
			name:             "single file renamed R050",
			diffOutput:       "R050\ta\tb",
			emoji:            "🐌",
			fallbackThreshold: 5,
			want:             "🐌 rename a → b",
		},
		{
			name:             "no emoji",
			diffOutput:       "A\tfoo.txt",
			emoji:            "",
			fallbackThreshold: 5,
			want:             "add foo.txt",
		},
		{
			name:             "no emoji renamed file",
			diffOutput:       "R100\ta\tb",
			emoji:            "",
			fallbackThreshold: 5,
			want:             "rename a → b",
		},
		{
			name:             "single root file added",
			diffOutput:       "A\tfile.txt",
			emoji:            "🐌",
			fallbackThreshold: 5,
			want:             "🐌 add file.txt",
		},
		{
			name:             "multiple root files",
			diffOutput:       "A\tfoo.txt\nM\tbar.txt",
			emoji:            "🐌",
			fallbackThreshold: 5,
			want:             "🐌 update ./ (2 files)",
		},
		{
			name:             "single dir up to 5 files",
			diffOutput:       "A\tsrc/foo.txt\nM\tsrc/bar.go\nD\tsrc/old.txt",
			emoji:            "🐌",
			fallbackThreshold: 5,
			want:             "🐌 update src/ (3 files)",
		},
		{
			name:             "single dir with more than 5 files",
			diffOutput:       "A\tsrc/f1.txt\nA\tsrc/f2.txt\nA\tsrc/f3.txt\nA\tsrc/f4.txt\nA\tsrc/f5.txt\nA\tsrc/f6.txt",
			emoji:            "🐌",
			fallbackThreshold: 5,
			want:             "🐌 sync src (6 files)",
		},
		{
			name:             "changes spanning up to threshold dirs",
			diffOutput:       "A\tsrc/foo.txt\nM\ttests/bar.go\nD\tdocs/old.txt",
			emoji:            "🐌",
			fallbackThreshold: 3,
			want:             "🐌 sync docs, src, tests (3 files)",
		},
		{
			name:             "changes spanning more than threshold dirs",
			diffOutput:       "A\tsrc/foo.txt\nM\ttests/bar.go\nD\tdocs/old.txt\nR100\tool\tnew/tool",
			emoji:            "🐌",
			fallbackThreshold: 3,
			want:             "🐌 sync 4 files",
		},
		{
			name:             "changes spanning exactly threshold dirs",
			diffOutput:       "A\tsrc/foo.txt\nM\ttests/bar.go\nD\tdocs/old.txt\nR100\tool\tnew/tool",
			emoji:            "🐌",
			fallbackThreshold: 4,
			want:             "🐌 sync docs, new, src, tests (4 files)",
		},
		{
			name:             "single dir with rename counts as that dir",
			diffOutput:       "R100\tsrc/old\tsrc/new",
			emoji:            "🐌",
			fallbackThreshold: 5,
			want:             "🐌 rename src/old → src/new",
		},
		{
			name:             "rename across dirs counts as destination dir",
			diffOutput:       "R100\tsrc/old\tdst/new",
			emoji:            "🐌",
			fallbackThreshold: 5,
			want:             "🐌 rename src/old → dst/new",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Generate(tt.diffOutput, tt.emoji, tt.fallbackThreshold)
			if got != tt.want {
				t.Errorf("Generate() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenerateWithModel(t *testing.T) {
	diff := "M\tsrc/a.go\nM\tlib/b.go\nM\tcmd/c.go\nM\tpkg/d.go\nM\tinternal/e.go\nM\ttest/f.go\nM\tdocs/g.go"
	modelCmd := "echo 'add feature X'"
	result := GenerateWithModel(diff, "🐌", 3, modelCmd, 30*time.Second, "fake full diff\n")

	if result != "🐌 add feature X" {
		t.Errorf("expected model output, got %q", result)
	}
}

func TestGenerateWithModelTimeout(t *testing.T) {
	diff := "M\tsrc/a.go\nM\tlib/b.go\nM\tcmd/c.go\nM\tpkg/d.go\nM\tinternal/e.go\nM\ttest/f.go\nM\tdocs/g.go"

	modelCmd := "sleep 60"
	result := GenerateWithModel(diff, "🐌", 3, modelCmd, 100*time.Millisecond, "fake full diff\n")

	if !strings.Contains(result, "sync") || !strings.Contains(result, "files") {
		t.Errorf("expected fallback to deterministic, got %q", result)
	}
	if !strings.HasPrefix(result, "🐌 ") {
		t.Errorf("expected emoji prefix in fallback, got %q", result)
	}
}

func TestGenerateWithModelExitError(t *testing.T) {
	diff := "M\tsrc/a.go\nM\tlib/b.go\nM\tcmd/c.go\nM\tpkg/d.go\nM\tinternal/e.go\nM\ttest/f.go\nM\tdocs/g.go"

	modelCmd := "exit 1"
	result := GenerateWithModel(diff, "🐌", 3, modelCmd, 5*time.Second, "fake full diff\n")

	if !strings.Contains(result, "sync") {
		t.Errorf("expected fallback, got %q", result)
	}
}

func TestGenerateWithModelBelowThreshold(t *testing.T) {
	diff := "M\tsrc/a.go\nM\tsrc/b.go"

	modelCmd := "echo 'should not be called'"
	result := GenerateWithModel(diff, "🐌", 3, modelCmd, 30*time.Second, "")

	if strings.Contains(result, "should not be called") {
		t.Error("model should not have been called below threshold")
	}
	expectedPrefix := "🐌 update"
	if !strings.HasPrefix(result, expectedPrefix) {
		t.Errorf("expected deterministic message, got %q", result)
	}
}
