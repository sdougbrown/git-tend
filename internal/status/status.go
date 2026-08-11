package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

type StatusFile struct {
	Version         int                   `json:"version"`
	DaemonStartedAt string                `json:"daemon_started_at"`
	LastTickAt      string                `json:"last_tick_at"`
	IntervalSeconds int                   `json:"interval_seconds"`
	Repos           map[string]RepoStatus `json:"repos"`
}

type RepoStatus struct {
	Mode                       string `json:"mode"`
	CurrentState               string `json:"current_state"`
	PriorState                 string `json:"prior_state"`
	UpdatedAt                  string `json:"updated_at"`
	LastSyncAt                 string `json:"last_sync_at"`
	LastError                  string `json:"last_error"`
	Ahead                      int    `json:"ahead"`
	Behind                     int    `json:"behind"`
	StuckSince                 string `json:"stuck_since"`
	SnoozedUntil               string `json:"snoozed_until"`
	OfflineSince               string `json:"offline_since"`
	ConsecutiveOfflineFailures int    `json:"consecutive_offline_failures"`
}

func Read(path string) *StatusFile {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var sf StatusFile
	if err := json.Unmarshal(data, &sf); err != nil {
		time.Sleep(10 * time.Millisecond)
		data, err = os.ReadFile(path)
		if err != nil {
			return nil
		}
		if err := json.Unmarshal(data, &sf); err != nil {
			return nil
		}
	}

	return &sf
}

// UpdateRepo atomically updates one repo's status. It is used by foreground
// commands, which can run concurrently with the daemon.
func UpdateRepo(path, repoPath string, update func(RepoStatus) RepoStatus) error {
	return withLock(path, func() error {
		sf := read(path)
		if sf == nil {
			sf = &StatusFile{Version: 1, Repos: make(map[string]RepoStatus)}
		}
		if sf.Repos == nil {
			sf.Repos = make(map[string]RepoStatus)
		}
		sf.Repos[repoPath] = update(sf.Repos[repoPath])
		return write(path, sf)
	})
}

// MergeAndWrite writes the daemon's snapshot without replacing a newer
// foreground-command result. Both writers share the sidecar lock.
func MergeAndWrite(path string, sf *StatusFile) error {
	return withLock(path, func() error {
		current := read(path)
		if current != nil {
			for repoPath, candidate := range sf.Repos {
				if existing, ok := current.Repos[repoPath]; ok && newerOrEqual(existing.UpdatedAt, candidate.UpdatedAt) {
					sf.Repos[repoPath] = existing
				}
			}
		}
		return write(path, sf)
	})
}

func Write(path string, sf *StatusFile) error {
	return withLock(path, func() error { return write(path, sf) })
}

func read(path string) *StatusFile {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var sf StatusFile
	if json.Unmarshal(data, &sf) != nil {
		return nil
	}
	return &sf
}

func write(path string, sf *StatusFile) error {
	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func withLock(path string, fn func() error) error {
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	return fn()
}

func newerOrEqual(a, b string) bool {
	at, errA := time.Parse(time.RFC3339, a)
	bt, errB := time.Parse(time.RFC3339, b)
	return errA == nil && (errB != nil || !at.Before(bt))
}
