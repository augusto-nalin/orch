# orch

Orchestrator + workers for Claude Code: a thin router session delegates each issue to a long-lived background worker; a Go broker and hooks handle claims, turns and commits.

## Install

In Claude Code:

```
/plugin marketplace add augusto-nalin/orch
/plugin install orch@orch
/orch:setup
```

Then `cd` into a project checkout and run `orch` (in iTerm2 on macOS for worker panes).
