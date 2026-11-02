package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/sdougbrown/cli-contract"

	"github.com/sdougbrown/git-tend/internal/config"
	"github.com/sdougbrown/git-tend/internal/daemon"
	"github.com/sdougbrown/git-tend/internal/paths"
)

var daemonCmd = &clicontract.Command{
	Name:  "daemon",
	Short: "Run the sync daemon (foreground)",
	Long:  `Start the git-tend daemon in the foreground. Used by launchd/systemd.`,
	Handle: func(ctx context.Context, inv clicontract.Invocation) (any, error) {
		// The daemon owns its lifecycle signals (SIGINT/SIGTERM shut down
		// between ticks); Run's context would cancel in-flight syncs.
		return nil, runDaemon()
	},
}

func runDaemon() error {
	configDir := paths.ConfigDir()
	configPath := filepath.Join(configDir, "config.toml")
	if err := config.WriteDefaultConfig(configPath, nil); err != nil {
		return fmt.Errorf("seeding default config: %w", err)
	}
	userCfg, err := config.ParseUserConfig(configPath)
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
	d := daemon.New(userCfg, configPath, stateDir, logger)
	return d.Run(context.Background())
}
