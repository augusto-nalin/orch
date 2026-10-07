#!/usr/bin/env bash
# Opens the orchestrator screen in tmux: workers on the top half, orchestrator on the bottom.
# Run from the project checkout. Re-running re-attaches to the existing screen.
#   orch-screen.sh            fresh orchestrator (it rehydrates from the state files)
#   orch-screen.sh --resume   resume the previous orchestrator conversation instead
set -u
plugin=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
orch_bin="$plugin/bin/orch"
project=$("$orch_bin" name)
s="$project-orch"
# iTerm2 (outside tmux): native panes in the current tab instead of tmux.
if [ "${TERM_PROGRAM:-}" = "iTerm.app" ] && [ -z "${TMUX:-}" ]; then
  exec bash "$plugin/scripts/orch-iterm.sh" screen "$@"
fi

attach() {
  if [ -n "${TMUX:-}" ]; then tmux switch-client -t "=$s"
  else tmux attach -t "=$s"; fi
}
tmux has-session -t "=$s" 2>/dev/null && { attach; exit; }

# A running orchestrator (e.g. a background session) is attached, never duplicated.
live=$(claude agents --json 2>/dev/null | jq -r --arg n "$s" \
  '[.[] | select(.kind=="background" and .name==$n and .pid != null)] | sort_by(.startedAt) | last | .id // empty')
if [ -n "$live" ]; then
  orch_cmd="claude attach $live"
elif [ "${1:-}" = "--resume" ]; then
  orch_cmd="claude --resume $s --name $s --plugin-dir $plugin --settings $plugin/orch-settings.json"
else
  orch_cmd="claude --name $s --plugin-dir $plugin --settings $plugin/orch-settings.json /orch:orch"
fi

# First pane = orchestrator: main-horizontal-mirrored keeps the first pane at the bottom.
tmux new-session -d -s "$s" -n screen -c "$PWD" -x "$(tput cols)" -y "$(tput lines)" "$orch_cmd"
tmux set -t "$s" -w main-pane-height 50%
tmux set -t "$s" -w pane-border-status top
tmux set -t "$s" -w allow-set-title off
tmux set -t "$s" -w pane-border-format ' #{pane_title} '
tmux select-pane -t "$s" -T "orchestrator"
# Top half starts with the agent overview; worker panes are added next to it.
tmux split-window -t "$s" -c "$PWD" "claude agents"
tmux select-pane -t "$s" -T "agents"
tmux select-layout -t "$s" main-horizontal-mirrored
# Keep the layout when panes come and go or the terminal resizes.
for h in after-split-window pane-exited client-resized; do
  tmux set-hook -t "$s" "$h" "select-layout main-horizontal-mirrored"
done
# Show workers that are already running.
claude agents --json 2>/dev/null | jq -r --arg p "$project-" --arg s "$s" \
  '.[] | select(.kind=="background" and .pid != null and (.name|startswith($p)) and .name != $s) | .name' |
  sort -u | while read -r w; do bash "$plugin/scripts/orch-pane.sh" "$w" >/dev/null; done
tmux select-pane -t "$s:screen.{bottom}"
attach
