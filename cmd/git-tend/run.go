package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/sdougbrown/git-tend/internal/config"
	"github.com/sdougbrown/git-tend/internal/paths"
	"github.com/sdougbrown/git-tend/internal/status"
	"github.com/sdougbrown/git-tend/internal/sync"
)

func init() {
	rootCmd.AddCommand(runCmd)
}

var runCmd = &cobra.Command{
	Use:   "run [path]",
	Short: "One-shot sync of a single repo",
	Long:  `Run one sync cycle for the git repo at path. Path must contain a .gittend config.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runRepo,
}

func runRepo(cmd *cobra.Command, args []string) error {
	repoPath := paths.ExpandPath(args[0])

	cfg, err := config.Parse(filepath.Join(repoPath, ".gittend"))
	if err != nil {
		return fmt.Errorf("parsing config: %w", err)
	}

	stateDir := paths.StateDir()
	ctx := context.Background()
	result := sync.SyncManual(ctx, repoPath, cfg, stateDir)
	if result.State != "skipped" {
		if err := recordRunStatus(filepath.Join(stateDir, "status.json"), repoPath, cfg.Mode, result); err != nil {
			return fmt.Errorf("recording status: %w", err)
		}
	}

	fmt.Printf("state: %s\n", result.State)
	if result.Error != "" {
		fmt.Printf("error: %s\n", result.Error)
	}
	if result.State == "ok" {
		return nil
	}
	return fmt.Errorf("sync failed (%s): %s", result.State, result.Error)
}

func recordRunStatus(statusPath, repoPath, mode string, result sync.SyncResult) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return status.UpdateRepo(statusPath, repoPath, func(rs status.RepoStatus) status.RepoStatus {
		rs.Mode = mode
		rs.UpdatedAt = now

		switch result.State {
		case "ok":
			rs.PriorState = rs.CurrentState
			rs.CurrentState = "ok"
			rs.LastSyncAt = now
			rs.LastError = ""
			rs.Ahead = result.Ahead
			rs.Behind = result.Behind
			rs.StuckSince = ""
			rs.SnoozedUntil = ""
			rs.OfflineSince = ""
			rs.ConsecutiveOfflineFailures = 0
		case "offline":
			rs.PriorState = rs.CurrentState
			rs.CurrentState = "offline"
			rs.LastError = result.Error
			if rs.OfflineSince == "" {
				rs.OfflineSince = now
			}
			rs.ConsecutiveOfflineFailures++
		case "stuck":
			rs.PriorState = rs.CurrentState
			rs.CurrentState = "stuck"
			rs.LastError = result.Error
			if rs.StuckSince == "" {
				rs.StuckSince = now
			}
		}
		return rs
	})
}
