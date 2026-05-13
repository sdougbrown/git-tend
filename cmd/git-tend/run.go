package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sdougbrown/git-tend/internal/config"
	"github.com/sdougbrown/git-tend/internal/paths"
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
	result := sync.Sync(ctx, repoPath, cfg, stateDir)

	fmt.Printf("state: %s\n", result.State)
	if result.Error != "" {
		fmt.Printf("error: %s\n", result.Error)
	}
	if result.State == "ok" {
		return nil
	}
	return fmt.Errorf("sync failed (%s): %s", result.State, result.Error)
}
