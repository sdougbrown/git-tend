package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sdougbrown/git-tend/internal/config"
	"github.com/sdougbrown/git-tend/internal/paths"
)

type Repo struct {
	Path   string
	Config *config.Config
}

var excludeNames = map[string]bool{
	"node_modules": true,
	"target":       true,
	"vendor":       true,
	".terraform":   true,
	"dist":         true,
	"build":        true,
	"__pycache__":  true,
}

// ScanRoots discovers repos under the configured roots and from the user
// config's [[repo]] entries, resolving each repo's effective config by
// merging the matching user entry (if any) over its .gittend (if any).
func ScanRoots(userCfg *config.UserConfig) []Repo {
	var repos []Repo
	var discovered []string

	for _, root := range userCfg.Roots {
		for _, path := range walkRoot(paths.ExpandPath(root), userCfg.ScanDepth) {
			discovered = append(discovered, path)
			if repo, ok := loadRepo(path, userCfg); ok {
				repos = append(repos, repo)
			}
		}
	}

	// Entries opt repos in even without a .gittend file, and are honored even
	// outside the configured roots.
	for _, entry := range userCfg.Repos {
		duplicate := false
		for _, path := range discovered {
			if paths.SamePath(path, entry.Path) {
				duplicate = true // already loaded with its entry merged in
				break
			}
		}
		if duplicate {
			continue
		}
		if !isGitRepo(entry.Path) {
			warn("user config repo %s: not a git repo, skipping", entry.Path)
			continue
		}
		if repo, ok := loadRepo(entry.Path, userCfg); ok {
			repos = append(repos, repo)
		}
	}

	return repos
}

// loadRepo resolves the effective config for the repo at path. The second
// return is false when the repo's config fails to resolve.
func loadRepo(path string, userCfg *config.UserConfig) (Repo, bool) {
	cfg, err := config.ResolveRepo(path, userCfg)
	if err != nil {
		warn("resolving config for %s: %v", path, err)
		return Repo{}, false
	}

	return Repo{Path: path, Config: cfg}, true
}

// isGitRepo reports whether path looks like a git repository: it contains a
// .git directory. A .git file (a worktree) does not count, so worktrees are
// never synced by the daemon.
func isGitRepo(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && info.IsDir()
}

func walkRoot(root string, maxDepth int) []string {
	var found []string

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			warn("walking %s: %v", path, err)
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		depth := strings.Count(rel, string(filepath.Separator))

		if depth > maxDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if !d.IsDir() {
			return nil
		}

		name := d.Name()
		if name == ".git" {
			return filepath.SkipDir
		}

		if path != root && strings.HasPrefix(name, ".") {
			return filepath.SkipDir
		}

		if excludeNames[name] {
			return filepath.SkipDir
		}

		if !isGitRepo(path) {
			return nil
		}

		if _, err := os.Stat(filepath.Join(path, ".gittend")); err != nil {
			return nil
		}

		found = append(found, path)
		return filepath.SkipDir
	})

	return found
}

func warn(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "WARN: "+format+"\n", args...)
}
