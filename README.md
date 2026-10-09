# orch

orch sits between full autonomy and a single chat. You talk to one orchestrator; each issue gets its own Claude Code worker that does the work in the background and comes back to you for decisions and commits.

## Install

In Claude Code:

```
/plugin marketplace add augusto-nalin/orch
/plugin install orch@orch
/orch:setup
```

Then `cd` into a project checkout and run `orch` (in iTerm2 on macOS for worker panes).

## Build

Needs Go. From a checkout:

```
make build                      # dev build at plugin/bin/orchctl-dev; the plugin's orchctl shim uses it
make release VERSION=x.y.z      # all platforms into dist/ (darwin signed + notarized), updates orchctl.sha256
```
