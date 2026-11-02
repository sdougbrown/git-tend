package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/sdougbrown/cli-contract"

	"github.com/sdougbrown/git-tend/internal/config"
	"github.com/sdougbrown/git-tend/internal/paths"
	"github.com/sdougbrown/git-tend/internal/status"
	"github.com/sdougbrown/git-tend/internal/sync"
)

var runCmd = &clicontract.Command{
	Name:  "run",
	Short: "One-shot sync of a single repo",
	Long: `Run one sync cycle for the git repo at path. The repo must be opted in
by a .gittend config or a [[repo]] entry in the user config.`,
	Positionals: []clicontract.Positional{{Name: "path", Required: true}},
	Handle: func(ctx context.Context, inv clicontract.Invocation) (any, error) {
		return nil, runRepo(ctx, inv.Args[0])
	},
}

func runRepo(ctx context.Context, repoPathArg string) error {
	repoPath := paths.ExpandPath(repoPathArg)

	userCfg, err := config.ParseUserConfig(filepath.Join(paths.ConfigDir(), "config.toml"))
	if err != nil {
		return fmt.Errorf("loading user config: %w", err)
	}
	cfg, err := config.ResolveRepo(repoPath, userCfg)
	if err != nil {
		return fmt.Errorf("parsing config: %w", err)
	}

	stateDir := paths.StateDir()
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
