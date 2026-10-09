# orch

orch sits between full autonomy and a single chat. You talk to one orchestrator; each issue gets its own Claude Code worker that does the work in the background and comes back to you for decisions and commits.

## Install

In Claude Code, one command at a time:

1. Add the marketplace: `/plugin marketplace add augusto-nalin/orch`
   (or, in the `/plugin` → Add Marketplace dialog, enter just `augusto-nalin/orch`)
2. Install the plugin: `/plugin install orch@orch`
3. Set up the `orch` and `orchctl` commands: `/orch:setup`

Then `cd` into a project checkout and run `orch` (in iTerm2 on macOS for worker panes).

## Build

Needs Go. From a checkout:

```
make build    # dev build at plugin/bin/orchctl-dev; the plugin's orchctl shim uses it
```
