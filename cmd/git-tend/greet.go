package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sdougbrown/cli-contract"

	"github.com/sdougbrown/git-tend/internal/config"
	"github.com/sdougbrown/git-tend/internal/paths"
	"github.com/sdougbrown/git-tend/internal/status"
)

var greetCmd = &clicontract.Command{
	Name:  "greet",
	Short: "Print once-per-day startup summary",
	Flags: []clicontract.Flag{
		{Name: "no-color", Kind: clicontract.Bool, Usage: "Disable ANSI color output"},
	},
	Handle: func(ctx context.Context, inv clicontract.Invocation) (any, error) {
		return nil, runGreet(inv.Bools["no-color"])
	},
}

func runGreet(noColor bool) error {
	if os.Getenv("NO_COLOR") != "" {
		noColor = true
	}

	stateDir := paths.StateDir()
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}

	greetLastPath := filepath.Join(stateDir, "greet.last")
	today := time.Now().Format("2006-01-02")
	if data, err := os.ReadFile(greetLastPath); err == nil {
		if strings.TrimSpace(string(data)) == today {
			return nil
		}
	}

	sf := status.Read(filepath.Join(stateDir, "status.json"))
	if sf == nil {
		if err := os.WriteFile(greetLastPath, []byte(today+"\n"), 0644); err != nil {
			return fmt.Errorf("writing greet.last: %w", err)
		}
		return nil
	}

	cfg, err := config.ParseUserConfig(filepath.Join(paths.ConfigDir(), "config.toml"))
	if err != nil {
		cfg = &config.UserConfig{EscalateAfterDays: 3}
	}

	var stuckPaths []string
	var escalated []struct {
		path string
		days int
	}
	now := time.Now()

	for path, rs := range sf.Repos {
		if rs.CurrentState != "stuck" {
			continue
		}
		stuckPaths = append(stuckPaths, path)
		if rs.StuckSince != "" {
			t, err := time.Parse(time.RFC3339, rs.StuckSince)
			if err == nil {
				days := int(now.Sub(t).Hours() / 24)
				if days >= cfg.EscalateAfterDays {
					escalated = append(escalated, struct {
						path string
						days int
					}{path, days})
				}
			}
		}
	}

	if len(stuckPaths) > 0 {
		pathList := strings.Join(stuckPaths, ", ")
		prefix := "\u26a0 git-tend: "
		msg := fmt.Sprintf("%d repos stuck (%s) \u2014 run 'git-tend status'", len(stuckPaths), pathList)
		if noColor {
			fmt.Println(prefix + msg)
		} else {
			fmt.Println("\033[33m" + prefix + "\033[0m" + msg)
		}

		for _, e := range escalated {
			line := fmt.Sprintf("  %s STUCK FOR %dd", e.path, e.days)
			if noColor {
				fmt.Println(line)
			} else {
				fmt.Println("\033[31m" + line + "\033[0m")
			}
		}
	}

	if err := os.WriteFile(greetLastPath, []byte(today+"\n"), 0644); err != nil {
		return fmt.Errorf("writing greet.last: %w", err)
	}

	return nil
}
