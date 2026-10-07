#!/usr/bin/env bash
# `orch`: opens the orchestrator screen in the current iTerm2 tab — agents and
# workers on the top half, orchestrator on the bottom. Run from the project
# checkout. Re-running focuses the existing screen.
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
if [ "${TERM_PROGRAM:-}" != "iTerm.app" ]; then
  echo "orch needs iTerm2 (TERM_PROGRAM=${TERM_PROGRAM:-unset})" >&2
  exit 1
fi
exec bash "$plugin/scripts/orch-iterm.sh" screen "$@"
