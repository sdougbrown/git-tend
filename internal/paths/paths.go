package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func StateDir() string {
	if runtime.GOOS == "darwin" {
		return appSupportDir() + "/git-tend/"
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".local", "state")
	}
	return filepath.Join(base, "git-tend") + "/"
}

func LogDir() string {
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Logs", "git-tend") + "/"
	}
	return StateDir()
}

func ConfigDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "git-tend") + "/"
}

func BinDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin") + "/"
}

func ExpandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[2:])
		}
	} else if strings.HasPrefix(path, "$HOME") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = strings.Replace(path, "$HOME", home, 1)
		}
	}

	// Managed-repo state is keyed by paths found during scanning, which are
	// absolute. Normalize CLI paths as well so `run .` updates that same entry.
	if absolute, err := filepath.Abs(path); err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(path)
}

// SamePath reports whether two paths refer to the same directory, after
// tilde expansion, cleaning, and best-effort symlink resolution. Resolution
// is attempted on each side independently, so a symlinked root (e.g. a
// ~/Code symlink farm) still matches its real path; paths that do not exist
// are compared as cleaned strings. On darwin, where the default file
// systems are case-insensitive, the comparison ignores case so an entry
// path differing only in case from the scanned path still matches.
func SamePath(a, b string) bool {
	return samePath(a, b, runtime.GOOS == "darwin")
}

// samePath is SamePath with the case sensitivity of the comparison made
// explicit so it can be exercised on case-sensitive file systems too.
func samePath(a, b string, caseInsensitive bool) bool {
	equal := func(x, y string) bool { return x == y }
	if caseInsensitive {
		equal = strings.EqualFold
	}
	a = ExpandPath(a)
	b = ExpandPath(b)
	if equal(a, b) {
		return true
	}
	resA, okA := resolve(a)
	resB, okB := resolve(b)
	if okA && okB {
		return equal(resA, resB)
	}
	if okA {
		return equal(resA, b)
	}
	if okB {
		return equal(a, resB)
	}
	return false
}

func resolve(path string) (string, bool) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path, false
	}
	return resolved, true
}

func appSupportDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support")
}
