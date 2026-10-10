# ORCH
ORCH (like orch-ard) sits between full autonomy and a single chat, agentic workflow, but without loosing control.

It solves a simple problem, parallelise work, but still keeps you in control and aware of everything that has been done..

## The orchestrator

**Orchestrator** is long lived it keeps its context clean as actual work is delegated.

You talk to the orchestrator, each issue gets its own Claude Code worker. 

## Workers
**Workers** are long lived, are NOT ephemeral, and are NOT agents.

**Workers** keep you in the loop, asks questions, and permissions, or and do the work.

**Worker** takes a task and stays with it, for ever, if an issue appears, the same worker with all the context is re-alived.


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

## Claude desktop app

In the Code tab, open a session on your project.

1. Open the Terminal (Ctrl+\`) and install \
`claude plugin marketplace add augusto-nalin/orch` \
`claude plugin install orch@orch`
2. Start a new session on the project, then set up orch \
`/orch:setup`
3. Rename the session to `<project>-orch` (click its title), where `<project>` is the project folder name, e.g. `orch-orch`
4. Start the orchestrator \
`/orch:orch`

Workers run in the background, `claude agents` in the Terminal shows them.

## Update

`orch` says when a newer release is out. Update with \
`orchctl update` \
then restart your Claude sessions. It also replaces a dev build (`orchctl version` shows `<version>-dev`) with the release.

## Build

Build with \
`make build` \
or \
`go build -o plugin/bin/orchctl-dev ./cmd/orchctl`
