package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sdougbrown/git-tend/internal/paths"
	"github.com/sdougbrown/git-tend/internal/status"
)

var (
	promptShowOffline bool
	promptAnyStuck    bool
	promptNoColor     bool
)

var promptCmd = &cobra.Command{
	Use:   "prompt",
	Short: "Print short status segment for shell prompts",
	Long:  `Print a short status segment for shell prompts. Empty when clean.`,
	RunE:  runPrompt,
}

func init() {
	promptCmd.Flags().BoolVar(&promptShowOffline, "show-offline", false, "Include offline repos in the prompt segment")
	promptCmd.Flags().BoolVar(&promptAnyStuck, "any-stuck", false, "Exit 1 when stuck repos exist")
	promptCmd.Flags().BoolVar(&promptNoColor, "no-color", false, "Disable ANSI color output")
	rootCmd.AddCommand(promptCmd)
}

func runPrompt(cmd *cobra.Command, args []string) error {
	if os.Getenv("NO_COLOR") != "" {
		promptNoColor = true
	}

	sf := status.Read(filepath.Join(paths.StateDir(), "status.json"))
	if sf == nil {
		return nil
	}

	staleAfter := 2 * time.Duration(sf.IntervalSeconds) * time.Second
	var down bool
	if sf.LastTickAt != "" {
		t, err := time.Parse(time.RFC3339, sf.LastTickAt)
		if err == nil && time.Since(t) > staleAfter {
			down = true
		}
	}

	stuckCount := 0
	offlineCount := 0
	for _, rs := range sf.Repos {
		if rs.CurrentState == "stuck" {
			stuckCount++
		}
		if rs.CurrentState == "offline" {
			offlineCount++
		}
	}

	var parts []string

	if down {
		if promptNoColor {
			parts = append(parts, "\u26a0 tend:down")
		} else {
			parts = append(parts, "\033[31m\u26a0 tend:down\033[0m")
		}
	} else if stuckCount > 0 {
		if promptNoColor {
			parts = append(parts, fmt.Sprintf("\u26a0 tend:%d", stuckCount))
		} else {
			parts = append(parts, fmt.Sprintf("\033[33m\u26a0 tend:%d\033[0m", stuckCount))
		}
	}

	if promptShowOffline && offlineCount > 0 {
		if promptNoColor {
			parts = append(parts, fmt.Sprintf("tend:offline:%d", offlineCount))
		} else {
			parts = append(parts, fmt.Sprintf("\033[33mtend:offline:%d\033[0m", offlineCount))
		}
	}

	output := strings.Join(parts, " ")
	if output != "" {
		fmt.Print(output)
	}

	if promptAnyStuck && stuckCount > 0 {
		os.Exit(1)
	}

	return nil
}
