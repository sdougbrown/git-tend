package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/sdougbrown/cli-contract"

	"github.com/sdougbrown/git-tend/internal/paths"
)

var logsCmd = &clicontract.Command{
	Name:  "logs",
	Short: "Print or tail the daemon log",
	Long:  `Print or tail the git-tend daemon log file.`,
	Flags: []clicontract.Flag{
		{Name: "follow", Kind: clicontract.Bool, Short: "f", Usage: "Follow/tail the log"},
	},
	Handle: func(ctx context.Context, inv clicontract.Invocation) (any, error) {
		return nil, runLogs(inv.Bools["follow"])
	},
}

func runLogs(follow bool) error {
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
