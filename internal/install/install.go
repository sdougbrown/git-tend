package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func IsMacOS() bool { return runtime.GOOS == "darwin" }
func IsLinux() bool  { return runtime.GOOS == "linux" }

const launchdPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.dbrown.gittend</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>daemon</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin</string>
    </dict>
</dict>
</plist>
`

const systemdUnitTemplate = `[Unit]
Description=git-tend background repo auto-sync daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%%h/.local/bin/git-tend daemon
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`

func launchdPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", "com.dbrown.gittend.plist"), nil
}

func systemdUnitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user", "git-tend.service"), nil
}

func WriteLaunchdPlist() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	binPath := filepath.Join(home, ".local", "bin", "git-tend")
	logPath := filepath.Join(home, "Library", "Logs", "git-tend", "git-tend.log")

	plistPath, err := launchdPlistPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0755); err != nil {
		return "", fmt.Errorf("mkdir LaunchAgents: %w", err)
	}

	content := fmt.Sprintf(launchdPlistTemplate, binPath, logPath, logPath)
	if err := os.WriteFile(plistPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write plist: %w", err)
	}
	return plistPath, nil
}

func WriteSystemdUnit() (string, error) {
	unitPath, err := systemdUnitPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0755); err != nil {
		return "", fmt.Errorf("mkdir systemd user dir: %w", err)
	}

	content := systemdUnitTemplate
	if err := os.WriteFile(unitPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write unit: %w", err)
	}
	return unitPath, nil
}

func LoadService() error {
	if IsMacOS() {
		plistPath, err := launchdPlistPath()
		if err != nil {
			return err
		}
		return exec.Command("launchctl", "load", plistPath).Run()
	}
	if IsLinux() {
		return exec.Command("systemctl", "--user", "enable", "--now", "git-tend").Run()
	}
	return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
}

func UnloadService() error {
	if IsMacOS() {
		plistPath, err := launchdPlistPath()
		if err != nil {
			return err
		}
		return exec.Command("launchctl", "unload", plistPath).Run()
	}
	if IsLinux() {
		return exec.Command("systemctl", "--user", "disable", "--now", "git-tend").Run()
	}
	return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
}

func RemoveServiceFiles() error {
	if IsMacOS() {
		plistPath, err := launchdPlistPath()
		if err != nil {
			return err
		}
		err = os.Remove(plistPath)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if IsLinux() {
		unitPath, err := systemdUnitPath()
		if err != nil {
			return err
		}
		err = os.Remove(unitPath)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
}

const (
	fenceStart = "# >>> git-tend prompt >>>"
	fenceEnd   = "# <<< git-tend prompt <<<"
)

func zshSnippet() string {
	return fenceStart + "\nPROMPT='$(git-tend prompt 2>/dev/null) '$PROMPT\n" + fenceEnd + "\n"
}

func bashSnippet() string {
	return fenceStart + "\nPS1='$(git-tend prompt 2>/dev/null) '$PS1\n" + fenceEnd + "\n"
}

func fishSnippet() string {
	return fenceStart + "\ngit-tend prompt 2>/dev/null\n" + fenceEnd + "\n"
}

func detectShell(explicit string) string {
	if explicit != "" {
		return explicit
	}
	shellEnv := os.Getenv("SHELL")
	if strings.Contains(shellEnv, "zsh") {
		return "zsh"
	}
	if strings.Contains(shellEnv, "bash") {
		return "bash"
	}
	if strings.Contains(shellEnv, "fish") {
		return "fish"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	checks := []struct {
		shell string
		path  string
	}{
		{"zsh", ".zshrc"},
		{"bash", ".bashrc"},
		{"bash", ".bash_profile"},
		{"fish", ".config/fish/config.fish"},
	}
	for _, c := range checks {
		p := filepath.Join(home, c.path)
		if _, err := os.Stat(p); err == nil {
			return c.shell
		}
	}
	return ""
}

func rcFilePath(shell string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch shell {
	case "zsh":
		return filepath.Join(home, ".zshrc"), nil
	case "bash":
		bashrc := filepath.Join(home, ".bashrc")
		if _, err := os.Stat(bashrc); err == nil {
			return bashrc, nil
		}
		return filepath.Join(home, ".bash_profile"), nil
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish"), nil
	default:
		return "", fmt.Errorf("unsupported shell: %s", shell)
	}
}

func snippetForShell(shell string) (string, error) {
	switch shell {
	case "zsh":
		return zshSnippet(), nil
	case "bash":
		return bashSnippet(), nil
	case "fish":
		return fishSnippet(), nil
	default:
		return "", fmt.Errorf("unsupported shell: %s", shell)
	}
}

func removeFencedBlock(content, startFence, endFence string) (string, bool) {
	start := strings.Index(content, startFence)
	if start == -1 {
		return content, false
	}
	end := strings.Index(content[start:], endFence)
	if end == -1 {
		return content, false
	}
	end += start + len(endFence)
	for start > 0 && content[start-1] == '\n' {
		start--
	}
	if end < len(content) && content[end] == '\n' {
		end++
	}
	return content[:start] + content[end:], true
}

func InstallShellPrompt(shell string, force, dryRun bool) (string, error) {
	shell = detectShell(shell)
	if shell == "" {
		return "", fmt.Errorf("could not detect shell")
	}

	rcPath, err := rcFilePath(shell)
	if err != nil {
		return "", err
	}

	snippet, err := snippetForShell(shell)
	if err != nil {
		return "", err
	}

	content, err := os.ReadFile(rcPath)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read %s: %w", rcPath, err)
	}
	contentStr := string(content)

	hasFences := strings.Contains(contentStr, fenceStart)

	if hasFences && !force {
		return "", nil
	}

	var newContent string
	if hasFences && force {
		cleaned, _ := removeFencedBlock(contentStr, fenceStart, fenceEnd)
		if !strings.HasSuffix(cleaned, "\n") {
			cleaned += "\n"
		}
		newContent = cleaned + snippet
	} else {
		if len(contentStr) > 0 && !strings.HasSuffix(contentStr, "\n") {
			contentStr += "\n"
		}
		newContent = contentStr + snippet
	}

	if dryRun {
		fmt.Printf("Would write to %s:\n", rcPath)
		if contentStr != newContent {
			fmt.Print(diff(contentStr, newContent))
		} else {
			fmt.Println("(no changes)")
		}
		return "", nil
	}

	if err := os.WriteFile(rcPath, []byte(newContent), 0644); err != nil {
		return "", fmt.Errorf("write %s: %w", rcPath, err)
	}
	return rcPath, nil
}

func UninstallShellPrompt(shell string) (string, error) {
	shell = detectShell(shell)
	if shell == "" {
		return "", fmt.Errorf("could not detect shell")
	}

	rcPath, err := rcFilePath(shell)
	if err != nil {
		return "", err
	}

	content, err := os.ReadFile(rcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", rcPath, err)
	}
	contentStr := string(content)

	cleaned, removed := removeFencedBlock(contentStr, fenceStart, fenceEnd)
	if !removed {
		return "", nil
	}

	if cleaned == "" {
		if err := os.Remove(rcPath); err != nil {
			return "", fmt.Errorf("remove %s: %w", rcPath, err)
		}
		return rcPath, nil
	}

	if err := os.WriteFile(rcPath, []byte(cleaned), 0644); err != nil {
		return "", fmt.Errorf("write %s: %w", rcPath, err)
	}
	return rcPath, nil
}

func diff(a, b string) string {
	al := strings.Split(a, "\n")
	bl := strings.Split(b, "\n")
	var out strings.Builder
	i, j := 0, 0
	for i < len(al) || j < len(bl) {
		if i < len(al) && j < len(bl) && al[i] == bl[j] {
			out.WriteString("  " + al[i] + "\n")
			i++
			j++
		} else {
			if i < len(al) {
				out.WriteString("- " + al[i] + "\n")
				i++
			}
			for j < len(bl) && (i >= len(al) || al[i] != bl[j]) {
				out.WriteString("+ " + bl[j] + "\n")
				j++
			}
		}
	}
	return out.String()
}
