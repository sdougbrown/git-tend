# git-tend 🐌

You work across a handful of git repos — notes, dotfiles, a side project, maybe a shared config directory. You want them to stay in sync with their remotes without thinking about it. You don't want to open a separate app or remember to run a script.

git-tend is your git gardener: a background daemon that tends your repos while you work on other things. It watches a configured set of directory roots, finds repos that have opted in with a `.gittend` file, and syncs them on a regular interval. On macOS it runs as a launchd agent; on Linux as a systemd user service. When something goes wrong — a merge conflict, a push rejection, a commit hook failure — it stops touching that repo and tells you.

It does not touch repos you haven't opted in. It does not force-push or rewrite history. It does not run unless the daemon is running.

---

## What it does, concretely

For each opted-in repo, the daemon can operate in one of two modes:

**read-only** — pulls changes from the remote on every tick. If a pull would conflict, it aborts and marks the repo stuck.

**read-write** — stages your working tree, generates a commit message from the diff, does a `git pull --rebase`, then pushes. If any step fails (commit hook, rebase conflict, push rejection), it stops and marks the repo stuck.

A repo is marked stuck by writing a `.gittend.stuck` file in the repo root. While that file exists, the daemon skips the repo entirely and won't make things worse. You resolve the problem manually, then run `git-tend unstick <path>` to clear the flag.

The daemon also tracks an "offline" state separately from "stuck." If a fetch or push fails with a network error, the repo backs off exponentially (capping at `offline_backoff_cap`, default 30 minutes) rather than hammering the remote.

---

## Install

**Prerequisites:** macOS or Linux. The binary is statically compiled — no runtime dependencies.

Download a release binary from the [releases page](https://github.com/sdougbrown/git-tend/releases/latest) and put it on your `PATH`. Replace `<os>` with `darwin` or `linux` and `<arch>` with `arm64` or `amd64`:

```sh
mkdir -p ~/.local/bin
curl -L -o ~/.local/bin/git-tend \
  https://github.com/sdougbrown/git-tend/releases/latest/download/git-tend_<os>_<arch>
chmod +x ~/.local/bin/git-tend
```

Verify with `git-tend --version`.

Or build from source (requires Go 1.22 or later):

```sh
git clone https://github.com/sdougbrown/git-tend
cd git-tend
go build -o git-tend ./cmd/git-tend
mkdir -p ~/.local/bin
ln -sf "$(pwd)/git-tend" ~/.local/bin/git-tend
```

Make sure `~/.local/bin` is on your `PATH`, then install the daemon service:

```sh
git-tend install
```

On macOS this writes a launchd plist to `~/Library/LaunchAgents/com.dougthings.gittend.plist` and loads it. On Linux it writes a systemd user unit to `~/.config/systemd/user/git-tend.service` and enables it.

If `~/.config/git-tend/config.toml` doesn't exist, `install` writes a commented template. When stdin is a TTY it first prompts for which directories to scan (default `~/Code`, comma-separated for multiple). When run non-interactively (CI, piped install) it skips the prompt and writes the default. Either way the config path is printed so you can edit it.

The binary is expected at `~/.local/bin/git-tend`. If you put it somewhere else, edit the generated service file before loading.

To remove the service:

```sh
git-tend uninstall
```

---

## Getting started

**Step 1: Create a `.gittend` file in a repo you want to manage.**

For a notes or dotfiles repo where you just want automatic pull:

```toml
mode = "read-only"
sync_branch = "main"
```

For a repo where you want automatic commit and push:

```toml
mode = "read-write"
sync_branch = "main"

[commit]
emoji = "🐌"
```

The `.gittend` file must be present for the daemon to touch the repo. Without it, the repo is ignored entirely.

**Step 2: Check that the daemon found it.**

```sh
git-tend status
```

```
REPO                                     MODE STATE    LAST SYNC    AHEAD      BEHIND
------------------------------------------------------------------------------------------
/Users/you/Code/notes                    ro   ok       12s ago      0          0
/Users/you/.dotfiles                     ro   ok       12s ago      0          0
```

State is `pending`, `ok`, `stuck`, `offline`, or `snoozed`. `pending` means the daemon has discovered the repo but hasn't synced it yet (you'll see this briefly after `git-tend install`, `restart`, or `reload`). Mode is `ro` (read-only) or `rw` (read-write).

**Step 3: If a repo goes stuck, see why.**

The daemon writes a `.gittend.stuck` file in the repo root when it gives up. Read it:

```sh
cat /path/to/repo/.gittend.stuck
```

It contains the reason, the last command that failed, the exit code, and the last output. Fix whatever went wrong (resolve the conflict, fix the commit hook), then re-enable:

```sh
git-tend unstick /path/to/repo
```

---

## Shell integration (optional)

**Prompt segment** — shows a warning in your prompt when any managed repo is stuck or the daemon has gone quiet:

```sh
git-tend install --shell-prompt
```

This adds a snippet to your shell rc file (`.zshrc`, `.bashrc`, or `config.fish` — detected from `$SHELL`). The segment is empty when everything is clean. When there's something to report, it prints `⚠ tend:2` (number of stuck repos) or `⚠ tend:down` if the daemon hasn't ticked recently.

**Daily greet** — prints a one-time summary when you open your first shell of the day, if any repos are stuck:

```sh
git-tend install --shell-greet
```

Repos that have been stuck for 3 or more days are highlighted separately. The threshold is controlled by `escalate_after_days` in the global config.

To remove either snippet:

```sh
git-tend uninstall --shell-prompt
git-tend uninstall --shell-greet
```

Pass `--shell zsh` (or `bash` or `fish`) to override auto-detection. Use `--dry-run` to preview what would be written before committing.

---

## Configuration

### Global config (`~/.config/git-tend/config.toml`)

Controls which directories the daemon scans and how it behaves overall. Created on first `git-tend install` (with a prompt for roots when run interactively) or on first daemon start. The default roots are intentionally minimal — `~/Code` — to avoid scanning unexpected parts of your home directory; add the directories that match your layout.

```toml
# Directories to scan for opted-in repos. Tilde-expanded.
roots = ["~/Code"]

# How often to sync each repo.
interval = "60s"

# Log verbosity: debug, info, warn, error.
log_level = "info"

# How many days a repo can be stuck before the greet command highlights it urgently.
escalate_after_days = 3

# Timeout for network operations (fetch, push).
network_timeout = "30s"

# Maximum backoff interval for repos that keep failing to reach the remote.
offline_backoff_cap = "30m"

# How many directory levels deep to search inside each root.
scan_depth = 4
```

The daemon picks up changes to this file on `SIGHUP` — either send the signal directly or run `git-tend reload`.

### Per-repo config (`.gittend` in repo root)

```toml
# Required. "read-only" or "read-write".
mode = "read-only"

# Branch to sync. Daemon skips the repo if you're on a different branch.
sync_branch = "main"

# Per-repo sync interval. Overrides the global interval for this repo's timeout.
interval = "60s"

# read-write only: minimum quiet time before committing. Prevents committing
# mid-edit if you save frequently.
debounce = "30s"

[commit]
# Prefix emoji on generated commit messages. Default: 🐌
emoji = "🐌"

# Commit strategy. Currently only the default (heuristic) strategy is used.
strategy = ""

# Optional: shell command that receives a full git diff on stdin and prints
# a commit message on stdout. Used as a fallback only when the heuristic
# produces a low-information message like "sync 14 files".
model_cmd = "llm -m gpt-4o-mini 'write a short git commit message for this diff'"

# Timeout for the model command. Default: 30s.
model_timeout = "30s"

# Number of directories that triggers fallback to model_cmd.
model_fallback_threshold = 0

# Pass --no-verify to git commit. Use carefully.
no_verify = false

[include]
# Stage only these paths (relative to repo root). If empty, stages everything.
paths = ["src/", "config/"]

[exclude]
# Exclude these paths from staging. Uses git pathspec syntax.
paths = ["secrets/", "*.log"]

[notify]
# Send a system notification (macOS: osascript, Linux: notify-send) when
# a repo transitions to stuck or recovers.
on_stuck = false
on_recovered = false
```

---

## Commands

| Command | Description |
|---|---|
| `git-tend status` | Show last-sync status for all managed repos |
| `git-tend run <path>` | Run one sync cycle for a single repo manually |
| `git-tend unstick <path>` | Remove the `.gittend.stuck` flag and re-enable the repo |
| `git-tend snooze <path> [duration]` | Suppress stuck surfacing for a repo; default duration is 24h |
| `git-tend reload` | Send SIGHUP to the running daemon to reload config and rescan roots |
| `git-tend restart` | Stop the daemon so the service manager respawns it (picks up a rebuilt binary) |
| `git-tend logs` | Print the daemon log |
| `git-tend logs -f` | Tail the daemon log |
| `git-tend install` | Install and start the launchd/systemd service |
| `git-tend uninstall` | Stop and remove the service |
| `git-tend install --shell-prompt` | Add a prompt segment to your shell rc |
| `git-tend install --shell-greet` | Add a daily stuck-repo summary to your shell rc |
| `git-tend greet` | Print the daily summary directly (once per day) |
| `git-tend prompt` | Print the prompt segment directly |
| `git-tend daemon` | Run the daemon in the foreground (used by the service; not normally called directly) |

`git-tend run` requires a `.gittend` file in the target repo. `git-tend unstick` validates that the path is a git repo before removing the flag.

`git-tend snooze` writes to a `snoozes.json` file in the state directory. A snoozed repo shows as `snoozed` in `status` output and is skipped by the daemon until the duration expires. It does not remove the snooze from the file automatically — the state will simply be ignored once the timestamp passes.

---

## What it does not do

- It does not manage repos that don't have a `.gittend` file. Your repos are opt-in.
- It does not rewrite history or force-push.
- It does not merge. In read-write mode it rebases (`git pull --rebase`) and stops if there's a conflict.
- It does not support Windows.
- It does not watch the filesystem for changes. It polls on the configured interval.
- It does not authenticate for you. If your remote requires credentials that aren't already available (SSH key, credential helper), you'll see network errors.
- It does not commit from a non-sync branch. If you've switched to a feature branch, the daemon skips that repo silently until you're back on `sync_branch`.
