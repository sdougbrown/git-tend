package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/sdougbrown/git-tend/internal/paths"
)

func init() {
	rootCmd.AddCommand(restartCmd)
}

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Stop the running daemon and let the service manager respawn it",
	Long: `Send SIGTERM to the running daemon. The service manager (launchd on macOS,
systemd on Linux) respawns it against whatever binary the symlink/service file
points to. Use this after rebuilding to pick up new code, or any time you want
a fresh daemon process. Does not fetch or rebuild — that's on you.`,
	RunE: runRestart,
}

func runRestart(cmd *cobra.Command, args []string) error {
	pidPath := filepath.Join(paths.StateDir(), "daemon.pid")
	oldPid, err := readDaemonPid(pidPath)
	if err != nil {
		return err
	}

	proc, err := os.FindProcess(oldPid)
	if err != nil {
		return fmt.Errorf("finding process %d: %w", oldPid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("sending SIGTERM to pid %d: %w", oldPid, err)
	}
	fmt.Printf("sent SIGTERM to pid %d, waiting for respawn...\n", oldPid)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		newPid, err := readDaemonPid(pidPath)
		if err != nil {
			continue
		}
		if newPid == oldPid {
			continue
		}
		if syscall.Kill(newPid, 0) != nil {
			continue
		}
		fmt.Printf("daemon restarted: pid %d → %d\n", oldPid, newPid)
		return nil
	}

	return fmt.Errorf("daemon did not respawn within 10s — is the service loaded? try 'git-tend install'")
}

func readDaemonPid(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, fmt.Errorf("daemon not running (pid file missing at %s)", path)
		}
		return 0, fmt.Errorf("reading pid file %s: %w", path, err)
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid pid file at %s", path)
	}
	return pid, nil
}
