package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version is the release tag, injected at build time via -ldflags. Unreleased
// builds default to "dev" so they don't masquerade as a real version.
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:     "git-tend",
	Short:   "Background repo auto-sync daemon",
	Long:    `git-tend keeps configured git repos in sync in the background.`,
	Version: Version,
}

func main() {
	rootCmd.SetVersionTemplate("git-tend v{{.Version}}\n")
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
