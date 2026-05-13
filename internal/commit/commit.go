package commit

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

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

func GenerateWithModel(diffOutput string, emoji string, fallbackThreshold int, modelCmd string, modelTimeout time.Duration, fullDiff string) string {
	msg := Generate(diffOutput, emoji, fallbackThreshold)

	if modelCmd == "" {
		return msg
	}

	if !isLowInformation(msg) {
		return msg
	}

	if modelTimeout == 0 {
		modelTimeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), modelTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", modelCmd)
	cmd.Stdin = strings.NewReader(fullDiff)
	out, err := cmd.Output()
	if err != nil {
		return msg
	}

	result := strings.TrimSpace(string(out))
	if result == "" {
		return msg
	}

	if len(result) > 72 {
		result = result[:72]
	}

	if emoji != "" && !strings.HasPrefix(result, emoji) {
		result = emoji + " " + result
	}

	return result
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
