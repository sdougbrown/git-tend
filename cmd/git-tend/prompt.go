package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sdougbrown/cli-contract"

	"github.com/sdougbrown/git-tend/internal/paths"
	"github.com/sdougbrown/git-tend/internal/status"
)

var promptCmd = &clicontract.Command{
	Name:  "prompt",
	Short: "Print short status segment for shell prompts",
	Long:  `Print a short status segment for shell prompts. Empty when clean.`,
	Flags: []clicontract.Flag{
		{Name: "show-offline", Kind: clicontract.Bool, Usage: "Include offline repos in the prompt segment"},
		{Name: "any-stuck", Kind: clicontract.Bool, Usage: "Exit 1 when stuck repos exist"},
		{Name: "no-color", Kind: clicontract.Bool, Usage: "Disable ANSI color output"},
	},
	Handle: func(ctx context.Context, inv clicontract.Invocation) (any, error) {
		return runPrompt(promptOpts{
			showOffline: inv.Bools["show-offline"],
			anyStuck:    inv.Bools["any-stuck"],
			noColor:     inv.Bools["no-color"],
		})
	},
}

type promptOpts struct {
	showOffline bool
	anyStuck    bool
	noColor     bool
}

// runPrompt prints the segment and returns the stuck-repo count.
func runPrompt(opts promptOpts) (int, error) {
	if os.Getenv("NO_COLOR") != "" {
		opts.noColor = true
	}

	sf := status.Read(filepath.Join(paths.StateDir(), "status.json"))
	if sf == nil {
		return 0, nil
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
		if opts.noColor {
			parts = append(parts, "\u26a0 tend:down")
		} else {
			parts = append(parts, "\033[31m\u26a0 tend:down\033[0m")
		}
	} else if stuckCount > 0 {
		if opts.noColor {
			parts = append(parts, fmt.Sprintf("\u26a0 tend:%d", stuckCount))
		} else {
			parts = append(parts, fmt.Sprintf("\033[33m\u26a0 tend:%d\033[0m", stuckCount))
		}
	}

	if opts.showOffline && offlineCount > 0 {
		if opts.noColor {
			parts = append(parts, fmt.Sprintf("tend:offline:%d", offlineCount))
		} else {
			parts = append(parts, fmt.Sprintf("\033[33mtend:offline:%d\033[0m", offlineCount))
		}
	}

	output := strings.Join(parts, " ")
	if output != "" {
		fmt.Print(output)
	}

	return stuckCount, nil
}
