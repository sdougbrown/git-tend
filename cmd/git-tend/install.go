package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sdougbrown/git-tend/internal/config"
	"github.com/sdougbrown/git-tend/internal/install"
	"github.com/sdougbrown/git-tend/internal/paths"
)

var (
	installUserOnly    bool
	installShellPrompt bool
	installShellGreet  bool
	installDryRun      bool
	installForce       bool
	installShell       string
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the git-tend daemon as a service",
	Long:  `Install the git-tend daemon as a launchd (macOS) or systemd (Linux) user service.`,
	RunE:  runInstall,
}

var (
	uninstallShellPrompt bool
	uninstallShellGreet  bool
	uninstallShell       string
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall the git-tend daemon service",
	Long:  `Unload and remove the git-tend launchd (macOS) or systemd (Linux) user service.`,
	RunE:  runUninstall,
}

func init() {
	installCmd.Flags().BoolVar(&installUserOnly, "user-only", false, "Write the unit/plist file but don't load the service")
	installCmd.Flags().BoolVar(&installShellPrompt, "shell-prompt", false, "Install the prompt snippet into shell rc")
	installCmd.Flags().BoolVar(&installShellGreet, "shell-greet", false, "Install the greet snippet into shell rc")
	installCmd.Flags().BoolVar(&installDryRun, "dry-run", false, "Print what would be done, don't actually do it")
	installCmd.Flags().BoolVar(&installForce, "force", false, "Overwrite existing shell prompt fences")
	installCmd.Flags().StringVar(&installShell, "shell", "", "Shell name override (zsh, bash, fish)")

	uninstallCmd.Flags().BoolVar(&uninstallShellPrompt, "shell-prompt", false, "Remove the prompt snippet from shell rc")
	uninstallCmd.Flags().BoolVar(&uninstallShellGreet, "shell-greet", false, "Remove the greet snippet from shell rc")
	uninstallCmd.Flags().StringVar(&uninstallShell, "shell", "", "Shell name override (zsh, bash, fish)")

	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(uninstallCmd)
}

func runInstall(cmd *cobra.Command, args []string) error {
	hasShellFlag := installShellPrompt || installShellGreet

	if !hasShellFlag {
		var servicePath string
		var err error
		if install.IsMacOS() {
			servicePath, err = install.WriteLaunchdPlist()
		} else if install.IsLinux() {
			servicePath, err = install.WriteSystemdUnit()
		} else {
			return fmt.Errorf("unsupported platform: only macOS and Linux are supported")
		}
		if err != nil {
			return fmt.Errorf("writing service file: %w", err)
		}
		fmt.Printf("service file written to %s\n", servicePath)

		if err := bootstrapUserConfig(); err != nil {
			return fmt.Errorf("bootstrapping config: %w", err)
		}

		if !installUserOnly {
			if err := install.LoadService(); err != nil {
				return fmt.Errorf("loading service: %w", err)
			}
			fmt.Println("service loaded")
		}
	}

	if installShellPrompt {
		result, err := install.InstallShellPrompt(installShell, installForce, installDryRun)
		if err != nil {
			return fmt.Errorf("installing shell prompt: %w", err)
		}
		if result != "" {
			fmt.Printf("prompt installed in %s\n", result)
		} else if installDryRun {
		} else {
			fmt.Println("prompt already installed")
		}
	}

	if installShellGreet {
		result, err := install.InstallShellGreet(installShell, installForce, installDryRun)
		if err != nil {
			return fmt.Errorf("installing shell greet: %w", err)
		}
		if result != "" {
			fmt.Printf("greet installed in %s\n", result)
		} else if installDryRun {
		} else {
			fmt.Println("greet already installed")
		}
	}

	return nil
}

func bootstrapUserConfig() error {
	configPath := filepath.Join(paths.ConfigDir(), "config.toml")
	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("config already present at %s — leaving untouched\n", configPath)
		return nil
	}

	roots := config.DefaultRoots()
	if isStdinTTY() {
		if chosen, ok := promptForRoots(roots); ok {
			roots = chosen
		}
	}

	if err := config.WriteDefaultConfig(configPath, roots); err != nil {
		return err
	}
	fmt.Printf("config written to %s (scanning: %s)\n", configPath, strings.Join(roots, ", "))
	fmt.Println("edit it any time, then run 'git-tend reload' to apply changes")
	return nil
}

func isStdinTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func promptForRoots(def []string) ([]string, bool) {
	fmt.Printf("Which directories should git-tend scan for .gittend repos?\n")
	fmt.Printf("  (comma-separated; press enter for default) [%s]: ", strings.Join(def, ", "))

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, false
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, false
	}

	parts := strings.Split(line, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func runUninstall(cmd *cobra.Command, args []string) error {
	hasShellFlag := uninstallShellPrompt || uninstallShellGreet

	if !hasShellFlag {
		if err := install.UnloadService(); err != nil {
			return fmt.Errorf("unloading service: %w", err)
		}
		fmt.Println("service unloaded")

		if err := install.RemoveServiceFiles(); err != nil {
			return fmt.Errorf("removing service files: %w", err)
		}
		fmt.Println("service files removed")
	}

	if uninstallShellPrompt {
		result, err := install.UninstallShellPrompt(uninstallShell)
		if err != nil {
			return fmt.Errorf("uninstalling shell prompt: %w", err)
		}
		if result != "" {
			fmt.Printf("prompt removed from %s\n", result)
		} else {
			fmt.Println("prompt not found")
		}
	}

	if uninstallShellGreet {
		result, err := install.UninstallShellGreet(uninstallShell)
		if err != nil {
			return fmt.Errorf("uninstalling shell greet: %w", err)
		}
		if result != "" {
			fmt.Printf("greet removed from %s\n", result)
		} else {
			fmt.Println("greet not found")
		}
	}

	return nil
}
