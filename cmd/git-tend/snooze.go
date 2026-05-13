package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/sdougbrown/git-tend/internal/paths"
)

var snoozeCmd = &cobra.Command{
	Use:   "snooze <path> [duration]",
	Short: "Suppress surfacing for a stuck repo",
	Long:  `Suppress surfacing for a stuck repo until duration expiry (default 1 day).`,
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runSnooze,
}

func init() {
	rootCmd.AddCommand(snoozeCmd)
}

func runSnooze(cmd *cobra.Command, args []string) error {
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

	data, err := json.MarshalIndent(snoozes, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling snoozes: %w", err)
	}

	if err := os.WriteFile(snoozesPath, data, 0644); err != nil {
		return fmt.Errorf("writing snoozes: %w", err)
	}

	fmt.Printf("Snoozed %s until %s\n", repoPath, until.Format(time.RFC3339))
	return nil
}
