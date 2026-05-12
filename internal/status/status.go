package status

import (
	"encoding/json"
	"os"
	"time"
)

type StatusFile struct {
	Version         int                  `json:"version"`
	DaemonStartedAt string               `json:"daemon_started_at"`
	LastTickAt      string               `json:"last_tick_at"`
	IntervalSeconds int                  `json:"interval_seconds"`
	Repos           map[string]RepoStatus `json:"repos"`
}

type RepoStatus struct {
	Mode                      string `json:"mode"`
	CurrentState              string `json:"current_state"`
	PriorState                string `json:"prior_state"`
	LastSyncAt                string `json:"last_sync_at"`
	LastError                 string `json:"last_error"`
	Ahead                     int    `json:"ahead"`
	Behind                    int    `json:"behind"`
	StuckSince                string `json:"stuck_since"`
	SnoozedUntil              string `json:"snoozed_until"`
	OfflineSince              string `json:"offline_since"`
	ConsecutiveOfflineFailures int   `json:"consecutive_offline_failures"`
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

func Write(path string, sf *StatusFile) error {
	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmpPath, path)
}
