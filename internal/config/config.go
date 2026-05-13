package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Mode       string        `toml:"mode"`
	SyncBranch string        `toml:"sync_branch"`
	Interval   string        `toml:"interval"`
	Debounce   string        `toml:"debounce"`
	Commit     CommitConfig  `toml:"commit"`
	Include    IncludeConfig `toml:"include"`
	Exclude    ExcludeConfig `toml:"exclude"`
	Notify     NotifyConfig  `toml:"notify"`
}

type CommitConfig struct {
	Strategy       string `toml:"strategy"`
	Emoji          string `toml:"emoji"`
	ModelCmd       string `toml:"model_cmd"`
	ModelTimeout   string `toml:"model_timeout"`
	FallbackThresh int    `toml:"model_fallback_threshold"`
	NoVerify       bool   `toml:"no_verify"`
}

type IncludeConfig struct {
	Paths []string `toml:"paths"`
}

type ExcludeConfig struct {
	Paths []string `toml:"paths"`
}

type NotifyConfig struct {
	OnStuck     bool `toml:"on_stuck"`
	OnRecovered bool `toml:"on_recovered"`
}

func ParseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

type UserConfig struct {
	Roots             []string `toml:"roots"`
	Interval          string   `toml:"interval"`
	LogLevel          string   `toml:"log_level"`
	EscalateAfterDays int      `toml:"escalate_after_days"`
	NetworkTimeout    string   `toml:"network_timeout"`
	OfflineBackoffCap string   `toml:"offline_backoff_cap"`
	ScanDepth         int      `toml:"scan_depth"`
}

var defaultRoots = []string{"~/Code"}

// DefaultRoots returns a copy of the built-in default roots used when no
// config file exists and no roots are configured.
func DefaultRoots() []string {
	out := make([]string, len(defaultRoots))
	copy(out, defaultRoots)
	return out
}

var validLogLevels = map[string]bool{
	"debug": true,
	"info":  true,
	"warn":  true,
	"error": true,
}

func ParseUserConfig(path string) (*UserConfig, error) {
	cfg := &UserConfig{
		Roots:             defaultRoots,
		Interval:          "60s",
		LogLevel:          "info",
		EscalateAfterDays: 3,
		NetworkTimeout:    "30s",
		OfflineBackoffCap: "30m",
		ScanDepth:         4,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, fmt.Errorf("reading user config %s: %w", path, err)
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing user config %s: %w", path, err)
	}

	if len(cfg.Roots) == 0 {
		cfg.Roots = defaultRoots
	}

	if cfg.Interval != "" {
		if _, err := time.ParseDuration(cfg.Interval); err != nil {
			return nil, fmt.Errorf("invalid interval %q: %w", cfg.Interval, err)
		}
	}
	if cfg.Interval == "" {
		cfg.Interval = "60s"
	}

	if cfg.LogLevel != "" {
		if !validLogLevels[cfg.LogLevel] {
			return nil, fmt.Errorf("invalid log_level %q: must be debug, info, warn, or error", cfg.LogLevel)
		}
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}

	if cfg.EscalateAfterDays <= 0 {
		cfg.EscalateAfterDays = 3
	}

	if cfg.NetworkTimeout != "" {
		if _, err := time.ParseDuration(cfg.NetworkTimeout); err != nil {
			return nil, fmt.Errorf("invalid network_timeout %q: %w", cfg.NetworkTimeout, err)
		}
	}
	if cfg.NetworkTimeout == "" {
		cfg.NetworkTimeout = "30s"
	}

	if cfg.OfflineBackoffCap != "" {
		if _, err := time.ParseDuration(cfg.OfflineBackoffCap); err != nil {
			return nil, fmt.Errorf("invalid offline_backoff_cap %q: %w", cfg.OfflineBackoffCap, err)
		}
	}
	if cfg.OfflineBackoffCap == "" {
		cfg.OfflineBackoffCap = "30m"
	}

	if cfg.ScanDepth <= 0 {
		cfg.ScanDepth = 4
	}

	return cfg, nil
}

// WriteDefaultConfig writes a commented TOML template to path. If roots is
// empty, the built-in defaults are used. It is a no-op if the file already
// exists; callers should check beforehand if they care to distinguish.
func WriteDefaultConfig(path string, roots []string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking config path %s: %w", path, err)
	}

	if len(roots) == 0 {
		roots = defaultRoots
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	quoted := make([]string, len(roots))
	for i, r := range roots {
		quoted[i] = fmt.Sprintf("%q", r)
	}
	rootsLine := "[" + strings.Join(quoted, ", ") + "]"

	body := `# git-tend daemon configuration
# Docs: https://github.com/sdougbrown/git-tend#configuration

# Directories to scan for opted-in repos. A repo is opted in by placing a
# .gittend file at its root. Tilde-expanded. Adjust to match your layout.
roots = ` + rootsLine + `
# roots = ["~/Code", "~/dev", "~/.dotfiles"]
# roots = ["~/src", "~/work"]

# How often to sync each repo.
interval = "60s"

# Log verbosity: debug, info, warn, error.
log_level = "info"

# How many days a repo can be stuck before 'git-tend greet' highlights it urgently.
escalate_after_days = 3

# Timeout for network operations (fetch, push).
network_timeout = "30s"

# Maximum backoff interval for repos that keep failing to reach the remote.
offline_backoff_cap = "30m"

# How many directory levels deep to search inside each root.
scan_depth = 4
`

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("writing config %s: %w", path, err)
	}
	return nil
}

func Parse(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	if cfg.Mode == "" {
		cfg.Mode = "read-only"
	}
	if cfg.Mode != "read-only" && cfg.Mode != "read-write" {
		return nil, fmt.Errorf("invalid mode %q: must be read-only or read-write", cfg.Mode)
	}

	if cfg.SyncBranch == "" {
		cfg.SyncBranch = "main"
	}

	if cfg.Interval != "" {
		if _, err := time.ParseDuration(cfg.Interval); err != nil {
			return nil, fmt.Errorf("invalid interval %q: %w", cfg.Interval, err)
		}
	}

	if cfg.Debounce != "" {
		if _, err := time.ParseDuration(cfg.Debounce); err != nil {
			return nil, fmt.Errorf("invalid debounce %q: %w", cfg.Debounce, err)
		}
	}

	if cfg.Commit.Emoji == "" {
		cfg.Commit.Emoji = "🐌"
	}

	return &cfg, nil
}
