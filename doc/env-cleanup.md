# .env cleanup

Once the git-tend daemon is running, remove this line from `~/.dotfiles/system/.env`:

    git -C "$HOME/.botfiles" pull --quiet & disown

The daemon handles `.botfiles` sync via its `.gittend` config (read-only mode).
