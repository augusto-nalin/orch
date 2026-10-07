#!/usr/bin/env bash
# Shows a worker in the top half of the orchestrator screen (no-op outside it).
#   orch-pane.sh <session-name>
set -u
plugin=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
orch_bin="$plugin/bin/orch"
name="$1"
project=$("$orch_bin" name)
s="$project-orch"
[ -s "$("$orch_bin" dir)/screen" ] && exec bash "$plugin/scripts/orch-iterm.sh" pane "$name"
tmux has-session -t "=$s" 2>/dev/null || { echo "no orchestrator screen ($s) — skipped"; exit 0; }

# Already showing? Leave it.
tmux list-panes -t "$s:screen" -F '#{pane_title}' | grep -qxF "$name" && { echo "already shown"; exit 0; }

id=$(claude agents --json 2>/dev/null | jq -r --arg n "$name" \
  '[.[] | select(.kind=="background" and .name==$n and .pid != null)] | sort_by(.startedAt) | last | .id // empty')
[ -n "$id" ] || { echo "no running background session named $name"; exit 1; }

# Split off the newest top pane so workers line up left to right; hooks re-apply the layout.
top=$(tmux list-panes -t "$s:screen" -F '#{pane_top} #{pane_left} #{pane_id}' | awk '$1==0 && $2>=m{m=$2; p=$3} END{print p}')
pane=$(tmux split-window -d -h -t "$top" -P -F '#{pane_id}' "claude attach $id")
tmux select-pane -t "$pane" -T "$name"
echo "shown $name ($id) in $pane"
