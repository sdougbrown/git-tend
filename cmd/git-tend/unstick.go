package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/sdougbrown/git-tend/internal/git"
	"github.com/sdougbrown/git-tend/internal/paths"
	"github.com/sdougbrown/git-tend/internal/status"
	gitSync "github.com/sdougbrown/git-tend/internal/sync"
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

	// Hold the agent lock around the whole removal: an in-flight agent run
	// holds the same lock and writes the attempt record at its end, and
	// deleting state underneath it would race that write.
	stateDir := paths.StateDir()
	lock, err := gitSync.AcquireAgentLock(stateDir, repoPath)
	if err != nil {
		return fmt.Errorf("agent run in flight for %s; retry after it finishes: %w", repoPath, err)
	}
	defer lock.Close()

	stuckPath := filepath.Join(repoPath, ".gittend.stuck")
	err = os.Remove(stuckPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("no stuck flag at %s\n", stuckPath)
			return nil
		}
		return fmt.Errorf("removing stuck flag: %w", err)
	}

	// Reset the agent attempt counter and the daemon-issued stuck record so a
	// fresh stuck episode starts with a full budget of attempts.
	for _, path := range []string{
		gitSync.AgentStatePath(stateDir, repoPath),
		gitSync.StuckRecordPath(stateDir, repoPath),
	} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("resetting agent state: %w", err)
		}
	}

	// The flag is already gone, so a status write failure is reported but
	// does not fail the unstick.
	if err := refreshStatusOnUnstick(stateDir, repoPath); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update status for %s: %v\n", repoPath, err)
	}

	fmt.Printf("unstuck %s\n", repoPath)
	return nil
}

// refreshStatusOnUnstick moves an existing status entry out of stuck so
// `git-tend status` reflects the unstick before the daemon's next tick. A repo
// the daemon has not recorded is left absent.
func refreshStatusOnUnstick(stateDir, repoPath string) error {
	statusPath := filepath.Join(stateDir, "status.json")
	sf := status.Read(statusPath)
	if sf == nil {
		return nil
	}
	if _, ok := sf.Repos[repoPath]; !ok {
		return nil
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	return status.UpdateRepo(statusPath, repoPath, func(rs status.RepoStatus) status.RepoStatus {
		rs.PriorState = rs.CurrentState
		rs.CurrentState = "pending"
		rs.StuckSince = ""
		rs.AgentAttempts = 0
		rs.AgentMaxAttempts = 0
		rs.UpdatedAt = now
		return rs
	})
}
