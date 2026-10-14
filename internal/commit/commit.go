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

func runModel(modelCmd string, modelTimeout time.Duration, modelStdin string) (string, bool) {
	if modelTimeout == 0 {
		modelTimeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), modelTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", modelCmd)
	cmd.Stdin = strings.NewReader(modelStdin)
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
	r, _ := utf8.DecodeRuneInString(s)
	switch {
	case r >= 0x1F300 && r <= 0x1FAFF, // pictographs, supplemental symbols
		r >= 0x1F1E6 && r <= 0x1F1FF, // regional indicators (flags)
		r >= 0x2600 && r <= 0x27BF,   // misc symbols, dingbats
		r >= 0x2B00 && r <= 0x2BFF:   // misc symbols and arrows (e.g. ⭐)
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
