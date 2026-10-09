#!/usr/bin/env bash
# `orch`: opens the orchestrator screen in the current iTerm2 tab — agents and
# workers on the top half, orchestrator on the bottom. Run from the project
# checkout. Re-running focuses the existing screen. In any other terminal it runs
# just the orchestrator, without panes.
#   orch            fresh orchestrator (it rehydrates from the state files)
#   orch --resume   resume the previous orchestrator conversation instead
#   orch setup      build, link and check everything (runs by itself when needed)
set -u
plugin=$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/.." && pwd)
if [ "${1:-}" = "setup" ]; then
  exec bash "$plugin/scripts/orch-setup.sh"
fi
if ! command -v orchctl >/dev/null || [ ! -x "$plugin/bin/orchctl" ]; then
  echo "orch: first run — setting up"
  bash "$plugin/scripts/orch-setup.sh" || exit 1
  export PATH="$HOME/.local/bin:$PATH"
fi
"$plugin/bin/orchctl" update-check
if [ "${TERM_PROGRAM:-}" = "iTerm.app" ]; then
  exec bash "$plugin/scripts/orch-iterm.sh" screen "$@"
fi
# Any other terminal: the orchestrator runs right here, without worker panes.
s="$("$plugin/bin/orchctl" name)-orch"
echo "orch: worker panes need iTerm2 — run \`claude agents\` in another tab to watch workers"
# claude clears the screen on start; give the note time to be read.
read -r -t 10 -p "press Enter to start (starts by itself in 10s) " </dev/tty || echo
live=$(claude agents --json 2>/dev/null | jq -r --arg n "$s" \
  '[.[] | select(.kind=="background" and .name==$n and .pid != null)] | sort_by(.startedAt) | last | .id // empty')
[ -n "$live" ] && exec claude attach "$live"
flags=$("$plugin/bin/orchctl" flags orch) || exit 1
if [ "${1:-}" = "--resume" ]; then eval "exec claude --resume '$s' --name '$s' $flags"
else eval "exec claude --name '$s' $flags /orch:orch"; fi
