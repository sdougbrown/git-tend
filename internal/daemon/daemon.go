package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/sdougbrown/git-tend/internal/config"
	"github.com/sdougbrown/git-tend/internal/scan"
	"github.com/sdougbrown/git-tend/internal/status"
	gitSync "github.com/sdougbrown/git-tend/internal/sync"
)

type offlineState struct {
	consecutiveFailures int
	nextAttemptAt       time.Time
	offlineSince        time.Time
}

type Daemon struct {
	userCfg           *config.UserConfig
	configPath        string
	stateDir          string
	logger            *slog.Logger
	mu                sync.Mutex
	repos             []scan.Repo
	offline           map[string]*offlineState
	stuckLogged       map[string]bool
	pidPath           string
	repoStatus        map[string]status.RepoStatus
	startedAt         time.Time
	interval          time.Duration
	offlineBackoffCap time.Duration
}

func New(userCfg *config.UserConfig, configPath, stateDir string, logger *slog.Logger) *Daemon {
	interval, err := time.ParseDuration(userCfg.Interval)
	if err != nil || interval <= 0 {
		interval = 60 * time.Second
	}

	offlineBackoffCap, err := time.ParseDuration(userCfg.OfflineBackoffCap)
	if err != nil || offlineBackoffCap <= 0 {
		offlineBackoffCap = 30 * time.Minute
	}

	return &Daemon{
		userCfg:           userCfg,
		configPath:        configPath,
		stateDir:          stateDir,
		logger:            logger,
		offline:           make(map[string]*offlineState),
		stuckLogged:       make(map[string]bool),
		repoStatus:        make(map[string]status.RepoStatus),
		pidPath:           filepath.Join(stateDir, "daemon.pid"),
		startedAt:         time.Now(),
		interval:          interval,
		offlineBackoffCap: offlineBackoffCap,
	}
}

func (d *Daemon) Run(ctx context.Context) error {
	if err := os.MkdirAll(d.stateDir, 0755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}

	if err := d.acquirePID(); err != nil {
		return err
	}
	defer os.Remove(d.pidPath)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	d.rescanRoots()
	d.writeStatus()

	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			d.tick(ctx)
		case sig := <-sigCh:
			switch sig {
			case syscall.SIGHUP:
				d.logger.Info("received SIGHUP, reloading config and rescanning")
				if newCfg, err := config.ParseUserConfig(d.configPath); err != nil {
					d.logger.Error("reloading config", "error", err)
				} else {
					d.mu.Lock()
					d.userCfg = newCfg
					d.mu.Unlock()
				}
				d.rescanRoots()
			case syscall.SIGINT, syscall.SIGTERM:
				d.logger.Info("shutting down", "signal", sig.String())
				d.writeStatus()
				return nil
			}
		case <-ctx.Done():
			d.logger.Info("shutting down", "reason", ctx.Err())
			d.writeStatus()
			return nil
		}
	}
}

func (d *Daemon) acquirePID() error {
	data, err := os.ReadFile(d.pidPath)
	if err == nil {
		var existingPID int
		fmt.Sscanf(string(data), "%d", &existingPID)
		if existingPID > 0 {
			if err := syscall.Kill(existingPID, 0); err == nil {
				return fmt.Errorf("daemon already running, pid=%d", existingPID)
			}
		}
	}

	return os.WriteFile(d.pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644)
}

func (d *Daemon) rescanRoots() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.repos = scan.ScanRoots(d.userCfg.Roots, d.userCfg.ScanDepth)
	d.stuckLogged = make(map[string]bool)

	currentRepos := make(map[string]bool, len(d.repos))
	for _, r := range d.repos {
		currentRepos[r.Path] = true
	}
	for path := range d.repoStatus {
		if !currentRepos[path] {
			delete(d.repoStatus, path)
		}
	}

	d.logger.Info("scan complete", "repos", len(d.repos))
}

func (d *Daemon) tick(ctx context.Context) {
	timeout, err := time.ParseDuration(d.userCfg.NetworkTimeout)
	if err != nil || timeout <= 0 {
		timeout = 30 * time.Second
	}

	d.mu.Lock()
	repos := make([]scan.Repo, len(d.repos))
	copy(repos, d.repos)
	d.mu.Unlock()

	d.logger.Debug("tick start", "repos", len(repos))

	now := time.Now()

	for _, repo := range repos {
		stuckPath := filepath.Join(repo.Path, ".gittend.stuck")
		if _, err := os.Stat(stuckPath); err == nil {
			if !d.stuckLogged[repo.Path] {
				d.logger.Info("repo stuck, skipping", "repo", repo.Path)
				d.stuckLogged[repo.Path] = true
			}
			d.mu.Lock()
			rs := d.repoStatus[repo.Path]
			if rs.CurrentState != "stuck" {
				rs.PriorState = rs.CurrentState
				rs.CurrentState = "stuck"
				rs.Mode = repo.Config.Mode
				if rs.StuckSince == "" {
					rs.StuckSince = now.UTC().Format(time.RFC3339)
				}
			}
			d.repoStatus[repo.Path] = rs
			d.mu.Unlock()
			continue
		}

		d.mu.Lock()
		off := d.offline[repo.Path]
		skipOffline := off != nil && !off.nextAttemptAt.IsZero() && off.nextAttemptAt.After(now)
		d.mu.Unlock()

		if skipOffline {
			d.logger.Debug("repo in offline backoff, skipping", "repo", repo.Path, "next_attempt", off.nextAttemptAt)
			continue
		}

		syncCtx, cancel := context.WithTimeout(ctx, timeout)
		result := gitSync.Sync(syncCtx, repo.Path, repo.Config, d.stateDir)
		cancel()

		d.mu.Lock()
		rs := d.repoStatus[repo.Path]
		rs.Mode = repo.Config.Mode

		switch result.State {
		case "ok":
			off := d.offline[repo.Path]
			if off != nil {
				off.consecutiveFailures = 0
				off.nextAttemptAt = time.Time{}
				off.offlineSince = time.Time{}
			}

			rs.PriorState = rs.CurrentState
			rs.CurrentState = "ok"
			rs.LastSyncAt = now.UTC().Format(time.RFC3339)
			rs.LastError = ""
			rs.Ahead = result.Ahead
			rs.Behind = result.Behind
			rs.StuckSince = ""
			rs.SnoozedUntil = ""
			rs.OfflineSince = ""
			rs.ConsecutiveOfflineFailures = 0

		case "offline":
			off := d.offline[repo.Path]
			if off == nil {
				off = &offlineState{}
				d.offline[repo.Path] = off
			}
			off.consecutiveFailures++
			if off.offlineSince.IsZero() {
				off.offlineSince = now
			}

			if off.consecutiveFailures >= 3 {
				shift := off.consecutiveFailures - 2
				if shift > 20 {
					shift = 20
				}
				backoff := d.interval * time.Duration(int64(1)<<shift)
				if backoff <= 0 || backoff > d.offlineBackoffCap {
					backoff = d.offlineBackoffCap
				}
				off.nextAttemptAt = now.Add(backoff)
				d.logger.Debug("offline backoff applied", "repo", repo.Path,
					"failures", off.consecutiveFailures,
					"backoff", backoff.String())
			}

			rs.PriorState = rs.CurrentState
			rs.CurrentState = "offline"
			rs.LastError = result.Error
			rs.OfflineSince = off.offlineSince.UTC().Format(time.RFC3339)
			rs.ConsecutiveOfflineFailures = off.consecutiveFailures

		case "stuck":
			rs.PriorState = rs.CurrentState
			rs.CurrentState = "stuck"
			rs.LastError = result.Error
			if rs.StuckSince == "" {
				rs.StuckSince = now.UTC().Format(time.RFC3339)
			}

		case "skipped":
			d.logger.Debug("repo skipped", "repo", repo.Path, "reason", result.Error)
		}

		d.repoStatus[repo.Path] = rs
		d.mu.Unlock()
	}

	d.writeStatus()
	d.logger.Debug("tick complete")
}

func (d *Daemon) writeStatus() {
	d.mu.Lock()
	defer d.mu.Unlock()

	sf := &status.StatusFile{
		Version:         1,
		DaemonStartedAt: d.startedAt.UTC().Format(time.RFC3339),
		LastTickAt:      time.Now().UTC().Format(time.RFC3339),
		IntervalSeconds: int(d.interval.Seconds()),
		Repos:           d.repoStatus,
	}

	if err := status.Write(filepath.Join(d.stateDir, "status.json"), sf); err != nil {
		d.logger.Error("writing status", "error", err)
	}
}

func NewLogger(level slog.Level) *slog.Logger {
	var handler slog.Handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	if os.Getenv("LAUNCHD_SOCKET") != "" || os.Getenv("JOURNAL_STREAM") != "" {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	}
	return slog.New(handler)
}
