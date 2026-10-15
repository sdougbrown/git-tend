package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "git-tend.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// entryFrom parses doc as the body of a [[repo]] entry for /tmp/repo;
// sub-table headers in doc must be spelled [repo.<name>].
func entryFrom(t *testing.T, doc string) *RepoEntry {
	t.Helper()
	cfg, err := ParseUserConfig(writeConfig(t, "[[repo]]\npath = \"/tmp/repo\"\n"+doc))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return &cfg.Repos[0]
}

func TestParseInProgressWindow(t *testing.T) {
	tests := []struct {
		name    string
		window  string
		wantErr bool
	}{
		{name: "valid duration", window: "5m"},
		{name: "zero duration", window: "0s"},
		{name: "empty is accepted", window: ""},
		{name: "invalid duration", window: "bogus", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			b.WriteString("mode = \"read-only\"\n")
			if tt.window != "" {
				b.WriteString("[commit]\n")
				b.WriteString("in_progress_window = \"" + tt.window + "\"\n")
			}
			cfg, err := Resolve(writeConfig(t, b.String()), nil, "/tmp/repo")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error for invalid in_progress_window")
				}
				if !strings.Contains(err.Error(), "in_progress_window") {
					t.Errorf("error should mention in_progress_window, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Commit.InProgressWindow != tt.window {
				t.Errorf("InProgressWindow = %q, want %q", cfg.Commit.InProgressWindow, tt.window)
			}
		})
	}
}

func TestCommitStrategy(t *testing.T) {
	tests := []struct {
		name        string
		strategy    string
		modelCmd    string
		want        string
		wantErr     bool
		errContains string
	}{
		{name: "empty defaults to deterministic", strategy: "", want: "deterministic"},
		{name: "explicit deterministic kept", strategy: "deterministic", want: "deterministic"},
		{name: "model with model_cmd kept", strategy: "model", modelCmd: "llm -m x", want: "model"},
		{name: "model without model_cmd becomes deterministic", strategy: "model", want: "deterministic"},
		{name: "agent with model_cmd kept", strategy: "agent", modelCmd: "pi -p --no-session --no-tools", want: "agent"},
		{name: "agent without model_cmd becomes deterministic", strategy: "agent", want: "deterministic"},
		{name: "unknown value rejected", strategy: "bogus", wantErr: true, errContains: "strategy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			b.WriteString("mode = \"read-only\"\n")
			b.WriteString("[commit]\n")
			if tt.strategy != "" {
				b.WriteString("strategy = \"" + tt.strategy + "\"\n")
			}
			// model_cmd is a user-only key, so it comes from an entry.
			var entry *RepoEntry
			if tt.modelCmd != "" {
				entry = entryFrom(t, "[repo.commit]\nmodel_cmd = \""+tt.modelCmd+"\"\n")
			}
			cfg, err := Resolve(writeConfig(t, b.String()), entry, "/tmp/repo")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error for invalid strategy")
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error should mention strategy, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Commit.Strategy != tt.want {
				t.Errorf("Strategy = %q, want %q", cfg.Commit.Strategy, tt.want)
			}
		})
	}
}

func TestModelPromptParses(t *testing.T) {
	cfg, err := Resolve(writeConfig(t, "mode = \"read-only\"\n[commit]\nmodel_prompt = \"Summarize stdin.\"\n"), nil, "/tmp/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Commit.ModelPrompt != "Summarize stdin." {
		t.Errorf("ModelPrompt = %q, want %q", cfg.Commit.ModelPrompt, "Summarize stdin.")
	}
}

func TestParseStrategyDeterministicBackwardCompat(t *testing.T) {
	// Existing configs that already spell out the value must keep parsing.
	gittend := writeConfig(t, "mode = \"read-only\"\n[commit]\nstrategy = \"deterministic\"\n")
	entry := entryFrom(t, "[repo.commit]\nmodel_cmd = \"llm -m x 'write a commit message'\"\n")
	cfg, err := Resolve(gittend, entry, "/tmp/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Commit.Strategy != "deterministic" {
		t.Errorf("Strategy = %q, want deterministic", cfg.Commit.Strategy)
	}
}

func TestParseUserConfigRepoEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	data := `
[[repo]]
path = "~/Code/one"
mode = "read-write"
interval = "5m"

[repo.commit]
model_cmd = "run-it"

[[repo]]
path = "$HOME/Code/two"
sync_branch = "dev"
`
	cfg, err := ParseUserConfig(writeConfig(t, data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Repos) != 2 {
		t.Fatalf("expected 2 repo entries, got %d", len(cfg.Repos))
	}
	if cfg.Repos[0].Path != filepath.Join(os.Getenv("HOME"), "Code", "one") {
		t.Errorf("Path[0] = %q, want tilde-expanded", cfg.Repos[0].Path)
	}
	resolved, err := Resolve("", &cfg.Repos[0], cfg.Repos[0].Path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Mode != "read-write" || resolved.Interval != "5m" {
		t.Errorf("unexpected resolved entry[0]: %+v", resolved)
	}
	if resolved.Commit.ModelCmd != "run-it" {
		t.Errorf("ModelCmd = %q, want run-it", resolved.Commit.ModelCmd)
	}
	if cfg.Repos[1].Path != filepath.Join(os.Getenv("HOME"), "Code", "two") {
		t.Errorf("Path[1] = %q, want $HOME-expanded", cfg.Repos[1].Path)
	}
	resolved, err = Resolve("", &cfg.Repos[1], cfg.Repos[1].Path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.SyncBranch != "dev" {
		t.Errorf("SyncBranch = %q, want dev", resolved.SyncBranch)
	}
}

func TestParseUserConfigRepoEntryMissingPath(t *testing.T) {
	_, err := ParseUserConfig(writeConfig(t, "[[repo]]\nmode = \"read-only\"\n"))
	if err == nil || !strings.Contains(err.Error(), "missing path") {
		t.Errorf("expected missing-path error, got: %v", err)
	}
}

func TestParseUserConfigRepoEntryDuplicatesMerge(t *testing.T) {
	data := `
[[repo]]
path = "/tmp/repo-a"
mode = "read-write"
interval = "2m"

[[repo]]
path = "/tmp/repo-a/"
sync_branch = "dev"
mode = "read-only"
`
	cfg, err := ParseUserConfig(writeConfig(t, data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Repos) != 1 {
		t.Fatalf("expected duplicate entries to collapse, got %d", len(cfg.Repos))
	}
	// Later entry wins per key; keys only it sets are kept.
	resolved, err := Resolve("", &cfg.Repos[0], "/tmp/repo-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.Mode != "read-only" {
		t.Errorf("Mode = %q, want read-only (later entry)", resolved.Mode)
	}
	if resolved.Interval != "2m" {
		t.Errorf("Interval = %q, want 2m (earlier entry kept)", resolved.Interval)
	}
	if resolved.SyncBranch != "dev" {
		t.Errorf("SyncBranch = %q, want dev", resolved.SyncBranch)
	}
}

func TestResolveMergePrecedence(t *testing.T) {
	gittend := writeConfig(t, "mode = \"read-write\"\ninterval = \"2m\"\nsync_branch = \"main\"\n")
	entry := entryFrom(t, "interval = \"7m\"\n[repo.commit]\nemoji = \"🚀\"\n")

	cfg, err := Resolve(gittend, entry, "/tmp/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Mode != "read-write" {
		t.Errorf("Mode = %q, want read-write (unset at user level)", cfg.Mode)
	}
	if cfg.Interval != "7m" {
		t.Errorf("Interval = %q, want 7m (user level wins)", cfg.Interval)
	}
	if cfg.SyncBranch != "main" {
		t.Errorf("SyncBranch = %q, want main (unset at user level)", cfg.SyncBranch)
	}
	if cfg.Commit.Emoji != "🚀" {
		t.Errorf("Emoji = %q, want 🚀 (user level wins)", cfg.Commit.Emoji)
	}
	// Defaults apply to the merged result, not per layer.
	if cfg.Commit.Strategy != "deterministic" {
		t.Errorf("Strategy = %q, want default deterministic", cfg.Commit.Strategy)
	}
	if cfg.Debounce != "" {
		t.Errorf("Debounce = %q, want empty", cfg.Debounce)
	}
}

func TestResolveDefaultsApplyPostMerge(t *testing.T) {
	// An unset user-level mode must not clobber the .gittend mode with the
	// built-in default.
	gittend := writeConfig(t, "mode = \"read-write\"\n")
	entry := entryFrom(t, "sync_branch = \"dev\"\n")

	cfg, err := Resolve(gittend, entry, "/tmp/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Mode != "read-write" {
		t.Errorf("Mode = %q, want read-write", cfg.Mode)
	}
	if cfg.SyncBranch != "dev" {
		t.Errorf("SyncBranch = %q, want dev", cfg.SyncBranch)
	}
}

func TestResolveEntryOnlyAppliesDefaults(t *testing.T) {
	// A [[repo]] entry opts a repo in without any .gittend file.
	cfg, err := Resolve("", entryFrom(t, "mode = \"read-write\"\n"), "/tmp/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Mode != "read-write" || cfg.SyncBranch != "main" || cfg.Commit.Strategy != "deterministic" {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestResolveGittendModelCmdIgnoredWithWarning(t *testing.T) {
	// go-toml matches struct fields case-insensitively, so every spelling of
	// the key (and of its table) must still be caught by the strip.
	tests := []struct {
		name    string
		gittend string
	}{
		{name: "plain", gittend: "[commit]\nstrategy = \"model\"\nmodel_cmd = \"evil\"\n"},
		{name: "uppercase key", gittend: "[commit]\nstrategy = \"model\"\nMODEL_CMD = \"evil\"\n"},
		{name: "uppercase table", gittend: "[COMMIT]\nstrategy = \"model\"\nmodel_cmd = \"evil\"\n"},
		{name: "inline table", gittend: "commit = { strategy = \"model\", Model_Cmd = \"evil\" }\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var warnings []string
			origWarn := warnf
			warnf = func(format string, args ...interface{}) {
				warnings = append(warnings, fmt.Sprintf(format, args...))
			}
			defer func() { warnf = origWarn }()

			cfg, err := Resolve(writeConfig(t, tt.gittend), nil, "/tmp/repo")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Commit.ModelCmd != "" {
				t.Errorf("ModelCmd = %q, want empty (ignored)", cfg.Commit.ModelCmd)
			}
			// With no user-level model_cmd, the strategy degrades to deterministic.
			if cfg.Commit.Strategy != "deterministic" {
				t.Errorf("Strategy = %q, want deterministic", cfg.Commit.Strategy)
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0], "/tmp/repo") || !strings.Contains(warnings[0], "commit.model_cmd") {
				t.Errorf("expected warning naming repo and key, got: %v", warnings)
			}
		})
	}
}

func TestResolveUserEntryModelCmdHonored(t *testing.T) {
	gittend := writeConfig(t, "[commit]\nstrategy = \"model\"\nmodel_cmd = \"from-gittend\"\n")
	entry := entryFrom(t, "[repo.commit]\nmodel_cmd = \"from-user\"\n")

	var warnings []string
	origWarn := warnf
	warnf = func(format string, args ...interface{}) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}
	defer func() { warnf = origWarn }()

	cfg, err := Resolve(gittend, entry, "/tmp/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Commit.ModelCmd != "from-user" {
		t.Errorf("ModelCmd = %q, want from-user (user level wins)", cfg.Commit.ModelCmd)
	}
	if cfg.Commit.Strategy != "model" {
		t.Errorf("Strategy = %q, want model", cfg.Commit.Strategy)
	}
	if len(warnings) != 1 {
		t.Errorf("expected one warning for the .gittend model_cmd, got: %v", warnings)
	}
}

func TestResolveInvalidGittendValue(t *testing.T) {
	gittend := writeConfig(t, "mode = \"bogus\"\n")
	if _, err := Resolve(gittend, nil, "/tmp/repo"); err == nil {
		t.Error("expected error for invalid mode")
	}
}

func TestParseUserConfigRejectsInvalidEntryValue(t *testing.T) {
	// A bad value in an entry fails the user-config parse, not the later
	// per-repo resolve.
	_, err := ParseUserConfig(writeConfig(t, "[[repo]]\npath = \"/tmp/repo\"\nmode = \"bogus\"\n"))
	if err == nil || !strings.Contains(err.Error(), "/tmp/repo") || !strings.Contains(err.Error(), "invalid mode") {
		t.Errorf("expected entry-path error for invalid mode, got: %v", err)
	}
	_, err = ParseUserConfig(writeConfig(t, "[[repo]]\npath = \"/tmp/repo\"\ndebounce = \"bogus\"\n"))
	if err == nil || !strings.Contains(err.Error(), "/tmp/repo") || !strings.Contains(err.Error(), "debounce") {
		t.Errorf("expected entry-path error for invalid debounce, got: %v", err)
	}
}

func TestResolveUserEntryNestedMergeKeepsGittendKeys(t *testing.T) {
	// An entry's [repo.commit] table must not wipe sibling keys the .gittend
	// set in the same table.
	gittend := writeConfig(t, "mode = \"read-write\"\ninterval = \"2m\"\n[commit]\nstrategy = \"model\"\n")
	entry := entryFrom(t, "[repo.commit]\nemoji = \"🚀\"\nmodel_cmd = \"user-cmd\"\n")

	cfg, err := Resolve(gittend, entry, "/tmp/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Commit.Strategy != "model" {
		t.Errorf("Strategy = %q, want model (from .gittend)", cfg.Commit.Strategy)
	}
	if cfg.Commit.Emoji != "🚀" {
		t.Errorf("Emoji = %q, want 🚀 (from entry)", cfg.Commit.Emoji)
	}
	if cfg.Commit.ModelCmd != "user-cmd" {
		t.Errorf("ModelCmd = %q, want user-cmd (from entry)", cfg.Commit.ModelCmd)
	}
	if cfg.Interval != "2m" {
		t.Errorf("Interval = %q, want 2m (from .gittend)", cfg.Interval)
	}
}

func TestResolveUserFalseClearsGittendValue(t *testing.T) {
	gittend := writeConfig(t, "[commit]\nno_verify = true\n")
	entry := entryFrom(t, "[repo.commit]\nno_verify = false\n")

	cfg, err := Resolve(gittend, entry, "/tmp/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Commit.NoVerify {
		t.Error("NoVerify = true, want false (user-level false clears .gittend)")
	}
}

func TestResolveUserArrayReplacesGittend(t *testing.T) {
	gittend := writeConfig(t, "[include]\npaths = [\"a\", \"b\"]\n")

	cfg, err := Resolve(gittend, entryFrom(t, "[repo.include]\npaths = [\"c\"]\n"), "/tmp/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Include.Paths) != 1 || cfg.Include.Paths[0] != "c" {
		t.Errorf("Include.Paths = %v, want [c] (replaced wholesale)", cfg.Include.Paths)
	}

	// An entry that leaves the array out falls back to the .gittend value.
	cfg, err = Resolve(gittend, entryFrom(t, "mode = \"read-only\"\n"), "/tmp/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Include.Paths) != 2 || cfg.Include.Paths[0] != "a" || cfg.Include.Paths[1] != "b" {
		t.Errorf("Include.Paths = %v, want [a b] (kept from .gittend)", cfg.Include.Paths)
	}
}
