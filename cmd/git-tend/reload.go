package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/sdougbrown/git-tend/internal/paths"
)

func init() {
	rootCmd.AddCommand(reloadCmd)
}

var reloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "SIGHUP the running daemon to rescan roots",
	Long:  `Send SIGHUP to the running daemon to trigger a root rescan and config reload without restarting.`,
	RunE:  runReload,
}

func runReload(cmd *cobra.Command, args []string) error {
	pidPath := filepath.Join(paths.StateDir(), "daemon.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return fmt.Errorf("daemon not running (pid file missing at %s)", pidPath)
	}

	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil || pid <= 0 {
		return fmt.Errorf("invalid pid file at %s", pidPath)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("finding process %d: %w", pid, err)
	}

	if err := process.Signal(syscall.SIGHUP); err != nil {
		return fmt.Errorf("sending SIGHUP to pid %d: %w", pid, err)
	}

	fmt.Println("SIGHUP sent to daemon")
	return nil
}
