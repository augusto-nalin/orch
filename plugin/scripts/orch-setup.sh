#!/usr/bin/env bash
# `orch setup` (also /orch:setup, `make install`): makes orch ready to use. Safe to
# re-run; prints one line per check.
#   - in a clone: builds bin/orchctl-dev (Go) when missing or older than the sources
#   - links orch and orchctl into ~/.local/bin (an installed plugin gets small
#     wrappers instead, since its versioned path changes on every update)
#   - checks PATH, a shadowing `orch` alias, iTerm2, jq, claude
#   - moves a legacy ~/.claude/orch to the state home (symlink left behind)
#   - imports the current project's old board.md/questions.md into state.json
set -u
plugin=$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/.." && pwd)
repo=$(dirname "$plugin")
bin="$plugin/bin/orchctl"
dev="$plugin/bin/orchctl-dev"
links="$HOME/.local/bin"
state_home="${ORCH_HOME:-${XDG_STATE_HOME:-$HOME/.local/state}/orch}"
fail=0
ok()   { echo "ok    $*"; }
did()  { echo "done  $*"; }
warn() { echo "warn  $*"; }
err()  { echo "error $*"; fail=1; }

# A clone builds orchctl-dev when it's missing or any Go source is newer; the
# release binary (bin/orchctl) hands over to it. An installed plugin ships only the
# release binary.
if [ -f "$repo/go.mod" ]; then
  if [ ! -x "$dev" ] || [ -n "$(find "$repo/cmd" "$repo/internal" "$repo/go.mod" -newer "$dev" -name '*.go' -o -newer "$dev" -name go.mod 2>/dev/null | head -1)" ]; then
    if ! command -v go >/dev/null; then
      [ -x "$bin" ] && warn "go not found — using the release orchctl, not your sources" ||
        err "go not found — install it (brew install go), then re-run: orch setup"
    elif (cd "$repo" && go build -o "$dev" ./cmd/orchctl); then
      did "built $dev"
    else
      err "build failed (cd $repo && go build ./cmd/orchctl)"
    fi
  else
    ok "orchctl-dev up to date"
  fi
  [ -e "$bin" ] || { [ -x "$dev" ] && cp "$dev" "$bin" && did "no release orchctl yet — copied the dev build"; }
fi
[ -x "$bin" ] && ok "orchctl $("$bin" version)" || err "$bin missing — reinstall the plugin"

mkdir -p "$links"
case "$plugin" in
*/plugins/cache/*)
  # Wrappers find the current install in Claude's plugin registry, falling back to this one.
  reg="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/plugins/installed_plugins.json"
  for pair in "orchctl:bin/orchctl" "orch:scripts/orch-screen.sh"; do
    name=${pair%%:*}; rel=${pair#*:}
    body="#!/usr/bin/env bash
# written by orch setup: runs the installed orch plugin's current version
p=\$(jq -r '[.plugins | to_entries[] | select(.key | startswith(\"orch@\")) | .value[].installPath] | last // empty' '$reg' 2>/dev/null)
[ -d \"\$p\" ] || p='$plugin'
exec \"\$p/$rel\" \"\$@\""
    if [ ! -L "$links/$name" ] && [ "$(cat "$links/$name" 2>/dev/null)" = "$body" ]; then
      ok "$links/$name"
    else
      rm -f "$links/$name" && printf '%s\n' "$body" > "$links/$name" && chmod +x "$links/$name" && did "wrote $links/$name"
    fi
  done
  ;;
*)
  for pair in "orchctl:$bin" "orch:$plugin/scripts/orch-screen.sh"; do
    name=${pair%%:*}; target=${pair#*:}
    if [ "$(readlink "$links/$name" 2>/dev/null)" = "$target" ]; then
      ok "$links/$name"
    else
      ln -sf "$target" "$links/$name" && did "linked $links/$name → $target"
    fi
  done
  ;;
esac

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
if ! command -v claude >/dev/null; then err "claude not found"
elif claude agents --json >/dev/null 2>&1; then ok "claude $(claude --version 2>/dev/null | cut -d' ' -f1)"
else err "claude too old — orch needs background sessions (claude agents, --bg); run: claude update"; fi

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
