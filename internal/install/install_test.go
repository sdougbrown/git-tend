package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteLaunchdPlist(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	path, err := WriteLaunchdPlist()
	if err != nil {
		t.Fatal(err)
	}

	if path == "" {
		t.Error("expected non-empty path")
	}

	expectedDir := filepath.Join(tmpHome, "Library", "LaunchAgents")
	if !strings.HasPrefix(path, expectedDir) {
		t.Errorf("path %q not under %q", path, expectedDir)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{LaunchdLabel, "KeepAlive", "RunAtLoad"} {
		if !strings.Contains(content, want) {
			t.Errorf("plist missing %q", want)
		}
	}
}

func TestWriteSystemdUnit(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	path, err := WriteSystemdUnit()
	if err != nil {
		t.Fatal(err)
	}

	if path == "" {
		t.Error("expected non-empty path")
	}

	if !strings.Contains(path, "git-tend.service") {
		t.Errorf("path %q should contain 'git-tend.service'", path)
	}

	expectedDir := filepath.Join(tmpHome, ".config", "systemd", "user")
	if !strings.HasPrefix(path, expectedDir) {
		t.Errorf("path %q not under %q", path, expectedDir)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"Restart=on-failure", "WantedBy=default.target"} {
		if !strings.Contains(content, want) {
			t.Errorf("unit file missing %q", want)
		}
	}
}

func TestInstallShellPromptForce(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	rcPath := filepath.Join(tmpHome, ".zshrc")
	original := "alias ll='ls -la'\n# >>> git-tend prompt >>>\nOLD CONTENT\n# <<< git-tend prompt <<<\n# other stuff\n"
	if err := os.WriteFile(rcPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	path, err := InstallShellPrompt("zsh", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if path != rcPath {
		t.Errorf("expected %q, got %q", rcPath, path)
	}

	data, _ := os.ReadFile(rcPath)
	content := string(data)

	if strings.Contains(content, "OLD CONTENT") {
		t.Error("force should have removed old fence content")
	}
	if !strings.Contains(content, "PROMPT='$(git-tend prompt") {
		t.Error("missing prompt snippet after force install")
	}
	if !strings.Contains(content, "alias ll='ls -la'") {
		t.Error("lost content outside fences")
	}
}

func TestInstallShellPromptIdempotent(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	rcPath := filepath.Join(tmpHome, ".zshrc")
	original := "alias ll='ls -la'\n# >>> git-tend prompt >>>\nOLD CONTENT\n# <<< git-tend prompt <<<\n# other stuff\n"
	if err := os.WriteFile(rcPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	path, err := InstallShellPrompt("zsh", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Errorf("expected empty path (no-op), got %q", path)
	}
}

func TestUninstallShellPrompt(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	rcPath := filepath.Join(tmpHome, ".zshrc")
	original := "alias ll='ls -la'\n"
	if err := os.WriteFile(rcPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := InstallShellPrompt("zsh", false, false)
	if err != nil {
		t.Fatal(err)
	}

	uninstallPath, err := UninstallShellPrompt("zsh")
	if err != nil {
		t.Fatal(err)
	}
	if uninstallPath != rcPath {
		t.Errorf("expected %q, got %q", rcPath, uninstallPath)
	}

	data, _ := os.ReadFile(rcPath)
	content := string(data)

	if strings.Contains(content, fenceStart) || strings.Contains(content, fenceEnd) {
		t.Error("fences should be removed after uninstall")
	}
	if !strings.Contains(content, "alias ll='ls -la'") {
		t.Error("original content should be preserved")
	}
}

func TestUninstallShellPromptNoFences(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	rcPath := filepath.Join(tmpHome, ".zshrc")
	original := "alias ll='ls -la'\n"
	if err := os.WriteFile(rcPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	path, err := UninstallShellPrompt("zsh")
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Errorf("expected empty path (no-op), got %q", path)
	}
}

func TestInstallShellPromptDryRun(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	rcPath := filepath.Join(tmpHome, ".zshrc")
	original := "alias ll='ls -la'\n"
	if err := os.WriteFile(rcPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	path, err := InstallShellPrompt("zsh", false, true)
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Errorf("dry run should return empty path, got %q", path)
	}

	data, _ := os.ReadFile(rcPath)
	if string(data) != original {
		t.Errorf("dry run should not modify file\ngot: %q\nwant: %q", string(data), original)
	}
}

func TestBashSnippetUsesPS1(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	rcPath := filepath.Join(tmpHome, ".bashrc")
	if err := os.WriteFile(rcPath, []byte("alias ll='ls -la'\n"), 0644); err != nil {
		t.Fatal(err)
	}

	path, err := InstallShellPrompt("bash", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if path != rcPath {
		t.Errorf("expected %q, got %q", rcPath, path)
	}

	data, _ := os.ReadFile(rcPath)
	content := string(data)
	if !strings.Contains(content, "PS1='$(git-tend prompt") {
		t.Error("bash snippet missing PS1")
	}
}

func TestZshSnippetUsesPROMPT(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	rcPath := filepath.Join(tmpHome, ".zshrc")
	if err := os.WriteFile(rcPath, []byte("alias ll='ls -la'\n"), 0644); err != nil {
		t.Fatal(err)
	}

	path, err := InstallShellPrompt("zsh", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if path != rcPath {
		t.Errorf("expected %q, got %q", rcPath, path)
	}

	data, _ := os.ReadFile(rcPath)
	content := string(data)
	if !strings.Contains(content, "PROMPT='$(git-tend prompt") {
		t.Error("zsh snippet missing PROMPT")
	}
}

func TestFishSnippetUsesRawCommand(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	fishDir := filepath.Join(tmpHome, ".config", "fish")
	if err := os.MkdirAll(fishDir, 0755); err != nil {
		t.Fatal(err)
	}
	rcPath := filepath.Join(fishDir, "config.fish")

	path, err := InstallShellPrompt("fish", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if path != rcPath {
		t.Errorf("expected %q, got %q", rcPath, path)
	}

	data, _ := os.ReadFile(rcPath)
	content := string(data)
	if !strings.Contains(content, "git-tend prompt 2>/dev/null") {
		t.Error("fish snippet missing raw command")
	}
	if strings.Contains(content, "PS1") || strings.Contains(content, "PROMPT") {
		t.Error("fish snippet should not contain PS1 or PROMPT")
	}
}

func TestWriteLaunchdPlistTwice(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	path1, err := WriteLaunchdPlist()
	if err != nil {
		t.Fatal(err)
	}

	path2, err := WriteLaunchdPlist()
	if err != nil {
		t.Fatal(err)
	}

	if path1 != path2 {
		t.Errorf("expected same path both times, got %q and %q", path1, path2)
	}
}

func TestRemoveServiceFilesIdempotent(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	_, err := WriteLaunchdPlist()
	if err != nil {
		t.Fatal(err)
	}

	if err := RemoveServiceFiles(); err != nil {
		t.Fatal(err)
	}

	if err := RemoveServiceFiles(); err != nil {
		t.Fatal(err)
	}
}
