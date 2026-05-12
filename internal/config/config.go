package config

import (
	"fmt"
	"os"
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
