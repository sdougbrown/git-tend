package config

import (
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
			cfg, err := Parse(writeConfig(t, b.String()))
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
			if tt.modelCmd != "" {
				b.WriteString("model_cmd = \"" + tt.modelCmd + "\"\n")
			}
			cfg, err := Parse(writeConfig(t, b.String()))
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
	cfg, err := Parse(writeConfig(t, "mode = \"read-only\"\n[commit]\nmodel_prompt = \"Summarize stdin.\"\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Commit.ModelPrompt != "Summarize stdin." {
		t.Errorf("ModelPrompt = %q, want %q", cfg.Commit.ModelPrompt, "Summarize stdin.")
	}
}

func TestParseStrategyDeterministicBackwardCompat(t *testing.T) {
	// Existing configs that already spell out the value must keep parsing.
	cfg, err := Parse(writeConfig(t, "mode = \"read-only\"\n[commit]\nstrategy = \"deterministic\"\nmodel_cmd = \"llm -m x 'write a commit message'\"\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Commit.Strategy != "deterministic" {
		t.Errorf("Strategy = %q, want deterministic", cfg.Commit.Strategy)
	}
}
