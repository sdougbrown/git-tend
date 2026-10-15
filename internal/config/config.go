package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/sdougbrown/git-tend/internal/paths"
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
	Strategy         string `toml:"strategy"`
	Emoji            string `toml:"emoji"`
	ModelCmd         string `toml:"model_cmd"`
	ModelPrompt      string `toml:"model_prompt"`
	ModelTimeout     string `toml:"model_timeout"`
	FallbackThresh   int    `toml:"model_fallback_threshold"`
	NoVerify         bool   `toml:"no_verify"`
	InProgressWindow string `toml:"in_progress_window"`
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

// RepoEntry is a user-level [[repo]] entry: per-repo settings set centrally
// in the user config for the repo at Path. Keys sit at the same level as
// path, with sub-tables like [repo.commit]. Raw holds the entry's TOML
// table with path removed; Resolve overlays it onto the repo's .gittend
// config, so only keys present in the entry take effect and keys left out
// fall back to the .gittend. Entries built directly (Raw nil) set nothing.
type RepoEntry struct {
	Path string         `toml:"path"`
	Raw  map[string]any `toml:"-"`
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
	// Populated from the raw [[repo]] tables after unmarshalling so entries
	// keep their per-key merge shape (see ParseUserConfig).
	Repos []RepoEntry `toml:"-"`
}

// EntryFor returns the user-level entry for the repo at path, or nil when no
// [[repo]] entry matches.
func (uc *UserConfig) EntryFor(path string) *RepoEntry {
	for i := range uc.Repos {
		if paths.SamePath(uc.Repos[i].Path, path) {
			return &uc.Repos[i]
		}
	}
	return nil
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

	var raw struct {
		Repos []any `toml:"repo"`
	}
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing user config %s: %w", path, err)
	}

	entries := make([]RepoEntry, 0, len(raw.Repos))
	for i, t := range raw.Repos {
		table, ok := t.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("repo entry %d: not a table", i+1)
		}
		p, _ := table["path"].(string)
		if p == "" {
			return nil, fmt.Errorf("repo entry %d: path must be a non-empty string", i+1)
		}
		delete(table, "path")
		entries = append(entries, RepoEntry{Path: p, Raw: table})
	}
	entries, err = normalizeRepoEntries(entries)
	if err != nil {
		return nil, err
	}
	// Validate each entry now so a bad value fails the user-config parse —
	// the daemon keeps its previous config on SIGHUP — instead of silently
	// dropping the repo at scan time.
	for i := range entries {
		var probe Config
		if err := overlayEntry(&probe, &entries[i], entries[i].Path); err != nil {
			return nil, err
		}
		if err := finalizeConfig(&probe); err != nil {
			return nil, fmt.Errorf("invalid user repo entry for %s: %w", entries[i].Path, err)
		}
	}
	cfg.Repos = entries

	return cfg, nil
}

// normalizeRepoEntries expands entry paths and collapses duplicate entries
// for the same repo, merging later entries over earlier ones per key so the
// effective entry for a path is deterministic regardless of ordering.
func normalizeRepoEntries(entries []RepoEntry) ([]RepoEntry, error) {
	normalized := make([]RepoEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Path == "" {
			return nil, fmt.Errorf("repo entry missing path")
		}
		entry.Path = paths.ExpandPath(entry.Path)
		merged := false
		for i := range normalized {
			if paths.SamePath(normalized[i].Path, entry.Path) {
				mergeRaw(normalized[i].Raw, entry.Raw)
				merged = true
				break
			}
		}
		if !merged {
			normalized = append(normalized, entry)
		}
	}
	return normalized, nil
}

// mergeRaw overlays src onto dst: keys present in src win, nested tables are
// merged per key, and other values (including arrays) replace dst's.
func mergeRaw(dst, src map[string]any) {
	for k, v := range src {
		if dm, ok := dst[k].(map[string]any); ok {
			if sm, ok := v.(map[string]any); ok {
				mergeRaw(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
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

# Per-repo settings can be set centrally here instead of (or in addition to)
# each repo's .gittend. An entry opts the repo in even if it has no .gittend
# file, and works even if the repo lives outside the configured roots.
# A key set in the entry always wins — even when its value is false or 0;
# an empty string means the built-in default for keys that have one. Keys
# left out of the entry fall back to the repo's .gittend.
#
# Command-executing keys (like commit.model_cmd below) are honored ONLY from
# a [[repo]] entry here — the repo's .gittend cannot set them.
#
# [[repo]]
# path = "~/Code/my-repo"
# mode = "read-write"
# sync_branch = "main"
#
# [repo.commit]
# model_cmd = "pi -p --no-session --no-tools"
`

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("writing config %s: %w", path, err)
	}
	return nil
}

// warnf is a variable so tests can capture warnings.
var warnf = func(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "WARN: "+format+"\n", args...)
}

// userOnlyKeys lists per-repo keys honored only from a user-config [[repo]]
// entry: a .gittend value for any of these is ignored with a warning, since
// the keys execute commands with the daemon's privileges and a repo writer
// must not control them. field addresses the key's decoded struct field so
// the check cannot be bypassed by alternate key spellings (go-toml matches
// struct fields case-insensitively, so [COMMIT] and MODEL_CMD decode into
// the same field a plain model_cmd would); to add another key, add one line
// here.
var userOnlyKeys = []struct {
	key   string
	field func(*Config) *string
}{
	{key: "commit.model_cmd", field: func(c *Config) *string { return &c.Commit.ModelCmd }},
}

// clearUserOnlyKeys zeroes user-only keys decoded from a .gittend, warning
// about each one that was set.
func clearUserOnlyKeys(cfg *Config, repoPath string) {
	for _, k := range userOnlyKeys {
		p := k.field(cfg)
		if *p == "" {
			continue
		}
		warnf("%s: ignoring %s from .gittend; set it in a [[repo]] entry of the user config instead", repoPath, k.key)
		*p = ""
	}
}

// Resolve returns the effective per-repo config for the repo at repoPath:
// the user-level [[repo]] entry (if any) overlaid per key on the repo's
// .gittend (if any), with defaults and validation applied once to the merged
// result. gittendPath may be empty when the repo has no .gittend file.
func Resolve(gittendPath string, entry *RepoEntry, repoPath string) (*Config, error) {
	var cfg Config

	if gittendPath != "" {
		data, err := os.ReadFile(gittendPath)
		if err != nil {
			return nil, fmt.Errorf("reading config file %s: %w", gittendPath, err)
		}
		cfg, err = parseRepoConfig(data, gittendPath, repoPath)
		if err != nil {
			return nil, err
		}
	}

	if entry != nil {
		if err := overlayEntry(&cfg, entry, repoPath); err != nil {
			return nil, err
		}
	}

	if err := finalizeConfig(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// overlayEntry applies an entry's raw table to cfg: keys present in the
// entry win, keys left out keep their current value.
func overlayEntry(cfg *Config, entry *RepoEntry, repoPath string) error {
	if len(entry.Raw) == 0 {
		return nil
	}
	data, err := toml.Marshal(entry.Raw)
	if err != nil {
		return fmt.Errorf("encoding user repo entry for %s: %w", repoPath, err)
	}
	// Unmarshalling onto the populated struct leaves keys absent from the
	// entry untouched, including inside sub-tables like [commit].
	if err := toml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("invalid user repo entry for %s: %w", repoPath, err)
	}
	return nil
}

// ErrNotOptedIn reports that a repo is opted in by neither a .gittend file
// nor a matching [[repo]] entry in the user config.
var ErrNotOptedIn = errors.New("no .gittend config and no matching [[repo]] entry in the user config")

// ResolveRepo resolves the effective per-repo config for the repo at
// repoPath from the two opt-in sources: the repo's .gittend file (if any)
// and the matching user-level [[repo]] entry (if any). It fails with an
// error wrapping ErrNotOptedIn when neither source is present.
func ResolveRepo(repoPath string, userCfg *UserConfig) (*Config, error) {
	gittend := filepath.Join(repoPath, ".gittend")
	var gittendPath string
	if _, err := os.Stat(gittend); err == nil {
		gittendPath = gittend
	}

	entry := userCfg.EntryFor(repoPath)
	if gittendPath == "" && entry == nil {
		return nil, fmt.Errorf("%s: %w", repoPath, ErrNotOptedIn)
	}
	return Resolve(gittendPath, entry, repoPath)
}

// parseRepoConfig decodes a .gittend, zeroing user-only keys so the decoded
// config can never carry one.
func parseRepoConfig(data []byte, gittendPath, repoPath string) (Config, error) {
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config %s: %w", gittendPath, err)
	}
	clearUserOnlyKeys(&cfg, repoPath)
	return cfg, nil
}

// finalizeConfig applies built-in defaults and validation to a config.
func finalizeConfig(cfg *Config) error {
	if cfg.Mode == "" {
		cfg.Mode = "read-only"
	}
	if cfg.Mode != "read-only" && cfg.Mode != "read-write" {
		return fmt.Errorf("invalid mode %q: must be read-only or read-write", cfg.Mode)
	}

	if cfg.SyncBranch == "" {
		cfg.SyncBranch = "main"
	}

	if cfg.Interval != "" {
		if _, err := time.ParseDuration(cfg.Interval); err != nil {
			return fmt.Errorf("invalid interval %q: %w", cfg.Interval, err)
		}
	}

	if cfg.Debounce != "" {
		if _, err := time.ParseDuration(cfg.Debounce); err != nil {
			return fmt.Errorf("invalid debounce %q: %w", cfg.Debounce, err)
		}
	}

	if cfg.Commit.InProgressWindow != "" {
		if _, err := time.ParseDuration(cfg.Commit.InProgressWindow); err != nil {
			return fmt.Errorf("invalid commit in_progress_window %q: %w", cfg.Commit.InProgressWindow, err)
		}
	}

	if cfg.Commit.Emoji == "" {
		cfg.Commit.Emoji = "🐌"
	}

	if cfg.Commit.Strategy == "" {
		cfg.Commit.Strategy = "deterministic"
	}
	if cfg.Commit.Strategy != "deterministic" && cfg.Commit.Strategy != "model" && cfg.Commit.Strategy != "agent" {
		return fmt.Errorf("invalid commit strategy %q: must be deterministic, model, or agent", cfg.Commit.Strategy)
	}
	// "model" and "agent" with no model command have nothing to consult;
	// behave exactly like the deterministic strategy.
	if (cfg.Commit.Strategy == "model" || cfg.Commit.Strategy == "agent") && cfg.Commit.ModelCmd == "" {
		cfg.Commit.Strategy = "deterministic"
	}

	return nil
}
