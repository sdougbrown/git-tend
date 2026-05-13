package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/sdougbrown/git-tend/internal/paths"
)

func init() {
	rootCmd.AddCommand(logsCmd)
	logsCmd.Flags().BoolP("follow", "f", false, "Follow/tail the log")
}

var logsCmd = &cobra.Command{
	Use:   "logs [-f]",
	Short: "Print or tail the daemon log",
	Long:  `Print or tail the git-tend daemon log file.`,
	RunE:  runLogs,
}

func runLogs(cmd *cobra.Command, args []string) error {
	follow, _ := cmd.Flags().GetBool("follow")

	if runtime.GOOS == "linux" {
		var c *exec.Cmd
		if follow {
			c = exec.Command("journalctl", "--user", "-u", "git-tend", "-f")
		} else {
			c = exec.Command("journalctl", "--user", "-u", "git-tend")
		}
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	}

	logPath := filepath.Join(paths.LogDir(), "git-tend.log")
	if follow {
		c := exec.Command("tail", "-f", logPath)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		return fmt.Errorf("reading log: %w", err)
	}
	fmt.Print(string(data))
	return nil
}
