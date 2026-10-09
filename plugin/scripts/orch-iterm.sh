#!/usr/bin/env bash
# Native iTerm2 screen for the orchestrator, in the tab you run it from:
# agents + workers on the top half, orchestrator on the bottom.
#   orch-iterm.sh screen [--resume]   take over the current tab
#   orch-iterm.sh pane <name>         show a running worker in the top row
#   orch-iterm.sh close <name>        close a worker's pane
# Pane ids live in <state dir>/screen ("agents <id>", "orch <id>", "worker <name> <id>").
set -u
plugin=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
orch_bin="$plugin/bin/orchctl"
dir=$("$orch_bin" dir)
project=$("$orch_bin" name)
s="$project-orch"
screen="$dir/screen"

# Runs AppleScript `body` against the iTerm2 session with unique id $1 (bound to `sess`).
on_session() {
  osascript - "$1" <<EOF
on run argv
  tell application "iTerm2"
    repeat with w in windows
      repeat with t in tabs of w
        repeat with sess in sessions of t
          if unique id of sess is (item 1 of argv) then
$2
          end if
        end repeat
      end repeat
    end repeat
  end tell
  return ""
end run
EOF
}
alive() { [ -n "$1" ] && [ "$(on_session "$1" 'return "yes"')" = "yes" ]; }
# Pane exists AND still runs claude (an exited `claude attach` leaves a bare shell behind).
running() {
  alive "$1" || return 1
  local tty; tty=$(on_session "$1" '            return tty of sess')
  [ -n "$tty" ] && ps -o command= -t "${tty#/dev/}" 2>/dev/null | grep -qE '(^|/)claude( |$)'
}
get() { awk -v k="$1" -v n="${2:-}" '$1==k && (n=="" || $2==n) {print $NF}' "$screen" 2>/dev/null | tail -1; }
running_id() {
  claude agents --json 2>/dev/null | jq -r --arg n "$1" \
    '[.[] | select(.kind=="background" and .name==$n and .pid != null)] | sort_by(.startedAt) | last | .id // empty'
}
# Splits session $1 (horizontally = new pane below, vertically = to the right), types $3 into it.
split() {
  on_session "$1" "            tell sess to set p to (split $2 with default profile)
            tell p to write text \"$3\"
            return unique id of p"
}

case "${1:-}" in
screen)
  orch=$(get orch)
  if running "$orch"; then on_session "$orch" '            select sess'; exit 0; fi
  here="${ITERM_SESSION_ID#*:}"
  [ -n "$here" ] || { echo "not inside iTerm2"; exit 1; }
  live=$(running_id "$s")
  if [ -n "$live" ]; then cmd="claude attach $live"
  elif [ "${2:-}" = "--resume" ]; then cmd="claude --resume $s --name $s --plugin-dir $plugin --settings $plugin/orch-settings.json"
  else cmd="claude --name $s --plugin-dir $plugin --settings $plugin/orch-settings.json /orch:orch"; fi
  orch=$(split "$here" horizontally "cd '$PWD' && $cmd")
  mkdir -p "$dir"             # first run in a project: the broker hasn't made it yet
  printf 'agents %s\norch %s\n' "$here" "$orch" > "$screen"
  # Workers that are already running.
  claude agents --json 2>/dev/null | jq -r --arg p "$project-" --arg s "$s" \
    '.[] | select(.kind=="background" and .pid != null and (.name|startswith($p)) and .name != $s) | .name' |
    sort -u | while read -r w; do "$0" pane "$w" >/dev/null; done
  on_session "$orch" '            select sess' >/dev/null
  exec claude agents          # this pane becomes the agent overview
  ;;
pane)
  name="$2"
  [ -s "$screen" ] && alive "$(get orch)" || { echo "no orchestrator screen — skipped"; exit 0; }
  running "$(get worker "$name")" && { echo "already shown"; exit 0; }
  id=$(running_id "$name"); [ -n "$id" ] || { echo "no running background session named $name"; exit 1; }
  # Split the widest top-row pane, so the row stays roughly even.
  best=""; bestw=0
  for p in $(get agents) $(awk '$1=="worker"{print $3}' "$screen"); do
    w=$(on_session "$p" '            return columns of sess')
    [ -n "$w" ] && [ "$w" -gt "$bestw" ] && { best="$p"; bestw="$w"; }
  done
  [ -n "$best" ] || { echo "no top-row pane left"; exit 1; }
  new=$(split "$best" vertically "claude attach $id")
  grep -v "^worker $name " "$screen" > "$screen.tmp"; mv "$screen.tmp" "$screen"
  echo "worker $name $new" >> "$screen"
  echo "shown $name ($id)"
  ;;
close)
  name="$2"; p=$(get worker "$name")
  [ -n "$p" ] && on_session "$p" '            close sess
            return "closed"' >/dev/null && echo "closed pane of $name"
  grep -v "^worker $name " "$screen" > "$screen.tmp" 2>/dev/null; mv "$screen.tmp" "$screen" 2>/dev/null
  ;;
*) sed -n '2,8p' "$0"; exit 1 ;;
esac
