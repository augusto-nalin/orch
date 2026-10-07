#!/usr/bin/env bash
# `orch setup`: makes orch ready to use. Safe to re-run; prints one line per check.
#   - builds bin/orchctl (Go) when missing or older than the sources
#   - links orch and orchctl into ~/.local/bin
#   - checks PATH, a shadowing `orch` alias, iTerm2, jq, claude
#   - moves a legacy ~/.claude/orch to the state home (symlink left behind)
#   - imports the current project's old board.md/questions.md into state.json
set -u
plugin=$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/.." && pwd)
repo=$(dirname "$plugin")
bin="$plugin/bin/orchctl"
links="$HOME/.local/bin"
state_home="${ORCH_HOME:-${XDG_STATE_HOME:-$HOME/.local/state}/orch}"
fail=0
ok()   { echo "ok    $*"; }
did()  { echo "done  $*"; }
warn() { echo "warn  $*"; }
err()  { echo "error $*"; fail=1; }

# Build when the binary is missing or any Go source is newer.
if [ ! -x "$bin" ] || [ -n "$(find "$repo/cmd" "$repo/internal" "$repo/go.mod" -newer "$bin" -name '*.go' -o -newer "$bin" -name go.mod 2>/dev/null | head -1)" ]; then
  if ! command -v go >/dev/null; then
    err "go not found — install it (brew install go), then re-run: orch setup"
  elif (cd "$repo" && go build -o "$bin" ./cmd/orchctl); then
    did "built $bin"
  else
    err "build failed (cd $repo && go build ./cmd/orchctl)"
  fi
else
  ok "orchctl up to date"
fi

mkdir -p "$links"
for pair in "orchctl:$bin" "orch:$plugin/scripts/orch-screen.sh"; do
  name=${pair%%:*}; target=${pair#*:}
  if [ "$(readlink "$links/$name" 2>/dev/null)" = "$target" ]; then
    ok "$links/$name"
  else
    ln -sf "$target" "$links/$name" && did "linked $links/$name → $target"
  fi
done

case ":$PATH:" in
  *":$links:"*) ok "$links on PATH" ;;
  *) warn "$links not on PATH — add to your shell rc: export PATH=\"\$HOME/.local/bin:\$PATH\"" ;;
esac

# An alias or function named orch in the user's shell hides the command.
shell=$(basename "${SHELL:-zsh}")
shadow=$("$shell" -ic 'alias orch 2>/dev/null || type orch 2>/dev/null | grep -i function' 2>/dev/null </dev/null | LC_ALL=C sed $'s/\033\\[[0-9;?]*[A-Za-z]//g' | head -1)
if [ -n "$shadow" ]; then
  warn "your $shell has $shadow — it hides $links/orch; remove it from your shell rc"
else
  ok "no orch alias"
fi

[ -d /Applications/iTerm.app ] && ok "iTerm2" || err "iTerm2 not found — orch needs it (brew install --cask iterm2)"
command -v jq >/dev/null && ok "jq" || err "jq not found (brew install jq)"
command -v claude >/dev/null && ok "claude" || err "claude not found"

# Legacy state under ~/.claude (the Bash sandbox can't write there).
legacy="$HOME/.claude/orch"
if [ -d "$legacy" ] && [ ! -L "$legacy" ]; then
  if [ -e "$state_home" ]; then
    warn "both $legacy and $state_home exist — merge them by hand"
  else
    mkdir -p "$(dirname "$state_home")" && mv "$legacy" "$state_home" && ln -s "$state_home" "$legacy" &&
      did "moved $legacy → $state_home (symlink left)"
  fi
fi

# Old markdown state for the current project → state.json.
if [ -x "$bin" ]; then
  dir=$("$bin" dir)
  if [ -f "$dir/state.json" ]; then
    ok "state $dir"
  elif [ -f "$dir/board.md" ]; then
    out=$("$bin" import) && did "$out ($dir)" || err "import failed in $dir"
  fi
fi

exit $fail
