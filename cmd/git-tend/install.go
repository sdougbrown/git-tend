package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sdougbrown/git-tend/internal/install"
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
