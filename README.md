# orch

orch sits between full autonomy and a single chat, agentic workflow, but without loosing control.

You talk to the orchestrator, each issue gets its own Claude Code worker. 

Workers are long lived, Worker are NOT ephemeral, workers are NOT agents.

Workers keep you in the loop, asks questions, and permissions, or and do the work.

Worker takes a task and stays with it, for ever, if an issue appears, the same worker with all the context is re-alived.

Orchestrator is long lived it keeps its context clean as actual work is delegated.

## Install

In Claude Code, one command at a time:

1. Add the marketplace \
`/plugin marketplace add augusto-nalin/orch` 
2. Install the plugin \
`/plugin install orch@orch`
4. Set up orch \
`/orch:setup`

Then `cd` into a project checkout and run `orch` 
Note: iTerm2 shows agents chats in real time.

## Build

Build with \
`make build` \
or \
`go build -o plugin/bin/orchctl-dev ./cmd/orchctl`
