package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dbrown/git-tend/internal/config"
	"github.com/dbrown/git-tend/internal/paths"
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

func ScanRoots(roots []string, maxDepth int) []Repo {
	var repos []Repo

	for _, root := range roots {
		root = paths.ExpandPath(root)
		repos = append(repos, walkRoot(root, maxDepth)...)
	}

	return repos
}

func walkRoot(root string, maxDepth int) []Repo {
	var repos []Repo

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

		if strings.HasPrefix(name, ".") {
			return filepath.SkipDir
		}

		if excludeNames[name] {
			return filepath.SkipDir
		}

		dotGit := filepath.Join(path, ".git")
		gittend := filepath.Join(path, ".gittend")

		gitInfo, gitErr := os.Stat(dotGit)
		if gitErr != nil || !gitInfo.IsDir() {
			return nil
		}

		if _, err := os.Stat(gittend); err != nil {
			return nil
		}

		cfg, parseErr := config.Parse(gittend)
		if parseErr != nil {
			warn("parsing .gittend in %s: %v", path, parseErr)
			return nil
		}

		repos = append(repos, Repo{Path: path, Config: cfg})
		return filepath.SkipDir
	})

	return repos
}

func warn(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "WARN: "+format+"\n", args...)
}
