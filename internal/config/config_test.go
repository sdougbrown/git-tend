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
