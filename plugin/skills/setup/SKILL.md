---
name: setup
description: Make orch ready on this machine — put the `orch` and `orchctl` commands on PATH and check jq, claude and (macOS) iTerm2. Trigger on /orch:setup, after installing or updating the orch plugin, or when `orch`/`orchctl` is not found.
---

# orch setup

Run the setup script two directories above this skill's base directory:

`bash <base directory>/../../scripts/orch-setup.sh`

Safe to re-run; it prints one line per check (`ok`, `done`, `warn`, `error`). Show
the user the `warn` and `error` lines with the fix each one names, in plain English.

Then tell the user how to start: `cd` into a project checkout, run `orch` — in iTerm2 on macOS for worker panes; any other terminal (Linux, Windows Git Bash) runs just the orchestrator.
After a plugin update, run /orch:setup again.
