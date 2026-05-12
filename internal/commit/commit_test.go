package commit

import (
	"testing"
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
