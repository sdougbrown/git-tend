package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/sys/unix"

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
// writeStuck writes one to the state dir before writing the repo-local
// .gittend.stuck file. The repo-local file is untrusted — anyone with write
// access to the repo can forge one — so RunStuckAgent refuses to act unless
// the file's stuck_at matches a daemon-issued record, and the record's
// daemon-observed reason is authoritative for the allowlist.
type stuckRecord struct {
	StuckAt   string `json:"stuck_at"`
	Reason    string `json:"reason"`
	WrittenAt string `json:"written_at"`
}

// agentAttempt is the per-repo attempt counter, persisted in the state dir so
// it survives daemon restarts. The budget is cumulative for the repo, not per
// stuck file: it resets only when a sync actually succeeds or a human runs
// `git-tend unstick`. An agent exit 0 alone doesn't reset it — otherwise an
// agent that clears the flag without actually fixing the repo would re-arm a
// full budget on every failed sync. Episode records the stuck_at of the last
// episode seen, for diagnostics only.
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
	// MaxAttempts is the configured cap. It is 0 unless the agent is engaged
	// with a valid stuck episode (provenance verified, reason allowed), so
	// status never claims an agent is pending when it isn't.
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
// and manual syncs for no protection. Concurrent agent runs are excluded by a
// dedicated per-repo flock (see AcquireAgentLock), which also covers the
// daemon-restart case: the lock fd is passed to the agent command itself, so
// the kernel keeps the flock until every process of the run has exited — a
// replacement daemon can't start a second run on the same tree while an
// orphaned run (daemon died mid-command) is still going.
func RunStuckAgent(ctx context.Context, repoPath string, cfg *config.Config, stateDir string, logger *slog.Logger) AgentRunResult {
	agent := cfg.Agent
	var res AgentRunResult

	if !agent.Enabled() {
		return res
	}

	// An un-Parsed config (tests, direct callers) leaves MaxAttempts at 0,
	// which would make the cap below permanently disable the agent; apply the
	// same default config.Parse does.
	maxAttempts := agent.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultAgentMaxAttempts
	}

	recordPath := AgentStatePath(stateDir, repoPath)

	// Serialize the provenance check, the run, and the cleanup under the
	// per-repo agent lock. writeStuck takes the same lock, so a concurrent
	// stuck episode is refused rather than interleaved with this run's
	// cleanup — the flag/record verified here can't be replaced by a fresh
	// episode mid-run. A flock (not a PID/timestamp marker) so no stale-
	// marker liveness heuristics are needed. The fd is shared with the agent
	// command itself (cmd.ExtraFiles below), so a run orphaned by a daemon
	// restart keeps holding the lock until the whole process group exits — a
	// replacement daemon then can't start a second concurrent run on the same
	// repo.
	lock, err := AcquireAgentLock(stateDir, repoPath)
	if err != nil {
		if logger != nil {
			logger.Debug("agent unstick: another agent run is in flight", "repo", repoPath)
		}
		// Report the same fail-closed budget the non-contended path would:
		// an unparseable record must not surface as a fresh (0-attempt) one.
		if att, rerr := readAgentAttempt(recordPath); rerr != nil {
			res.Attempts = maxAttempts
		} else {
			res.Attempts = att.Attempts
		}
		return res
	}
	defer lock.Close()

	// Read the flag and the daemon-issued record under the lock, and verify
	// provenance under the lock: the record must exist, its stuck_at must
	// match the flag's, and the record's (authoritative) reason must be
	// allowed. The repo-local file can be forged by anything that can write
	// to the repo, so only act when the file's stuck_at matches a record the
	// daemon itself wrote, and treat the daemon-observed reason — not the
	// file's — as authoritative for the allowlist. Reading under the lock
	// means a concurrent writeStuck can't replace the flag/record between the
	// check and the cleanup.
	stuck, err := readStuckFile(repoPath)
	if err != nil {
		// No flag or unparsable contents: nothing to recover from.
		if logger != nil {
			logger.Debug("agent unstick: no readable stuck file", "repo", repoPath, "error", err)
		}
		return res
	}

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

	// From here on the agent is engaged with this stuck episode; status may
	// show the attempt budget.
	res.MaxAttempts = maxAttempts

	record, err := readAgentAttempt(recordPath)
	if err != nil {
		// Unparseable record: fail closed — treat the budget as spent rather
		// than re-arming the agent from state we can't trust. A successful
		// sync or `git-tend unstick` removes the record and unwedges the
		// feature; a merely absent record is a fresh budget, not an error.
		if logger != nil {
			logger.Warn("agent unstick: unreadable attempt record, treating budget as spent", "repo", repoPath, "error", err)
		}
		res.Attempts = maxAttempts
		return res
	}
	// The budget is per repo, not per stuck file: a new episode — even one
	// created right after the agent's own exit 0 — does not reset the
	// counter. Rewriting stuck_at can't help an attacker either: the
	// provenance match gates whether the agent runs at all, and the counter
	// only resets on a successful sync or a human unstick.
	res.Attempts = record.Attempts
	if record.Attempts >= maxAttempts {
		// The budget was already spent on a prior run; the daemon's per-tick
		// loop re-enters here every tick, so log at Debug to avoid flooding.
		// The Info line is emitted once, on the run that exhausts the budget
		// (see the failure path below).
		if logger != nil {
			logger.Debug("agent unstick: attempt cap reached, waiting for human", "repo", repoPath,
				"attempts", record.Attempts, "max_attempts", maxAttempts)
		}
		return res
	}

	cmdCtx, cancel := context.WithTimeout(ctx, parseAgentTimeout(agent.Timeout))
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "sh", "-c", agent.OnStuckCmd)
	cmd.Dir = repoPath
	cmd.WaitDelay = 10 * time.Second
	// Run the command in its own process group and kill the whole group on
	// timeout: a `sh -c` wrapper often leaves the real agent running as a
	// child (or grandchild), and killing only the shell would orphan it —
	// still running with daemon privileges.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Hand a read-only dup of the lock fd to the command: the flock lives on
	// the open file description, so the child (and anything it spawns that
	// inherits the fd) keeps holding it even if the daemon dies mid-run.
	// Without this, the kernel would release the lock the instant the daemon
	// died — while the orphaned command group was still working on the tree.
	//
	// The dup is read-only (the lock file is opened O_RDONLY): the command
	// doesn't need write access to the lock file, and a misbehaving command's
	// leftover child then holds a read-only fd — a narrower grant than a
	// writeable one. flock(2) works on any open mode, so a read-only fd still
	// holds LOCK_EX.
	dupFd, err := syscall.Dup(int(lock.Fd()))
	if err != nil {
		res.Err = err
		return res
	}
	lockDup := os.NewFile(uintptr(dupFd), "agent-lock")
	defer lockDup.Close()
	// Keep the parent's copy from leaking to other children the daemon spawns.
	if _, cerr := unix.FcntlInt(uintptr(dupFd), unix.F_SETFD, unix.O_CLOEXEC); cerr != nil && logger != nil {
		logger.Debug("agent unstick: setting CLOEXEC on the lock dup", "repo", repoPath, "error", cerr)
	}
	cmd.ExtraFiles = []*os.File{lockDup}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return os.ErrProcessDone
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		res.Err = err
		return res
	}
	go func() {
		defer stdin.Close()
		if err := writeAgentContext(stdin, repoPath, cfg.SyncBranch, rec, stuck); err != nil {
			fmt.Fprintf(os.Stderr, "agent unstick: writing context: %v\n", err)
		}
	}()

	res.Ran = true
	runErr := cmd.Run()
	record.Attempts++
	record.LastRunAt = time.Now().UTC().Format(time.RFC3339)
	record.Episode = rec.StuckAt
	res.Attempts = record.Attempts

	if runErr == nil {
		// Recovery: clear the flag and the provenance record so the next tick
		// syncs normally. The incremented attempt counter is persisted BEFORE
		// the flag is removed: the budget is cumulative for the repo, so an
		// exit 0 that doesn't actually fix the repo must leave the spent
		// attempt on disk — otherwise every episode re-arms a fresh budget. A
		// successful sync or `git-tend unstick` clears the record instead.
		if err := writeAgentAttempt(recordPath, record); err != nil && logger != nil {
			logger.Error("agent unstick: saving attempt record after recovery", "repo", repoPath, "error", err)
		}
		// Re-verify the flag's stuck_at still matches the verified record
		// before deleting it (compare-and-delete, like the record): a flag
		// that no longer belongs to this episode must not be deleted here.
		// The agent lock refuses a concurrent writeStuck, so the flag can't
		// be replaced mid-run; the stuck_at comparison is defence in depth.
		if err := removeStuckFlagIfCurrent(repoPath, rec.StuckAt); err != nil && !os.IsNotExist(err) {
			res.Err = fmt.Errorf("exit 0 but removing stuck flag: %w", err)
			return res
		}
		removeStuckRecordIfCurrent(stateDir, repoPath, rec.StuckAt)
		res.Recovered = true
		if logger != nil {
			logger.Info("agent unstick: recovered", "repo", repoPath, "reason", rec.Reason)
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
			"attempts", record.Attempts, "max_attempts", maxAttempts)
	}
	// Log the cap at Info only on the run that exhausts the budget (the
	// transition into exhausted). The pre-run cap check above logs Debug on
	// every repeat tick, so the daemon's per-tick loop doesn't flood the log.
	if record.Attempts == maxAttempts && logger != nil {
		logger.Info("agent unstick: attempt cap reached, waiting for human", "repo", repoPath,
			"attempts", record.Attempts, "max_attempts", maxAttempts)
	}
	return res
}

// removeStuckRecordIfCurrent deletes the provenance record only if it still
// belongs to the episode the caller acted on. A concurrent writer recording a
// fresh episode must not have its record deleted here — that would orphan the
// new flag with the agent permanently disabled. Callers hold the agent lock,
// which serializes them against writeStuck; the stuck_at comparison is
// defence in depth.
func removeStuckRecordIfCurrent(stateDir, repoPath, stuckAt string) {
	path := StuckRecordPath(stateDir, repoPath)
	rec, err := readStuckRecord(path)
	if err != nil || rec.StuckAt != stuckAt {
		return
	}
	_ = os.Remove(path)
}

// removeStuckFlagIfCurrent deletes the repo-local stuck flag only if its
// stuck_at still belongs to the episode the caller acted on. A flag that no
// longer matches must not be deleted here — that would swallow a fresh
// episode's flag and orphan its record. Callers hold the agent lock, which
// refuses a concurrent writeStuck, so the flag can't be replaced mid-run; the
// stuck_at comparison is defence in depth.
func removeStuckFlagIfCurrent(repoPath, stuckAt string) error {
	stuck, err := readStuckFile(repoPath)
	if err != nil {
		// Absent flag: already gone, nothing to delete.
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if stuck.StuckAt != stuckAt {
		// The flag no longer belongs to this episode; don't delete it.
		return nil
	}
	return os.Remove(filepath.Join(repoPath, ".gittend.stuck"))
}

// RunStuckAgent also guards on the default so callers passing an un-Parsed
// config (tests, direct callers) don't get a permanently disabled agent
// (MaxAttempts 0 would trip the cap immediately).
const defaultAgentMaxAttempts = config.DefaultAgentMaxAttempts

// maxStuckFileBytes caps how much of the repo-controlled stuck file is read.
const maxStuckFileBytes = 1 << 20

func readStuckFile(repoPath string) (*stuckFile, error) {
	// O_NOFOLLOW: the path is repo-controlled, so a symlink planted there
	// must not turn this into a read from an arbitrary target.
	f, err := os.OpenFile(filepath.Join(repoPath, ".gittend.stuck"), os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// Refuse oversized files outright rather than parsing a truncated prefix:
	// the cap bounds repo-controlled input, so anything bigger is not a stuck
	// file this daemon wrote.
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Size() > maxStuckFileBytes {
		return nil, fmt.Errorf("stuck file is %d bytes, over the %d byte cap", fi.Size(), maxStuckFileBytes)
	}
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

// AgentLockPath returns the state-dir file used as the agent-run lock for a repo.
func AgentLockPath(stateDir, repoPath string) string {
	hash := sha256.Sum256([]byte(repoPath))
	return filepath.Join(stateDir, "agent", hex.EncodeToString(hash[:])+".lock")
}

// AcquireAgentLock takes the per-repo agent-run flock. Callers that mutate
// agent-relevant state (RunStuckAgent, writeStuck, `git-tend unstick`) hold it
// so their record/flag writes serialize against each other. The fd returned is
// read-only (flock(2) needs no write access) and must be passed to the agent
// command via cmd.ExtraFiles so the lock outlives the daemon process (see
// RunStuckAgent).
func AcquireAgentLock(stateDir, repoPath string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Join(stateDir, "agent"), 0755); err != nil {
		return nil, err
	}
	// 0600: flock only requires an open fd, so a world-readable lock file
	// would let any local user LOCK_EX it and block all agent runs. Chmod
	// after open so a lock file created by an older version at 0644 is
	// tightened too.
	//
	// O_RDONLY: flock(2) works on any open mode, so the lock fd doesn't need
	// write access. Keeping it read-only means the fd handed to the agent
	// command (a dup of this one) is read-only too — a misbehaving command's
	// leftover child then holds a read-only fd, a narrower grant than a
	// writeable one.
	lockPath := AgentLockPath(stateDir, repoPath)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDONLY, 0600)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(lockPath, 0600); err != nil {
		f.Close()
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
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
	rec := stuckRecord{
		StuckAt:   stuckAt,
		Reason:    reason,
		WrittenAt: time.Now().UTC().Format(time.RFC3339),
	}
	return writeJSONAtomic(StuckRecordPath(stateDir, repoPath), rec)
}

// agentContext is the structured context document the agent command receives
// on stdin.
type agentContext struct {
	Repo         string `toml:"repo"`
	SyncBranch   string `toml:"sync_branch"`
	StuckAt      string `toml:"stuck_at"`
	Reason       string `toml:"reason"`
	LastCommand  string `toml:"last_command"`
	LastExitCode int    `toml:"last_exit_code"`
	LastOutput   string `toml:"last_output"`
}

// writeAgentContext writes the structured context document the agent command
// receives on stdin: the daemon-observed reason (authoritative for the
// allowlist) plus the stuck file's diagnostic fields, the repo path, and the
// sync branch. The repo-local file is untrusted, so its reason is not used.
// The document is marshaled (not hand-formatted) so repo-influenced output
// containing TOML-hostile bytes cannot forge context fields.
func writeAgentContext(w io.Writer, repoPath, syncBranch string, rec *stuckRecord, stuck *stuckFile) error {
	ctx := agentContext{
		Repo:         repoPath,
		SyncBranch:   syncBranch,
		StuckAt:      rec.StuckAt,
		Reason:       rec.Reason,
		LastCommand:  stuck.LastCommand,
		LastExitCode: stuck.LastExitCode,
		LastOutput:   stuck.LastOutput,
	}
	data, err := toml.Marshal(ctx)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func readAgentAttempt(path string) (agentAttempt, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Absent record: a fresh budget, not an error.
			return agentAttempt{}, nil
		}
		return agentAttempt{}, err
	}
	var a agentAttempt
	if err := json.Unmarshal(data, &a); err != nil {
		return agentAttempt{}, fmt.Errorf("parsing attempt record: %w", err)
	}
	return a, nil
}

// writeJSONAtomic writes JSON state via a temp file + rename so a crash can
// never leave a truncated record behind (a truncated record would fail closed
// and wedge the agent until a human intervenes).
func writeJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	// fsync before rename: without it a power loss can leave the rename
	// durable while the data is not, and a zero-length record fails to parse
	// — which fails closed and wedges the agent until a human intervenes.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func writeAgentAttempt(path string, a agentAttempt) error {
	return writeJSONAtomic(path, a)
}

func parseAgentTimeout(timeout string) time.Duration {
	if d, err := time.ParseDuration(timeout); err == nil && d > 0 {
		return d
	}
	return 10 * time.Minute
}
