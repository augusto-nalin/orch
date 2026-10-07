#!/usr/bin/env bash
# Stops a finished worker and closes its pane. The transcript stays, so
# `claude --resume <name> --bg --name <name> "…"` reopens it with full context.
#   orch-close.sh <session-name>
set -u
plugin=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
orch_bin="$plugin/bin/orch"
name="$1"
project=$("$orch_bin" name)
s="$project-orch"

ids=$(claude agents --json 2>/dev/null | jq -r --arg n "$name" \
  '.[] | select(.kind=="background" and .name==$n and .pid != null) | .id')
for id in $ids; do claude stop "$id" >/dev/null && echo "stopped $name ($id)"; done
[ -n "$ids" ] || echo "$name was not running"

bash "$plugin/scripts/orch-iterm.sh" close "$name" 2>/dev/null
if tmux has-session -t "=$s" 2>/dev/null; then
  tmux list-panes -t "$s:screen" -F '#{pane_title} #{pane_id}' |
    awk -v n="$name" '{id=$NF; $NF=""; sub(/ $/,""); if ($0==n) print id}' |
    while read -r p; do tmux kill-pane -t "$p" && echo "closed pane $p"; done
fi
