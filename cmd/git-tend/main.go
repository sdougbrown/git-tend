package main

import (
	"context"
	"io"
	"os"

	"github.com/sdougbrown/cli-contract"
)

// Version is the release tag, injected at build time via -ldflags. Unreleased
// builds default to "dev" so they don't masquerade as a real version.
var Version = "dev"

// versionTemplate renders the --version output.
const versionTemplate = "git-tend v{{.Version}}\n"

// app assembles the declared CLI.
func app() *clicontract.App {
	return &clicontract.App{
		Name:            "git-tend",
		Version:         Version,
		VersionTemplate: versionTemplate,
		RootShort:       "Background repo auto-sync daemon",
		RootLong:        `git-tend keeps configured git repos in sync in the background.`,
		Commands: []*clicontract.Command{
			daemonCmd,
			greetCmd,
			installCmd,
			uninstallCmd,
			logsCmd,
			promptCmd,
			reloadCmd,
			restartCmd,
			runCmd,
			snoozeCmd,
			statusCmd,
			unstickCmd,
		},
	}
}

// tendAdapter implements the result vocabulary: git-tend's handlers print
// their own human output directly, failures go to stderr, and every failure
// exits 1.
type tendAdapter struct{}

// ErrorResult returns nothing: this CLI has no envelope, so dispatch-level
// failures write only their stderr diagnostic.
func (tendAdapter) ErrorResult(command string, err error) any { return nil }

// Write is a no-op: handlers print their own output.
func (tendAdapter) Write(w io.Writer, result any) error { return nil }

// ExitCode reports 1 for every failure, 0 otherwise.
func (tendAdapter) ExitCode(inv clicontract.Invocation, result any, handlerErr error) int {
	if handlerErr != nil {
		return 1
	}
	return 0
}

// Deadline passes the context through: none of these commands bound their
// own runtime.
func (tendAdapter) Deadline(ctx context.Context, inv clicontract.Invocation) (context.Context, context.CancelFunc) {
	return ctx, func() {}
}

// Validate reports no pre-flight caller errors.
func (tendAdapter) Validate(inv clicontract.Invocation) error { return nil }

// BeforeHandler runs no pre-flight diagnostics.
func (tendAdapter) BeforeHandler(inv clicontract.Invocation, stderr io.Writer) {}

func main() {
	os.Exit(clicontract.Run(app(), tendAdapter{}, os.Args[1:], os.Stdout, os.Stderr))
}
