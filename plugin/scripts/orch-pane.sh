#!/usr/bin/env bash
# Shows a worker in the top half of the orchestrator screen (no-op outside it).
#   orch-pane.sh <session-name>
set -u
plugin=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
exec bash "$plugin/scripts/orch-iterm.sh" pane "$1"
