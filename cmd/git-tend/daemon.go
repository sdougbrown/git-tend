package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dbrown/git-tend/internal/config"
	"github.com/dbrown/git-tend/internal/daemon"
	"github.com/dbrown/git-tend/internal/paths"
)

func init() {
	rootCmd.AddCommand(daemonCmd)
}

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run the sync daemon (foreground)",
	Long:  `Start the git-tend daemon in the foreground. Used by launchd/systemd.`,
	RunE:  runDaemon,
}

func runDaemon(cmd *cobra.Command, args []string) error {
	configDir := paths.ConfigDir()
	userCfg, err := config.ParseUserConfig(filepath.Join(configDir, "config.toml"))
	if err != nil {
		return fmt.Errorf("loading user config: %w", err)
	}

	var level slog.Level
	switch userCfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	logger := daemon.NewLogger(level)
	stateDir := paths.StateDir()
	d := daemon.New(userCfg, stateDir, logger)
	return d.Run(context.Background())
}
