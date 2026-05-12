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
	return path
}

func appSupportDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support")
}
