---
name: setup
description: Make orch ready on this machine — put the `orch` and `orchctl` commands on PATH and check iTerm2, jq and claude. Trigger on /orch:setup, after installing or updating the orch plugin, or when `orch`/`orchctl` is not found.
---

# orch setup

Run the setup script two directories above this skill's base directory:

`bash <base directory>/../../scripts/orch-setup.sh`

Safe to re-run; it prints one line per check (`ok`, `done`, `warn`, `error`). Show
the user the `warn` and `error` lines with the fix each one names, in plain English.

Then tell the user how to start: open iTerm2, `cd` into a project checkout, run `orch`.
After a plugin update, run /orch:setup again.
