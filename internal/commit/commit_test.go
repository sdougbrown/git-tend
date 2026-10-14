package commit

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

// agentLowInfoDiff produces a heuristic message of "sync N files" so the
// agent strategy's low-information trigger fires.
func agentLowInfoDiff() string {
	return "M\ta/1\nM\ta/2\nM\ta/3\nM\tb/1\nM\tb/2\nM\tb/3"
}

func TestGenerateAgentPassesMultiLineMessageThrough(t *testing.T) {
	agentCmd := "printf 'subject line\n\nbody explaining the why\n'"
	result := GenerateAgent(agentLowInfoDiff(), "full diff", "/repo", "main", "", "🐌", 1, agentCmd, 30*time.Second)

	want := "🐌 subject line\n\nbody explaining the why"
	if result != want {
		t.Errorf("GenerateAgent() = %q, want %q", result, want)
	}
}

func TestGenerateAgentKeepsAgentEmoji(t *testing.T) {
	result := GenerateAgent(agentLowInfoDiff(), "full diff", "/repo", "main", "", "🐌", 1, "echo '📝 rewrite the parser'", 30*time.Second)

	if result != "📝 rewrite the parser" {
		t.Errorf("GenerateAgent() = %q, want %q", result, "📝 rewrite the parser")
	}
}

func TestGenerateAgentFallbackOnEmptyOutput(t *testing.T) {
	result := GenerateAgent(agentLowInfoDiff(), "full diff", "/repo", "main", "", "🐌", 1, "true", 5*time.Second)

	if !strings.HasPrefix(result, "🐌 sync ") {
		t.Errorf("expected heuristic fallback, got %q", result)
	}
}

func TestGenerateAgentFallbackOnError(t *testing.T) {
	result := GenerateAgent(agentLowInfoDiff(), "full diff", "/repo", "main", "", "🐌", 1, "exit 1", 5*time.Second)

	if !strings.HasPrefix(result, "🐌 sync ") {
		t.Errorf("expected heuristic fallback, got %q", result)
	}
}

func TestGenerateAgentFallbackOnTimeout(t *testing.T) {
	result := GenerateAgent(agentLowInfoDiff(), "full diff", "/repo", "main", "", "🐌", 1, "sleep 60", 100*time.Millisecond)

	if !strings.HasPrefix(result, "🐌 sync ") {
		t.Errorf("expected heuristic fallback, got %q", result)
	}
}

func TestGenerateAgentNotConsultedForInformativeHeuristic(t *testing.T) {
	// The heuristic message "add foo.txt" is informative, so the agent must
	// never run — this is the cost guardrail for small changes.
	result := GenerateAgent("A\tfoo.txt", "full diff", "/repo", "main", "", "🐌", 1, "echo 'agent wrote this'", 30*time.Second)

	if result != "🐌 add foo.txt" {
		t.Errorf("GenerateAgent() = %q, want %q", result, "🐌 add foo.txt")
	}
}

func TestGenerateAgentReceivesStdin(t *testing.T) {
	fullDiff := "diff --git a/src/a.go b/src/a.go\n--- a/src/a.go\n+++ b/src/a.go"
	agentCmd := `while IFS= read -r line; do
	case "$line" in
		repo:*) echo "saw repo" ;;
		branch:*) echo "saw branch" ;;
		diff\ --git*) echo "saw diff" ;;
		prompt*) echo "saw prompt" ;;
	esac
done`
	result := GenerateAgent(agentLowInfoDiff(), fullDiff, "/tmp/repo", "feature", "prompt text here", "🐌", 1, agentCmd, 30*time.Second)

	for _, want := range []string{"saw repo", "saw branch", "saw diff", "saw prompt"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in agent output, got %q", want, result)
		}
	}
}

func TestGenerateAgentUsesDefaultPrompt(t *testing.T) {
	agentCmd := "if grep -q 'imperative subject'; then echo 'default prompt present'; else echo 'default prompt missing'; fi"
	result := GenerateAgent(agentLowInfoDiff(), "full diff", "/repo", "main", DefaultAgentPrompt, "🐌", 1, agentCmd, 30*time.Second)

	if result != "🐌 default prompt present" {
		t.Errorf("expected default prompt on stdin, got %q", result)
	}
}

func TestGenerateAgentCustomPromptOverridesDefault(t *testing.T) {
	agentCmd := "if grep -q 'house style'; then echo 'custom prompt used'; else echo 'default used'; fi"
	result := GenerateAgent(agentLowInfoDiff(), "full diff", "/repo", "main", "Use the house style.", "🐌", 1, agentCmd, 30*time.Second)

	if result != "🐌 custom prompt used" {
		t.Errorf("expected custom prompt on stdin, got %q", result)
	}
}

func TestTruncateDiff(t *testing.T) {
	tests := []struct {
		name string
		diff string
		max  int
	}{
		{name: "under cap unchanged", diff: "short diff", max: 100},
		{name: "empty diff unchanged", diff: "", max: 100},
		{name: "exactly at cap unchanged", diff: strings.Repeat("a\n", 10), max: 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateDiff(tt.diff, tt.max)
			if got != tt.diff {
				t.Errorf("truncateDiff() = %q, want unchanged %q", got, tt.diff)
			}
		})
	}

	t.Run("no newline cuts at line boundary", func(t *testing.T) {
		// A single minified line with no newline in the window: the cut must
		// still land on a whole-rune boundary (valid UTF-8), not mid-rune.
		diff := strings.Repeat("é", 30) // 60 bytes, no newline
		got := truncateDiff(diff, 25)
		if !strings.Contains(got, "[git-tend: diff truncated") {
			t.Errorf("expected truncation note in output, got %q", got)
		}
		body := strings.TrimSuffix(got, truncationNote(25))
		if !strings.HasPrefix(diff, body) {
			t.Errorf("expected cut content to be a prefix of the diff, got %q", body)
		}
		if !utf8.ValidString(body) {
			t.Errorf("expected valid UTF-8 after cut, got %q", body)
		}
		if got := truncateDiff("\n"+strings.Repeat("a", 30), 10); !strings.HasPrefix(got, "\n") {
			t.Errorf("expected newline at index 0 to be kept, got %q", got)
		}
	})

	t.Run("mid-string newline cuts just after it", func(t *testing.T) {
		// A window that contains a newline somewhere in the middle (not at
		// index 0): the cut must end just after that newline, so the agent
		// never sees a partial line.
		diff := "abcdefghij\nklmnopqrst"
		got := truncateDiff(diff, 15)
		if !strings.Contains(got, "[git-tend: diff truncated") {
			t.Errorf("expected truncation note in output, got %q", got)
		}
		body := strings.TrimSuffix(got, truncationNote(15))
		if want := "abcdefghij\n"; body != want {
			t.Errorf("expected cut to end just after the mid-string newline, got %q, want %q", body, want)
		}
		if !strings.HasSuffix(body, "\n") {
			t.Errorf("expected cut to end with a newline, got %q", body)
		}
	})
}

// truncationNote reconstructs the note truncateDiff appends for a given cap.
func truncationNote(max int) string {
	return fmt.Sprintf("[git-tend: diff truncated at %d bytes; the remainder is not shown]\n", max)
}

func TestRunAgentCapsMessageLength(t *testing.T) {
	long := strings.Repeat("x", maxAgentMessageRunes+500)
	got, ok := runAgent("cat", 30*time.Second, long)
	if !ok {
		t.Fatal("expected ok output")
	}
	if got != strings.Repeat("x", maxAgentMessageRunes) {
		t.Errorf("expected message capped at %d runes, got %d", maxAgentMessageRunes, len([]rune(got)))
	}
}

func TestRunAgentStripsNULBytes(t *testing.T) {
	// NUL bytes make `git commit -m` fail with EINVAL, so the agent's output
	// must come out clean instead of wedging the repo in a stuck state.
	got, ok := runAgent(`printf 'msg\000tail'`, 30*time.Second, "")
	if !ok {
		t.Fatal("expected ok output")
	}
	if strings.ContainsRune(got, '\x00') {
		t.Errorf("expected NUL bytes stripped, got %q", got)
	}
	if got != "msgtail" {
		t.Errorf("expected NUL removal to join text, got %q", got)
	}
}

func TestRunAgentWaitDelayDoesNotBlockOnBackgroundedGrandchild(t *testing.T) {
	// A backgrounded grandchild (sleep 60 &) holds the stdout pipe open after
	// sh exits. Without cmd.WaitDelay, cmd.Wait would block for the
	// grandchild's lifetime (~60s) while the per-repo flock is held; with it,
	// the pipes are closed and the wait returns well within timeout+WaitDelay.
	start := time.Now()
	_, ok := runAgent("sleep 60 &", 100*time.Millisecond, "")
	elapsed := time.Since(start)
	if ok {
		t.Fatalf("expected ok=false for a backgrounded grandchild holding the pipe")
	}
	if elapsed > 15*time.Second {
		t.Errorf("runAgent took %v, expected well under timeout+WaitDelay (~5s); the backgrounded sleep must not block the wait", elapsed)
	}
}

func TestBoundedWriterDiscardsBeyondCap(t *testing.T) {
	w := &boundedWriter{max: 5}
	n, err := w.Write([]byte("abcdefgh"))
	if err != nil || n != 8 {
		t.Fatalf("Write() = (%d, %v), want (8, nil)", n, err)
	}
	if string(w.buf) != "abcde" {
		t.Errorf("expected only the first 5 bytes buffered, got %q", w.buf)
	}
}
