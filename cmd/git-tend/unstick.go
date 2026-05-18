package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sdougbrown/git-tend/internal/git"
	"github.com/sdougbrown/git-tend/internal/paths"
	"github.com/sdougbrown/git-tend/internal/status"
)

var unstickAll bool

func init() {
	rootCmd.AddCommand(unstickCmd)
	unstickCmd.Flags().BoolVar(&unstickAll, "all", false, "Unstick all managed repos")
}

var unstickCmd = &cobra.Command{
	Use:   "unstick [<path>]",
	Short: "Remove .gittend.stuck and re-enable a repo",
	Long:  `Remove the .gittend.stuck flag file from a repo, allowing the daemon to resume syncing. Pass --all to unstick every managed repo at once.`,
	Args:  cobra.MaximumNArgs(1),
	RunE:  unstickRepo,
}

func unstickRepo(cmd *cobra.Command, args []string) error {
	if unstickAll {
		return unstickAllRepos()
	}
	if len(args) == 0 {
		return fmt.Errorf("provide a repo path or use --all")
	}
	return removeStuckFlag(paths.ExpandPath(args[0]))
}

func unstickAllRepos() error {
	stateDir := paths.StateDir()
	sf := status.Read(filepath.Join(stateDir, "status.json"))
	if sf == nil || len(sf.Repos) == 0 {
		fmt.Println("No managed repos found.")
		return nil
	}

	anyStuck := false
	for repoPath := range sf.Repos {
		stuckPath := filepath.Join(repoPath, ".gittend.stuck")
		if _, err := os.Stat(stuckPath); os.IsNotExist(err) {
			continue
		}
		anyStuck = true
		if err := removeStuckFlag(repoPath); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}

	if !anyStuck {
		fmt.Println("No stuck repos.")
	}
	return nil
}

func removeStuckFlag(repoPath string) error {
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
