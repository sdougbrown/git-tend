package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/sdougbrown/git-tend/internal/install"
	"github.com/sdougbrown/git-tend/internal/paths"
)

func init() {
	rootCmd.AddCommand(restartCmd)
}

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the daemon via the service manager",
	Long: `Restart the running daemon via systemd (Linux) or launchd (macOS).
Use this after rebuilding to pick up a new binary, or any time you want
a fresh daemon process. Does not fetch or rebuild — that's on you.`,
	RunE: runRestart,
}

func runRestart(cmd *cobra.Command, args []string) error {
	pidPath := filepath.Join(paths.StateDir(), "daemon.pid")
	oldPid, err := readDaemonPid(pidPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var smErr error
	switch {
	case install.IsMacOS():
		smErr = exec.Command("launchctl", "kickstart", "-kp", install.LaunchdLabel).Run()
	case install.IsLinux():
		smErr = exec.Command("systemctl", "--user", "restart", "git-tend").Run()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	if smErr != nil {
		return fmt.Errorf("service manager restart failed: %w (is the service loaded? try 'git-tend install')", smErr)
	}

	if oldPid > 0 {
		fmt.Printf("restarting daemon (was pid %d)...\n", oldPid)
	} else {
		fmt.Println("restarting daemon...")
	}

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

	return fmt.Errorf("daemon did not come back within 10s — is the service loaded? try 'git-tend install'")
}

func readDaemonPid(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("pid file missing at %s", path)
		}
		return 0, fmt.Errorf("reading pid file %s: %w", path, err)
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid pid file at %s", path)
	}
	return pid, nil
}
