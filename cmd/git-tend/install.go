package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sdougbrown/cli-contract"

	"github.com/sdougbrown/git-tend/internal/config"
	"github.com/sdougbrown/git-tend/internal/install"
	"github.com/sdougbrown/git-tend/internal/paths"
)

var installCmd = &clicontract.Command{
	Name:  "install",
	Short: "Install the git-tend daemon as a service",
	Long:  `Install the git-tend daemon as a launchd (macOS) or systemd (Linux) user service.`,
	Flags: []clicontract.Flag{
		{Name: "user-only", Kind: clicontract.Bool, Usage: "Write the unit/plist file but don't load the service"},
		{Name: "shell-prompt", Kind: clicontract.Bool, Usage: "Install the prompt snippet into shell rc"},
		{Name: "shell-greet", Kind: clicontract.Bool, Usage: "Install the greet snippet into shell rc"},
		{Name: "dry-run", Kind: clicontract.Bool, Usage: "Print what would be done, don't actually do it"},
		{Name: "force", Kind: clicontract.Bool, Usage: "Overwrite existing shell prompt fences"},
		{Name: "shell", Kind: clicontract.String, Usage: "Shell name override (zsh, bash, fish)"},
	},
	Handle: func(ctx context.Context, inv clicontract.Invocation) (any, error) {
		return nil, runInstall(installOpts{
			userOnly:    inv.Bools["user-only"],
			shellPrompt: inv.Bools["shell-prompt"],
			shellGreet:  inv.Bools["shell-greet"],
			dryRun:      inv.Bools["dry-run"],
			force:       inv.Bools["force"],
			shell:       inv.Flags["shell"],
		})
	},
}

type installOpts struct {
	userOnly    bool
	shellPrompt bool
	shellGreet  bool
	dryRun      bool
	force       bool
	shell       string
}

func runInstall(opts installOpts) error {
	hasShellFlag := opts.shellPrompt || opts.shellGreet

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

		if !opts.userOnly {
			if err := install.LoadService(); err != nil {
				return fmt.Errorf("loading service: %w", err)
			}
			fmt.Println("service loaded")
		}
	}

	if opts.shellPrompt {
		result, err := install.InstallShellPrompt(opts.shell, opts.force, opts.dryRun)
		if err != nil {
			return fmt.Errorf("installing shell prompt: %w", err)
		}
		if result != "" {
			fmt.Printf("prompt installed in %s\n", result)
		} else if opts.dryRun {
		} else {
			fmt.Println("prompt already installed")
		}
	}

	if opts.shellGreet {
		result, err := install.InstallShellGreet(opts.shell, opts.force, opts.dryRun)
		if err != nil {
			return fmt.Errorf("installing shell greet: %w", err)
		}
		if result != "" {
			fmt.Printf("greet installed in %s\n", result)
		} else if opts.dryRun {
		} else {
			fmt.Println("greet already installed")
		}
	}

	return nil
}

var uninstallCmd = &clicontract.Command{
	Name:  "uninstall",
	Short: "Uninstall the git-tend daemon service",
	Long:  `Unload and remove the git-tend launchd (macOS) or systemd (Linux) user service.`,
	Flags: []clicontract.Flag{
		{Name: "shell-prompt", Kind: clicontract.Bool, Usage: "Remove the prompt snippet from shell rc"},
		{Name: "shell-greet", Kind: clicontract.Bool, Usage: "Remove the greet snippet from shell rc"},
		{Name: "shell", Kind: clicontract.String, Usage: "Shell name override (zsh, bash, fish)"},
	},
	Handle: func(ctx context.Context, inv clicontract.Invocation) (any, error) {
		return nil, runUninstall(uninstallOpts{
			shellPrompt: inv.Bools["shell-prompt"],
			shellGreet:  inv.Bools["shell-greet"],
			shell:       inv.Flags["shell"],
		})
	},
}

type uninstallOpts struct {
	shellPrompt bool
	shellGreet  bool
	shell       string
}

func runUninstall(opts uninstallOpts) error {
	hasShellFlag := opts.shellPrompt || opts.shellGreet

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

	if opts.shellPrompt {
		result, err := install.UninstallShellPrompt(opts.shell)
		if err != nil {
			return fmt.Errorf("uninstalling shell prompt: %w", err)
		}
		if result != "" {
			fmt.Printf("prompt removed from %s\n", result)
		} else {
			fmt.Println("prompt not found")
		}
	}

	if opts.shellGreet {
		result, err := install.UninstallShellGreet(opts.shell)
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
