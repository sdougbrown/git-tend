package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sdougbrown/git-tend/internal/git"
	"github.com/sdougbrown/git-tend/internal/paths"
)

func init() {
	rootCmd.AddCommand(unstickCmd)
}

var unstickCmd = &cobra.Command{
	Use:   "unstick <path>",
	Short: "Remove .gittend.stuck and re-enable a repo",
	Long:  `Remove the .gittend.stuck flag file from the repo at path, allowing the daemon to resume syncing. Validates that path is a git repo first.`,
	Args:  cobra.ExactArgs(1),
	RunE:  unstickRepo,
}

func unstickRepo(cmd *cobra.Command, args []string) error {
	repoPath := paths.ExpandPath(args[0])

	_, err := git.CurrentBranch(repoPath)
	if err != nil {
		return fmt.Errorf("not a git repository: %s", repoPath)
	}

	stuckPath := filepath.Join(repoPath, ".gittend.stuck")
	err = os.Remove(stuckPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("no stuck flag at %s\n", stuckPath)
			return nil
		}
		return fmt.Errorf("removing stuck flag: %w", err)
	}

	fmt.Printf("unstuck %s\n", repoPath)
	return nil
}
