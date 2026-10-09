#!/usr/bin/env bash
# `orch setup` (also /orch:setup, `make install`): makes orch ready to use. Safe to
# re-run; prints one line per check.
#   - in a clone: builds bin/orchctl-dev (Go) when missing or older than the sources;
#     otherwise bin/orchctl downloads the signed release binary on first use
#   - links orch and orchctl into ~/.local/bin (an installed plugin gets small
#     wrappers instead, since its versioned path changes on every update)
#   - checks PATH, a shadowing `orch` alias, iTerm2 (macOS, for worker panes), jq, claude
set -u
plugin=$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/.." && pwd)
repo=$(dirname "$plugin")
bin="$plugin/bin/orchctl"
os=$(uname -s)
case "$os" in MINGW* | MSYS* | CYGWIN*) windows=1 ext=.exe ;; *) windows="" ext="" ;; esac
dev="$plugin/bin/orchctl-dev$ext"
links="$HOME/.local/bin"
fail=0
ok()   { echo "ok    $*"; }
did()  { echo "done  $*"; }
warn() { echo "warn  $*"; }
err()  { echo "error $*"; fail=1; }

# A clone builds orchctl-dev when it's missing or any Go source is newer; the
# bin/orchctl shim hands over to it. Without it (an installed plugin, or no go) the
# shim downloads the signed release binary for this version.
if [ -f "$repo/go.mod" ]; then
  if [ ! -x "$dev" ] || [ -n "$(find "$repo/cmd" "$repo/internal" "$repo/go.mod" -newer "$dev" -name '*.go' -o -newer "$dev" -name go.mod 2>/dev/null | head -1)" ]; then
    if ! command -v go >/dev/null; then
      warn "go not found — using the release orchctl, not your sources"
    elif (cd "$repo" && go build -o "$dev" ./cmd/orchctl); then
      did "built $dev"
    else
      err "build failed (cd $repo && go build ./cmd/orchctl)"
    fi
  else
    ok "orchctl-dev up to date"
  fi
fi
if v=$("$bin" version); then ok "orchctl $v"; else err "orchctl not runnable (see above)"; fi

mkdir -p "$links"
# Windows (Git Bash) has no real symlinks, so a clone gets wrappers there too.
case "$plugin${windows:+/plugins/cache/}" in
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

case "$os" in
Darwin)
  jq_hint="brew install jq"
  [ -d /Applications/iTerm.app ] && ok "iTerm2" ||
    warn "iTerm2 not found — orch runs without worker panes (brew install --cask iterm2)" ;;
Linux) jq_hint="install it with your package manager" ;;
*) jq_hint="winget install jqlang.jq" ;;
esac
command -v jq >/dev/null && ok "jq" || err "jq not found ($jq_hint)"
if ! command -v claude >/dev/null; then err "claude not found"
elif claude agents --json >/dev/null 2>&1; then ok "claude $(claude --version 2>/dev/null | cut -d' ' -f1)"
else err "claude too old — orch needs background sessions (claude agents, --bg); run: claude update"; fi

exit $fail
