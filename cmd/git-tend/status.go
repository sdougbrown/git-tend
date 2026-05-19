package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sdougbrown/git-tend/internal/paths"
	"github.com/sdougbrown/git-tend/internal/status"
)

func init() {
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Print last-sync status for managed repos",
	RunE:  runStatus,
}

func runStatus(cmd *cobra.Command, args []string) error {
	stateDir := paths.StateDir()
	sf := status.Read(filepath.Join(stateDir, "status.json"))

	if sf == nil {
		fmt.Println("Daemon not running. Run 'git-tend install' or 'git-tend daemon'.")
		return nil
	}
	if len(sf.Repos) == 0 {
		fmt.Println("No managed repos found yet. If you just started the daemon, give it a minute to scan. Otherwise, make sure repos have a .gittend file inside them.")
		return nil
	}

	fmt.Printf("%-40s %-4s %-8s %-12s %-10s %-10s\n", "REPO", "MODE", "STATE", "LAST SYNC", "AHEAD", "BEHIND")
	fmt.Println(strings.Repeat("-", 90))

	for path, rs := range sf.Repos {
		mode := "ro"
		if rs.Mode == "read-write" {
			mode = "rw"
		}

		state := rs.CurrentState
		if state == "" {
			state = "ok"
		}

		lastSync := "never"
		if rs.LastSyncAt != "" {
			t, err := time.Parse(time.RFC3339, rs.LastSyncAt)
			if err == nil {
				ago := time.Since(t).Round(time.Second)
				if ago < time.Minute {
					lastSync = fmt.Sprintf("%ds ago", int(ago.Seconds()))
				} else if ago < time.Hour {
					lastSync = fmt.Sprintf("%dm ago", int(ago.Minutes()))
				} else if ago < 24*time.Hour {
					lastSync = fmt.Sprintf("%dh ago", int(ago.Hours()))
				} else {
					lastSync = fmt.Sprintf("%dd ago", int(ago.Hours()/24))
				}
			}
		}

		ahead := fmt.Sprintf("%d", rs.Ahead)
		behind := fmt.Sprintf("%d", rs.Behind)

		fmt.Printf("%-40s %-4s %-8s %-12s %-10s %-10s\n", path, mode, state, lastSync, ahead, behind)
	}

	return nil
}
