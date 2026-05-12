# --- git-tend: background repo auto-sync daemon ---
if [ -d "$HOME/Code/git-tend" ]; then
    echo "Building git-tend..."
    cd "$HOME/Code/git-tend" && go build -o git-tend ./cmd/git-tend

    mkdir -p "$HOME/.local/bin"
    ln -sf "$HOME/Code/git-tend/git-tend" "$HOME/.local/bin/git-tend"

    echo "Installing git-tend daemon..."
    "$HOME/.local/bin/git-tend" install
else
    echo "git-tend not found at $HOME/Code/git-tend — skipping install"
fi
# --- end git-tend ---
