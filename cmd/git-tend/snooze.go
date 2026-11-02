package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sdougbrown/cli-contract"

	"github.com/sdougbrown/git-tend/internal/paths"
)

var snoozeCmd = &clicontract.Command{
	Name:  "snooze",
	Short: "Suppress surfacing for a stuck repo",
	Long:  `Suppress surfacing for a stuck repo until duration expiry (default 1 day).`,
	Positionals: []clicontract.Positional{
		{Name: "path", Required: true},
		{Name: "duration"},
	},
	Handle: func(ctx context.Context, inv clicontract.Invocation) (any, error) {
		return nil, runSnooze(inv.Args)
	},
}

func runSnooze(args []string) error {
	repoPath := paths.ExpandPath(args[0])

	duration := 24 * time.Hour
	if len(args) >= 2 {
		var err error
		duration, err = time.ParseDuration(args[1])
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", args[1], err)
		}
	}

	until := time.Now().Add(duration).UTC()

	stateDir := paths.StateDir()
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}

	snoozesPath := filepath.Join(stateDir, "snoozes.json")
	snoozes := make(map[string]string)

	if data, err := os.ReadFile(snoozesPath); err == nil {
		json.Unmarshal(data, &snoozes)
	}

	snoozes[repoPath] = until.Format(time.RFC3339)

	if data, err := json.MarshalIndent(snoozes, "", "  "); err != nil {
		return fmt.Errorf("encoding snoozes: %w", err)
	} else if err := os.WriteFile(snoozesPath, data, 0644); err != nil {
		return fmt.Errorf("writing snoozes: %w", err)
	}

	fmt.Printf("snoozed %s until %s\n", repoPath, until.Format("2006-01-02 15:04 MST"))
	return nil
}
