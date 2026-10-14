package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/sdougbrown/git-tend/internal/config"
)

// stuckFile mirrors the on-disk format of .gittend.stuck, which writeStuck
// emits as TOML.
type stuckFile struct {
	StuckAt      string `toml:"stuck_at"`
	Reason       string `toml:"reason"`
	LastCommand  string `toml:"last_command"`
	LastExitCode int    `toml:"last_exit_code"`
	LastOutput   string `toml:"last_output"`
}

// stuckRecord is the daemon-issued provenance record for a stuck episode.
// writeStuck writes one to the state dir whenever it writes the repo-local
// .gittend.stuck file. The repo-local file is untrusted — anyone with write
// access to the repo can forge one — so RunStuckAgent refuses to act unless
// the file's stuck_at matches a daemon-issued record, and the record's
// daemon-observed reason is authoritative for the allowlist.
type stuckRecord struct {
	StuckAt   string `json:"stuck_at"`
	Reason    string `json:"reason"`
	WrittenAt string `json:"written_at"`
}

// agentAttempt is the per-repo attempt counter for the current stuck episode,
// persisted in the state dir so it survives daemon restarts. Episode is the
// stuck_at of the daemon-issued stuck record the counter applies to.
type agentAttempt struct {
	Episode      string `json:"episode"`
	Attempts     int    `json:"attempts"`
	LastExitCode int    `json:"last_exit_code"`
	LastRunAt    string `json:"last_run_at"`
}

// AgentRunResult reports what RunStuckAgent did.
type AgentRunResult struct {
	// Ran is true when the agent command was invoked.
	Ran bool
	// Recovered is true when the command exited 0 and the stuck flag was removed.
	Recovered bool
	// Attempts is the attempt count for the current episode after this run.
	Attempts int
	// MaxAttempts is the configured cap, 0 when the agent is not configured.
	MaxAttempts int
	// Err is the command's error, when the run failed.
	Err error
}

// AgentStatePath returns the state-dir file holding the attempt counter for a
// repo. It is exported so `git-tend unstick` can reset the counter alongside
// the stuck flag.
func AgentStatePath(stateDir, repoPath string) string {
	hash := sha256.Sum256([]byte(repoPath))
	return filepath.Join(stateDir, "agent", hex.EncodeToString(hash[:])+".json")
}

// StuckRecordPath returns the state-dir file holding the daemon-issued
// provenance record for a repo's stuck episode. Exported so `git-tend unstick`
// can clear it alongside the stuck flag.
func StuckRecordPath(stateDir, repoPath string) string {
	hash := sha256.Sum256([]byte(repoPath))
	return filepath.Join(stateDir, "stuck", hex.EncodeToString(hash[:])+".json")
}

// RunStuckAgent runs the repo's configured on_stuck_cmd against a stuck repo.
//
// The agent run happens outside sync()'s per-repo flock. Every sync acquisition
// is non-blocking and, while the stuck flag exists, sync exits early anyway —
// the flag is what actually serializes the agent against syncs, so holding the
// lock for minutes of agent time would only delay concurrent `git-tend run`
// and manual syncs for no protection. Concurrent agent runs can't happen: the
// daemon's tick loop is the only caller and it is single-threaded.
func RunStuckAgent(ctx context.Context, repoPath string, cfg *config.Config, stateDir string, logger *slog.Logger) AgentRunResult {
	agent := cfg.Agent
	res := AgentRunResult{MaxAttempts: agent.MaxAttempts}

	if !agent.Enabled() {
		return res
	}

	stuck, err := readStuckFile(repoPath)
	if err != nil {
		// No flag or unparsable contents: nothing to recover from.
		if logger != nil {
			logger.Debug("agent unstick: no readable stuck file", "repo", repoPath, "error", err)
		}
		return res
	}

	// Provenance: the repo-local file can be forged by anything that can write
	// to the repo. Only act when the file's stuck_at matches a record the
	// daemon itself wrote, and treat the daemon-observed reason — not the
	// file's — as authoritative for the allowlist.
	rec, err := readStuckRecord(StuckRecordPath(stateDir, repoPath))
	if err != nil || rec.StuckAt != stuck.StuckAt {
		if logger != nil {
			logger.Debug("agent unstick: no matching daemon-issued stuck record", "repo", repoPath)
		}
		return res
	}

	if !agent.AllowsReason(rec.Reason) {
		if logger != nil {
			logger.Debug("agent unstick: reason not allowed", "repo", repoPath, "reason", rec.Reason)
		}
		return res
	}

	recordPath := AgentStatePath(stateDir, repoPath)
	record := readAgentAttempt(recordPath)
	if record.Episode != stuck.StuckAt {
		// New stuck file: new episode, counter starts fresh.
		record = agentAttempt{Episode: stuck.StuckAt}
	}
	res.Attempts = record.Attempts
	if record.Attempts >= agent.MaxAttempts {
		if logger != nil {
			logger.Info("agent unstick: attempt cap reached, waiting for human", "repo", repoPath,
				"attempts", record.Attempts, "max_attempts", agent.MaxAttempts)
		}
		return res
	}

	cmdCtx, cancel := context.WithTimeout(ctx, parseAgentTimeout(agent.Timeout))
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "sh", "-c", agent.OnStuckCmd)
	cmd.Dir = repoPath
	cmd.WaitDelay = 10 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		res.Err = err
		return res
	}
	go func() {
		defer stdin.Close()
		writeAgentContext(stdin, repoPath, cfg.SyncBranch, stuck)
	}()

	res.Ran = true
	runErr := cmd.Run()
	record.Attempts++
	record.LastRunAt = time.Now().UTC().Format(time.RFC3339)
	record.Episode = stuck.StuckAt
	res.Attempts = record.Attempts

	if runErr == nil {
		// Recovery: clear the flag and the provenance record so the next tick
		// syncs normally, and a future episode starts from zero.
		if err := os.Remove(filepath.Join(repoPath, ".gittend.stuck")); err != nil && !os.IsNotExist(err) {
			res.Err = fmt.Errorf("exit 0 but removing stuck flag: %w", err)
			_ = writeAgentAttempt(recordPath, record)
			return res
		}
		_ = os.Remove(StuckRecordPath(stateDir, repoPath))
		_ = os.Remove(recordPath)
		res.Recovered = true
		if logger != nil {
			logger.Info("agent unstick: recovered", "repo", repoPath, "reason", stuck.Reason)
		}
		return res
	}

	res.Err = runErr
	record.LastExitCode = exitCodeFromErr(runErr)
	if err := writeAgentAttempt(recordPath, record); err != nil && logger != nil {
		logger.Error("agent unstick: saving attempt record", "repo", repoPath, "error", err)
	}
	if logger != nil {
		logger.Warn("agent unstick: command failed", "repo", repoPath, "error", runErr,
			"attempts", record.Attempts, "max_attempts", agent.MaxAttempts)
	}
	return res
}

// maxStuckFileBytes caps how much of the repo-controlled stuck file is read.
const maxStuckFileBytes = 1 << 20

func readStuckFile(repoPath string) (*stuckFile, error) {
	f, err := os.Open(filepath.Join(repoPath, ".gittend.stuck"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// The stuck file is repo-controlled input; never read it unbounded.
	data, err := io.ReadAll(io.LimitReader(f, maxStuckFileBytes))
	if err != nil {
		return nil, err
	}
	var s stuckFile
	if err := toml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing stuck file: %w", err)
	}
	return &s, nil
}

func readStuckRecord(path string) (*stuckRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rec stuckRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("parsing stuck record: %w", err)
	}
	return &rec, nil
}

func writeStuckRecord(stateDir, repoPath, stuckAt, reason string) error {
	path := StuckRecordPath(stateDir, repoPath)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	rec := stuckRecord{
		StuckAt:   stuckAt,
		Reason:    reason,
		WrittenAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// writeAgentContext writes the structured context document the agent command
// receives on stdin: the stuck file contents plus the repo path and sync branch.
func writeAgentContext(w io.Writer, repoPath, syncBranch string, stuck *stuckFile) {
	fmt.Fprintf(w, "repo = %q\n", repoPath)
	fmt.Fprintf(w, "sync_branch = %q\n", syncBranch)
	fmt.Fprintf(w, "stuck_at = %q\n", stuck.StuckAt)
	fmt.Fprintf(w, "reason = %q\n", stuck.Reason)
	fmt.Fprintf(w, "last_command = %q\n", stuck.LastCommand)
	fmt.Fprintf(w, "last_exit_code = %d\n", stuck.LastExitCode)
	fmt.Fprintf(w, "last_output = \"\"\"\n%s\"\"\"\n", stuck.LastOutput)
}

func readAgentAttempt(path string) agentAttempt {
	data, err := os.ReadFile(path)
	if err != nil {
		return agentAttempt{}
	}
	var a agentAttempt
	if json.Unmarshal(data, &a) != nil {
		return agentAttempt{}
	}
	return a
}

func writeAgentAttempt(path string, a agentAttempt) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func parseAgentTimeout(timeout string) time.Duration {
	if d, err := time.ParseDuration(timeout); err == nil && d > 0 {
		return d
	}
	return 10 * time.Minute
}
