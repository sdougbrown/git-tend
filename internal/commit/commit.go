package commit

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// DefaultModelPrompt is the built-in prompt used when a repo opts into the
// model strategy without configuring its own model_prompt. It names the
// stdin content explicitly: models that aren't told what stdin holds often
// claim they can't see the input.
const DefaultModelPrompt = `Stdin contains a git diff --cached --name-status listing (status TAB path).
Write one short imperative git commit message, max 72 characters, summarizing
these changes. Output only the message text itself.`

// MaxAgentDiffBytes caps the full diff handed to the agent command. Larger
// diffs are cut at a line boundary and a note is appended so the agent knows
// the tail is missing. This bounds token cost for a daemon that may process
// arbitrarily large staged changes.
const MaxAgentDiffBytes = 16 * 1024

// maxAgentMessageRunes caps the agent's message length. Unlike the other
// strategies the agent may produce multi-line messages (subject + body), so
// the 72-rune subject cap does not apply; this only stops pathological output.
const maxAgentMessageRunes = 2000

// maxAgentStdoutBytes bounds how much agent stdout the daemon will buffer.
// 2000 runes can never exceed 8000 bytes of UTF-8, so this always covers the
// message cap while a runaway agent command cannot grow the daemon's memory.
const maxAgentStdoutBytes = maxAgentMessageRunes * 4

// DefaultAgentPrompt is the built-in prompt used when a repo opts into the
// agent strategy without configuring its own model_prompt. It names the
// stdin content explicitly: models that aren't told what stdin holds often
// claim they can't see the input.
const DefaultAgentPrompt = `Stdin contains the repository path, the current branch,
the full "git diff --cached" output (possibly truncated with a note), and this
prompt. Write a git commit message for these changes: one imperative subject
line of at most 72 characters, optionally followed by a blank line and a short
body explaining why the change was made. Output only the message text itself.`

type change struct {
	status  string
	path    string
	oldPath string
}

func Generate(diffOutput string, emoji string, fallbackThreshold int) string {
	diffOutput = strings.TrimSpace(diffOutput)
	if diffOutput == "" {
		return ""
	}

	lines := strings.Split(diffOutput, "\n")
	changes := make([]change, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		status := parts[0]

		if strings.HasPrefix(status, "R") {
			if len(parts) >= 3 {
				changes = append(changes, change{
					status:  "R",
					path:    parts[2],
					oldPath: parts[1],
				})
			}
		} else {
			if len(parts) >= 2 {
				changes = append(changes, change{
					status: status,
					path:   parts[1],
				})
			}
		}
	}

	if len(changes) == 0 {
		return ""
	}

	n := len(changes)
	prefix := emoji
	if prefix != "" {
		prefix += " "
	}

	if n == 1 && changes[0].status == "A" {
		return prefix + "add " + changes[0].path
	}

	if n == 1 && changes[0].status == "M" {
		return prefix + "update " + changes[0].path
	}

	if n == 1 && changes[0].status == "D" {
		return prefix + "remove " + changes[0].path
	}

	if n == 1 && changes[0].status == "R" {
		return prefix + "rename " + changes[0].oldPath + " → " + changes[0].path
	}

	dirs := make(map[string]bool)
	for _, c := range changes {
		dirs[topLevelDir(c.path)] = true
	}

	dirList := make([]string, 0, len(dirs))
	for d := range dirs {
		dirList = append(dirList, d)
	}
	sort.Strings(dirList)

	if len(dirList) == 1 && n <= 5 {
		return fmt.Sprintf("%supdate %s/ (%d files)", prefix, dirList[0], n)
	}

	if len(dirList) <= fallbackThreshold {
		return fmt.Sprintf("%ssync %s (%d files)", prefix, strings.Join(dirList, ", "), n)
	}

	return fmt.Sprintf("%ssync %d files", prefix, n)
}

func topLevelDir(path string) string {
	idx := strings.Index(path, "/")
	if idx == -1 {
		return "."
	}
	return path[:idx]
}

// GenerateWithModel is the deterministic strategy's model consultation: the
// heuristic message is used unless it is low-information, in which case
// modelCmd is run with modelStdin on stdin. On timeout, error, or empty
// output the heuristic message stands.
func GenerateWithModel(diffOutput string, emoji string, fallbackThreshold int, modelCmd string, modelTimeout time.Duration, modelStdin string) string {
	msg := Generate(diffOutput, emoji, fallbackThreshold)

	if modelCmd == "" {
		return msg
	}

	if !isLowInformation(msg) {
		return msg
	}

	result, ok := runModel(modelCmd, modelTimeout, modelStdin)
	if !ok {
		return msg
	}

	return applyEmoji(result, emoji)
}

// GenerateModelFirst is the model strategy: modelCmd is always consulted
// with modelStdin on stdin, falling back to the heuristic message on
// timeout, error, or empty output.
func GenerateModelFirst(diffOutput string, emoji string, fallbackThreshold int, modelCmd string, modelTimeout time.Duration, modelStdin string) string {
	fallback := Generate(diffOutput, emoji, fallbackThreshold)

	if modelCmd == "" {
		return fallback
	}

	result, ok := runModel(modelCmd, modelTimeout, modelStdin)
	if !ok {
		return fallback
	}

	return applyEmoji(result, emoji)
}

// GenerateAgent is the agent strategy: the agent command receives the full
// staged diff on stdin (plus repo path and branch) and its stdout is the
// commit message, possibly multi-line. The daemon still runs git commit
// itself — the agent is a message author, never an executor.
//
// Cost guardrail: the agent is consulted only when the heuristic message is
// low-information ("sync N files"), the same trigger the deterministic
// strategy uses to fall back to model_cmd. Small, well-described changes get
// the heuristic message and never invoke the agent.
//
// On agent error, timeout, or empty output the heuristic message stands.
func GenerateAgent(nameStatusDiff, fullDiff, repoPath, branch, prompt, emoji string, fallbackThreshold int, agentCmd string, timeout time.Duration) string {
	msg := Generate(nameStatusDiff, emoji, fallbackThreshold)

	if agentCmd == "" {
		return msg
	}

	if !isLowInformation(msg) {
		return msg
	}

	if prompt == "" {
		prompt = DefaultAgentPrompt
	}

	result, ok := runAgent(agentCmd, timeout, agentStdin(fullDiff, repoPath, branch, prompt))
	if !ok {
		return msg
	}

	return applyEmoji(result, emoji)
}

// agentStdin composes the agent command's stdin payload: repo context, the
// capped full diff, and the prompt.
func agentStdin(fullDiff, repoPath, branch, prompt string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "repo: %s\nbranch: %s\n\n", repoPath, branch)
	b.WriteString(truncateDiff(fullDiff, MaxAgentDiffBytes))
	b.WriteString("\n")
	b.WriteString(prompt)
	b.WriteString("\n")
	return b.String()
}

// truncateDiff caps diff at max bytes, cutting at a line boundary so the agent
// never sees a partial line, and appends a note saying the tail was dropped.
func truncateDiff(diff string, max int) string {
	if len(diff) <= max {
		return diff
	}
	cut := diff[:max]
	if idx := strings.LastIndexByte(cut, '\n'); idx >= 0 {
		cut = cut[:idx+1]
	} else {
		// No newline in the window (a single minified line): cut at a whole
		// rune boundary so the result is still valid UTF-8.
		for len(cut) > 0 {
			r, size := utf8.DecodeLastRuneInString(cut)
			if r != utf8.RuneError || size != 1 {
				break
			}
			cut = cut[:len(cut)-1]
		}
	}
	return cut + fmt.Sprintf("[git-tend: diff truncated at %d bytes; the remainder is not shown]\n", max)
}

// boundedWriter stores up to max bytes and silently discards the rest, so a
// runaway command writing far more than the message cap cannot buffer
// unbounded output in the daemon's memory.
type boundedWriter struct {
	buf []byte
	max int
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	room := w.max - len(w.buf)
	if room > 0 {
		if room > len(p) {
			room = len(p)
		}
		w.buf = append(w.buf, p[:room]...)
	}
	return len(p), nil
}

func runAgent(agentCmd string, timeout time.Duration, stdin string) (string, bool) {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", agentCmd)
	cmd.Stdin = strings.NewReader(stdin)
	var out boundedWriter
	out.max = maxAgentStdoutBytes
	cmd.Stdout = &out
	// Without WaitDelay, a grandchild of sh that outlives it and holds the
	// stdout pipe would keep Wait blocked past the context deadline while the
	// per-repo flock is held; WaitDelay closes the pipes and returns instead.
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		return "", false
	}

	// NUL bytes are invalid in a git commit message (git commit -m fails with
	// EINVAL), so strip them and fall back via the emptiness check if nothing
	// usable remains.
	result := strings.ReplaceAll(string(out.buf), "\x00", "")
	result = strings.TrimSpace(result)
	if result == "" {
		return "", false
	}

	return truncateRunes(result, maxAgentMessageRunes), true
}

func runModel(modelCmd string, modelTimeout time.Duration, modelStdin string) (string, bool) {
	if modelTimeout == 0 {
		modelTimeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), modelTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", modelCmd)
	cmd.Stdin = strings.NewReader(modelStdin)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}

	result := strings.TrimSpace(string(out))
	if result == "" {
		return "", false
	}

	return truncateRunes(result, 72), true
}

// applyEmoji prepends the configured emoji unless the message already
// starts with one, so a model that picked its own emoji doesn't end up
// doubled up (e.g. "🐌 📝 ...").
func applyEmoji(result, emoji string) string {
	if emoji != "" && !startsWithEmoji(result) {
		return emoji + " " + result
	}
	return result
}

func startsWithEmoji(s string) bool {
	r, size := utf8.DecodeRuneInString(s)
	// A leading U+FE0F variation selector qualifies the next rune as emoji
	// presentation; skip it so a selector-qualified emoji is still detected.
	if r == 0xFE0F {
		r, _ = utf8.DecodeRuneInString(s[size:])
	}
	switch {
	case r >= 0x1F300 && r <= 0x1FAFF, // pictographs, supplemental symbols
		r >= 0x1F1E6 && r <= 0x1F1FF, // regional indicators (flags)
		r >= 0x1F170 && r <= 0x1F1E5, // enclosed alphanumeric supplement (🅰 🆒 🆕)
		r >= 0x1F200 && r <= 0x1F2FF, // enclosed ideographic supplement (🈁 🈳)
		r >= 0x2300 && r <= 0x23FF,   // misc technical (⌚ ⏰ ⏳ ⏸)
		r >= 0x2500 && r <= 0x25FF,   // geometric shapes (▶ ◀ ▲ ▼)
		r >= 0x2600 && r <= 0x27BF,   // misc symbols, dingbats
		// Misc symbols and arrows: only the RGI emoji codepoints, not the
		// plain arrows in the block (⬀–⬄).
		r >= 0x2B05 && r <= 0x2B07, // ⬅ ⬆ ⬇
		r >= 0x2B1B && r <= 0x2B1C, // ⬛ ⬜
		r == 0x2B50 || r == 0x2B55: // ⭐ ⭕
		return true
	}
	return false
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

func isLowInformation(msg string) bool {
	if !strings.Contains(msg, "sync ") {
		return false
	}
	if strings.Contains(msg, "(") || strings.Contains(msg, ",") {
		return false
	}
	if !strings.HasSuffix(msg, " files") {
		return false
	}
	rest := msg[:len(msg)-len(" files")]
	lastSpace := strings.LastIndex(rest, " ")
	if lastSpace == -1 {
		return false
	}
	digits := rest[lastSpace+1:]
	if digits == "" {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
