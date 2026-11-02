package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/sdougbrown/cli-contract"

	"github.com/sdougbrown/git-tend/internal/paths"
)

var reloadCmd = &clicontract.Command{
	Name:  "reload",
	Short: "SIGHUP the running daemon to rescan roots",
	Long:  `Send SIGHUP to the running daemon to trigger a root rescan and config reload without restarting.`,
	Handle: func(ctx context.Context, inv clicontract.Invocation) (any, error) {
		return nil, runReload()
	},
}

func runReload() error {
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
