package commit

import (
	"strings"
	"testing"
	"time"
)

func TestGenerate(t *testing.T) {
	tests := []struct {
		name              string
		diffOutput        string
		emoji             string
		fallbackThreshold int
		want              string
	}{
		{
			name:              "empty diff",
			diffOutput:        "",
			emoji:             "🐌",
			fallbackThreshold: 5,
			want:              "",
		},
		{
			name:              "whitespace-only diff",
			diffOutput:        "  \n\t\n ",
			emoji:             "🐌",
			fallbackThreshold: 5,
			want:              "",
		},
		{
			name:              "single file added",
			diffOutput:        "A\tfoo.txt",
			emoji:             "🐌",
			fallbackThreshold: 5,
			want:              "🐌 add foo.txt",
		},
		{
			name:              "single file modified",
			diffOutput:        "M\tbar.go",
			emoji:             "🐌",
			fallbackThreshold: 5,
			want:              "🐌 update bar.go",
		},
		{
			name:              "single file deleted",
			diffOutput:        "D\told.txt",
			emoji:             "🐌",
			fallbackThreshold: 5,
			want:              "🐌 remove old.txt",
		},
		{
			name:              "single file renamed R100",
			diffOutput:        "R100\told\tnew",
			emoji:             "🐌",
			fallbackThreshold: 5,
			want:              "🐌 rename old → new",
		},
		{
			name:              "single file renamed R050",
			diffOutput:        "R050\ta\tb",
			emoji:             "🐌",
			fallbackThreshold: 5,
			want:              "🐌 rename a → b",
		},
		{
			name:              "no emoji",
			diffOutput:        "A\tfoo.txt",
			emoji:             "",
			fallbackThreshold: 5,
			want:              "add foo.txt",
		},
		{
			name:              "no emoji renamed file",
			diffOutput:        "R100\ta\tb",
			emoji:             "",
			fallbackThreshold: 5,
			want:              "rename a → b",
		},
		{
			name:              "single root file added",
			diffOutput:        "A\tfile.txt",
			emoji:             "🐌",
			fallbackThreshold: 5,
			want:              "🐌 add file.txt",
		},
		{
			name:              "multiple root files",
			diffOutput:        "A\tfoo.txt\nM\tbar.txt",
			emoji:             "🐌",
			fallbackThreshold: 5,
			want:              "🐌 update ./ (2 files)",
		},
		{
			name:              "single dir up to 5 files",
			diffOutput:        "A\tsrc/foo.txt\nM\tsrc/bar.go\nD\tsrc/old.txt",
			emoji:             "🐌",
			fallbackThreshold: 5,
			want:              "🐌 update src/ (3 files)",
		},
		{
			name:              "single dir with more than 5 files",
			diffOutput:        "A\tsrc/f1.txt\nA\tsrc/f2.txt\nA\tsrc/f3.txt\nA\tsrc/f4.txt\nA\tsrc/f5.txt\nA\tsrc/f6.txt",
			emoji:             "🐌",
			fallbackThreshold: 5,
			want:              "🐌 sync src (6 files)",
		},
		{
			name:              "changes spanning up to threshold dirs",
			diffOutput:        "A\tsrc/foo.txt\nM\ttests/bar.go\nD\tdocs/old.txt",
			emoji:             "🐌",
			fallbackThreshold: 3,
			want:              "🐌 sync docs, src, tests (3 files)",
		},
		{
			name:              "changes spanning more than threshold dirs",
			diffOutput:        "A\tsrc/foo.txt\nM\ttests/bar.go\nD\tdocs/old.txt\nR100\tool\tnew/tool",
			emoji:             "🐌",
			fallbackThreshold: 3,
			want:              "🐌 sync 4 files",
		},
		{
			name:              "changes spanning exactly threshold dirs",
			diffOutput:        "A\tsrc/foo.txt\nM\ttests/bar.go\nD\tdocs/old.txt\nR100\tool\tnew/tool",
			emoji:             "🐌",
			fallbackThreshold: 4,
			want:              "🐌 sync docs, new, src, tests (4 files)",
		},
		{
			name:              "single dir with rename counts as that dir",
			diffOutput:        "R100\tsrc/old\tsrc/new",
			emoji:             "🐌",
			fallbackThreshold: 5,
			want:              "🐌 rename src/old → src/new",
		},
		{
			name:              "rename across dirs counts as destination dir",
			diffOutput:        "R100\tsrc/old\tdst/new",
			emoji:             "🐌",
			fallbackThreshold: 5,
			want:              "🐌 rename src/old → dst/new",
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

func TestGenerateModelFirst(t *testing.T) {
	// The heuristic message here is informative ("update src/ (2 files)"),
	// but the model strategy consults the model regardless.
	diff := "M\tsrc/a.go\nM\tsrc/b.go"

	result := GenerateModelFirst(diff, "🐌", 3, "echo 'refactor the parser'", 30*time.Second, "fake stdin\n")

	if result != "🐌 refactor the parser" {
		t.Errorf("expected model output, got %q", result)
	}
}

func TestGenerateModelFirstFallbackOnError(t *testing.T) {
	diff := "M\tsrc/a.go\nM\tsrc/b.go"

	result := GenerateModelFirst(diff, "🐌", 3, "exit 1", 5*time.Second, "fake stdin\n")

	if !strings.HasPrefix(result, "🐌 update src/") {
		t.Errorf("expected fallback to heuristic, got %q", result)
	}
}

func TestGenerateModelFirstFallbackOnEmptyOutput(t *testing.T) {
	diff := "M\tsrc/a.go\nM\tsrc/b.go"

	result := GenerateModelFirst(diff, "🐌", 3, "true", 5*time.Second, "fake stdin\n")

	if !strings.HasPrefix(result, "🐌 update src/") {
		t.Errorf("expected fallback to heuristic, got %q", result)
	}
}

func TestGenerateModelFirstReceivesStdin(t *testing.T) {
	diff := "M\tsrc/a.go\nM\tsrc/b.go"
	modelStdin := "M\tsrc/a.go\n\n" + DefaultModelPrompt

	// The command echoes back whether the expected prompt text reached stdin.
	modelCmd := "if grep -q 'name-status listing'; then echo 'stdin has prompt'; else echo 'stdin missing prompt'; fi"
	result := GenerateModelFirst(diff, "🐌", 3, modelCmd, 30*time.Second, modelStdin)

	if result != "🐌 stdin has prompt" {
		t.Errorf("expected composed stdin with prompt, got %q", result)
	}
}

func TestStartsWithEmoji(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want bool
	}{
		{"pictograph", "📝 fix the parser", true},
		{"regional indicator", "🇺🇸 shipping", true},
		{"gap range: misc technical", "⏰ fix the alarm", true},
		{"gap range: geometric shapes", "▶ resume playback", true},
		{"enclosed alphanumeric", "🆕 add feature", true},
		{"enclosed ideographic", "🈁 marker", true},
		{"emoji codepoint in arrows block", "⬅ move left", true},
		{"star", "⭐ star it", true},
		{"misc symbols/dingbats", "✏ fix", true},
		{"black square", "⬛ fill", true},
		{"large circle", "⭕ circle", true},
		{"plain arrow in arrows block", "⬀ draw arrow", false},
		{"plain ascii", "fix the parser", false},
		{"variation selector before emoji", "\uFE0F⏰ fix the alarm", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := startsWithEmoji(tt.msg); got != tt.want {
				t.Errorf("startsWithEmoji(%q) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
}

func TestApplyEmoji(t *testing.T) {
	// Gap-range emoji already present: must not be doubled up.
	if got := applyEmoji("⏰ fix the alarm", "🐌"); got != "⏰ fix the alarm" {
		t.Errorf("expected emoji kept, got %q", got)
	}
	// No emoji present: configured emoji is prepended.
	if got := applyEmoji("fix the alarm", "🐌"); got != "🐌 fix the alarm" {
		t.Errorf("expected prefix added, got %q", got)
	}
}

func TestGenerateModelFirstKeepsModelEmoji(t *testing.T) {
	diff := "M\tsrc/a.go\nM\tsrc/b.go"

	result := GenerateModelFirst(diff, "🐌", 3, "echo '📝 fix the parser'", 30*time.Second, "fake stdin\n")

	if result != "📝 fix the parser" {
		t.Errorf("expected model emoji kept as-is, got %q", result)
	}
}

func TestGenerateWithModelKeepsModelEmoji(t *testing.T) {
	diff := "M\tsrc/a.go\nM\tlib/b.go\nM\tcmd/c.go\nM\tpkg/d.go\nM\tinternal/e.go\nM\ttest/f.go\nM\tdocs/g.go"

	result := GenerateWithModel(diff, "🐌", 3, "echo '📝 fix the parser'", 30*time.Second, "fake full diff\n")

	if result != "📝 fix the parser" {
		t.Errorf("expected model emoji kept as-is, got %q", result)
	}
}

func TestGenerateWithModelInlinePromptCommand(t *testing.T) {
	// GenerateWithModel passes the modelStdin payload through untouched — it
	// never appends the default prompt. Whether a prompt is composed at all is
	// the caller's decision (sync.go composes name-status+prompt only when a
	// prompt is configured; otherwise it passes the full diff).
	diff := "M\tsrc/a.go\nM\tlib/b.go\nM\tcmd/c.go\nM\tpkg/d.go\nM\tinternal/e.go\nM\ttest/f.go\nM\tdocs/g.go"
	modelStdin := "diff --git a/src/a.go b/src/a.go"

	// The command captures all of stdin and reports whether the default prompt
	// appears anywhere in it (regression) or whether the diff content reached
	// stdin (expected pass-through).
	modelCmd := "input=$(cat); if printf '%s\\n' \"$input\" | grep -q 'name-status listing'; then echo 'default prompt appended'; elif printf '%s\\n' \"$input\" | grep -q 'diff --git a/src/a.go b/src/a.go'; then echo 'inline prompt saw the full diff'; else echo 'stdin missing diff'; fi"
	result := GenerateWithModel(diff, "🐌", 3, modelCmd, 30*time.Second, modelStdin)

	if result != "🐌 inline prompt saw the full diff" {
		t.Errorf("expected stdin passed through unchanged, got %q", result)
	}
}

func TestTruncateRunesIsRuneSafe(t *testing.T) {
	tests := []struct {
		name  string
		input string
		max   int
		want  string
	}{
		{name: "short string unchanged", input: "fix bug", max: 72, want: "fix bug"},
		{name: "ascii truncation", input: strings.Repeat("a", 80), max: 72, want: strings.Repeat("a", 72)},
		{name: "multi-byte not split mid-rune", input: strings.Repeat("🐌", 40), max: 72, want: strings.Repeat("🐌", 40)},
		{name: "multi-byte truncation on rune boundary", input: strings.Repeat("🐌", 40), max: 20, want: strings.Repeat("🐌", 20)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncateRunes(tt.input, tt.max); got != tt.want {
				t.Errorf("truncateRunes(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
			}
		})
	}
}
