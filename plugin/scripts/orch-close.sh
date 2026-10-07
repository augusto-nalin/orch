#!/usr/bin/env bash
# Stops a finished worker and closes its pane. The transcript stays, so
# `claude --resume <name> --bg --name <name> "…"` reopens it with full context.
#   orch-close.sh <session-name>
set -u
plugin=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
name="$1"

ids=$(claude agents --json 2>/dev/null | jq -r --arg n "$name" \
  '.[] | select(.kind=="background" and .name==$n and .pid != null) | .id')
for id in $ids; do claude stop "$id" >/dev/null && echo "stopped $name ($id)"; done
[ -n "$ids" ] || echo "$name was not running"

bash "$plugin/scripts/orch-iterm.sh" close "$name"
