# orch v2 — Go broker + enforcement hooks, packaged as a plugin

## Context
The `/orch` skill is meant to be a thin router. Its context should hold only the
conversation with the user and the decisions. Two problems remain:

1. **Context growth.** After the skill rewrite, the orch still grows about 2k tokens
   per user interaction (measured 2026-10-06: 42k → 81k). About half the worker
   messages are mechanical: CLAIM→GO, NEED build, COMMIT notices, releases, logging.
   Each one costs an orch turn plus harness overhead: a ~180-token wrapper on every
   cross-session message, a ~130-token receipt on every SendMessage, and token
   reminders on every call.
2. **Rules are only text.** A worker spawned before a skill change kept the old rules
   and made 2 commits without a go-ahead. Running sessions never re-read skills.

**Goal:** move every mechanical step into a deterministic Go binary (the broker), and
enforce the rules with hooks that run fresh on every tool call. The orch handles only
judgment: talking to the user, questions, decisions, commit order and contested
files or resources. Target: ≤0.7k orch tokens per user interaction, and rules that
hold even for a worker on a stale skill.

Decisions made with the user:
- Go, not Python. A CLI binary, not an MCP server.
- Repo `~/source/orch` (already `git init`, empty, no remote). Commit freely.
- Contention (two workers want the same file or the build) → **the orch decides
  every conflict**. The broker never auto-grants a contested item.
- The orch decides **when** a worker commits. The worker decides **which files** go
  in it. Default is at most one commit per task.
- Plan is implemented by the orch (via a worker), not by this session.

## Implementation note for the orch
- One worker, `orch-v2`, spawned from `~/source/orch`, so it sits on that project's
  own board. It works and commits only there; no other worker touches that repo.
- orch is generic: no project-specific names in code, tests or docs.
- **One commit per phase**, after the orch's go.
- Go 1.23.6 is installed (`/opt/homebrew/bin/go`). Standard library only: JSON
  state, `syscall.Flock` for locking. No MCP SDK needed.

## Naming
- `orch` is the user's start command: the iTerm2 orchestrator screen with the plugin
  loaded (`plugin/scripts/orch-screen.sh`). No tmux.
- `orchctl` is the broker CLI that skills, hooks and workers call.
- `orch setup` (`plugin/scripts/orch-setup.sh`, also `make install`) sets orch up and is
  safe to re-run: builds `orchctl`, links `orch` + `orchctl` into `~/.local/bin`, checks
  PATH, a shadowing `orch` alias, iTerm2, jq and claude, moves a legacy `~/.claude/orch`
  to the state home, and imports the current project's old markdown state. Plain `orch`
  runs it by itself when `orchctl` is missing.

## Architecture

### Repo layout (`~/source/orch`)
```
go.mod                      module orch, go 1.23
cmd/orchctl/main.go         CLI entry: subcommands + `orchctl hook <event>`
internal/state/             state.json load/save under flock, board.md/questions.md render, log append
internal/broker/            claims, waits, commit tokens, contention queue, registry (session→role/issue)
internal/hook/              hook handlers (read JSON on stdin, write decision JSON)
internal/project/           project name + state dir (same rule as orch-state.sh: basename of git common dir's parent)
plugin/.claude-plugin/plugin.json
plugin/hooks/hooks.json     → "${CLAUDE_PLUGIN_ROOT}/bin/orch" hook <event>
plugin/skills/orch/SKILL.md      slimmed (see Skills)
plugin/skills/worker/SKILL.md    slimmed
plugin/scripts/             orch-screen.sh (`orch` start command), orch-setup.sh (`orch setup`), orch-iterm.sh, orch-pane.sh, orch-close.sh (moved from ~/.claude/skills/orch; iTerm2 only)
plugin/orch-settings.json   moved from ~/.claude/skills/orch (caveman off, you-should-know on, state dir writable)
plugin/worker-settings.json passed to workers: state dir writable in the sandbox, orchctl allowed
plugin/bin/orchctl          shim: orchctl-dev if built, else signed release binary (downloaded, sha256-checked)
Makefile                    build → plugin/bin/orchctl-dev; test; install (= orch setup); release; publish
```

### State
- State lives in `~/.local/state/orch/<project>/state.json` (not `~/.claude`: the Bash
  sandbox protects it even when allowed, and workers run `orchctl` through Bash), under an exclusive flock on
  `state.lock` for every read-modify-write. It holds: board rows, claims, contention
  queue, commit tokens, open and answered question index, and the session registry.
- `board.md`, `questions.md` and `log.md` stay as human-readable views, rendered or
  appended by the binary.
- `issues/<issue>.md` stays owned by workers, unchanged.
- A one-time `orchctl import` parses today's `board.md` (rows and `## Claims`) and
  `questions.md` into `state.json`. Port the formats handled by
  `~/.claude/skills/orch/orch-state.sh` (row/claim/release/q/a/log). That script is
  the reference and is retired after cutover.

### CLI (all output one word or one line, to keep contexts small)
Worker:
| command | result |
|---|---|
| `orchctl claim <issue> <file>…` | `GO`, or `HELD <file> by <issue>`, which queues the request as *contested* |
| `orchctl need <issue> build` | `GO` or `HELD build by <issue>` (same contention rules) |
| `orchctl release <issue> [<file>…]` | drops claims (all if no files); a contested item passes to whoever the orch ordered next |
| `orchctl wait <issue> go\|commit` | blocks until the claim or need is granted, or a commit token exists; prints `GO`/`COMMIT`. Run with `run_in_background` so the harness wakes the worker on exit |

Orch:
| command | result |
|---|---|
| `orchctl full` | startup view (same content as today's `orch-state.sh full`) |
| `orchctl row <issue> <status> ["outcome"]` | board row; auto-logs |
| `orchctl q <issue> "<gist>"` → `Q172` / `orchctl a <Qn>` | question index; auto-logs |
| `orchctl order <item> <issue> [<issue>…]` | decides a contested item's queue: the first issue gets it now if free, the rest in order on release |
| `orchctl commit-go <issue> [n]` | grants n (default 1) commit tokens; wakes the worker's `orchctl wait … commit` |
| `orchctl pause <issue>` / `orchctl resume <issue>` | sets a flag that the hooks enforce (edits denied while paused) |
| `orchctl log "<text>"` | rarely needed; the other commands log themselves |

Hooks only: `orchctl hook user-prompt`, `orchctl hook pre-tool`, `orchctl hook post-tool`,
`orchctl hook stop`, `orchctl hook stop-failure`.

### Hooks (`plugin/hooks/hooks.json`)
Hooks are active only in sessions launched with the plugin, so the user's normal
sessions are unaffected.

1. **UserPromptSubmit → registry.** A prompt starting with `/orch` (or `/orch:orch`)
   registers `session_id` as orch for this project. `/worker <issue>` (or
   `/orch:worker`) registers it as the worker for `<issue>`.
   - Fallback for resumed or unregistered sessions: resolve `session_id` → name with
     `claude agents --json` (`sessionId`, `name` = `<project>-<issue>`) and cache it.
     Verify first that `--resume` keeps the same session_id.
2. **PreToolUse `Edit|Write|NotebookEdit` (workers).** If `file_path` is inside the
   project checkout and not claimed by this worker's issue, or the issue is paused →
   `permissionDecision: deny` with the reason `claim first: orchctl claim <issue> <file>`.
   - Paths outside the checkout (the issue file in the state dir, scratch) are allowed.
3. **PreToolUse `Bash` (workers).**
   - `git add -A|--all|.` and `git commit -a|--all` → deny.
   - `git commit` without a commit token → deny with the reason
     `wait for the orch: orchctl wait <issue> commit`.
   - With a token → allow; the user's existing "ask" hook in `~/.claude/settings.json`
     still prompts, since deny/ask/allow combine and ask is kept.
   - Also fire a macOS notification (`osascript -e 'display notification …'`):
     "<session> waits for commit approval". This replaces the COMMIT message to the
     orch at zero tokens.
4. **PostToolUse `Bash` (workers).** After a successful `git commit`, consume one
   token, release that worker's claims, and log `<issue> commit <sha>`. This
   replaces the worker's REPORT.
5. **PreToolUse `Read` (orch session only).** Deny reads of files inside the project
   checkout and of images, with the reason "send a pointer to the worker instead".
   The state dir is allowed.
6. **Known gap:** edits made through Bash (`sed -i`, `>`) bypass the claim gate. The
   worker skill forbids them; optionally the Bash hook denies obvious `sed -i` / `>`
   on unclaimed repo paths. Best effort.
7. **Silent workers.** v2 moved CLAIM/NEED/GO off messages, so a worker that stops
   mid-work is invisible to the orch.
   - PreToolUse `SendMessage` to `<project>-orch`, or a Bash `orchctl wait`, marks the
     worker as having spoken this turn.
   - **Stop (workers):** holding claims, not paused, and silent this turn → `decision:
     block` once (`stop_hook_active` lets the second stop through).
   - **StopFailure** (an API error ended the turn; no Stop runs, output ignored): mark
     the worker stalled, log it, macOS notification. The orch's UserPromptSubmit
     (cross-session messages included) adds a "stalled workers" note until that
     worker's next prompt clears it; the orch nudges it.

The hooks must exit fast (Go binary, no network) and fail **open** on internal errors
(log to `~/.local/state/orch/<project>/hook-errors.log`), so a broker bug never blocks work.

### Message protocol after v2 (worker → orch, still SendMessage, ultra)
Kept, because each needs judgment or holds a decision:
- `Q` (dialog-ready, as today)
- `DECIDED`
- `READY <issue>: <outcome> | done|handover | open: …` (the orch decides the commit order)
- `HELD <issue>: <item> by <other>` (the orch decides the order → `orchctl order`)
- `NOTE`, `ANSWERED-DIRECT`, `RELATED`, `CONFLICT`

Removed, because the broker or hooks handle them: `CLAIM`, `NEED`, `COMMIT`, `REPORT`,
`GO`/`COMMIT-GO` messages from the orch (the worker wakes from `orchctl wait`).

### Skills (slimmed; moved into the plugin)
- **orch**: keep the router principles (conversation and decisions only, pointers
  over content, never reword, plain English to the user, caveman ultra to workers),
  asking via AskUserQuestion, closing workers, deep-dive guidance. Replace the
  protocol table with the short v2 list. Replace the state section with the CLI
  commands. Spawn command adds `--plugin-dir ~/source/orch/plugin`.
- **worker**: keep issue-file ownership, the template, stay-on-issue, and "the user
  decides". Turns and commits become: `orchctl claim` → on `HELD`, send `HELD` to the
  orch, run `orchctl wait <issue> go` in the background, end the turn. Done → `READY`,
  `orchctl wait <issue> commit` in the background, then exactly one commit of your own
  files. Note in the skill that hooks enforce all of this.
- Both skills shrink by about 40%, so less fixed context.

### Launch changes
- `orch-screen.sh` / `orch-iterm.sh`: add `--plugin-dir ~/source/orch/plugin` next to
  the existing `--settings …/orch-settings.json`. Point the settings path at the
  plugin copy.
- Worker spawn and resume (in the orch skill):
  `claude --bg --plugin-dir ~/source/orch/plugin --name <p>-<i> "/orch:worker <i> — …"`.
- Verify the plugin skill names (`/orch:orch`, `/orch:worker`) and update the registry
  regexes and screen scripts to match.

## Phases (one commit each, orch gives the go)
1. **Core:** go.mod, `internal/project`, `internal/state` (flock, JSON, renderers),
   `internal/broker` (claims, contention, tokens, registry), CLI subcommands, `orch
   import`. Unit tests: claim/held/order/release handoff, commit tokens, paused flag,
   concurrent writers (goroutines + flock), and import of a synthetic fixture in the
   formats written by `orch-state.sh` (`testdata/`).
2. **Hooks:** `orchctl hook …` handlers with table tests that feed fixture hook JSON
   (`session_id`, `cwd`, `tool_name`, `tool_input`, `hook_event_name`, `prompt`) and
   assert the decision JSON. Cover fail-open.
3. **Plugin and skills:** plugin.json, hooks.json, the moved scripts and settings,
   the slimmed skills, the Makefile `install`.
4. **Cutover (with the user):** `orchctl import` for each project with live state;
   restart its orch with the plugin; resume its workers with `--plugin-dir`
   (verify that flags apply on `--resume`); retire `~/.claude/skills/orch` and `~/.claude/skills/worker`, keeping
   the backups in `.bak-2026-10-06/`.

## Verification
- `go test ./...` passes; `go vet` clean.
- **Hook smoke tests** (no model): `echo '<fixture>' | plugin/bin/orchctl hook pre-tool`
  for:
  - an unclaimed edit (deny)
  - a claimed edit (allow)
  - `git commit` without a token (deny)
  - `git commit` with a token (allow, notification fires)
  - `git add -A` (deny)
  - an orch reading a repo file (deny)
  - a corrupt state.json (allow, error logged)
- **Live end-to-end in a scratch git repo,** with two workers spawned through the
  plugin:
  - worker A claims X and edits it; worker B's edit of X is denied
  - B gets `HELD`, the orch runs `orchctl order`, A releases, and B's background
    `orchctl wait` wakes it
  - A sends READY, the orch runs `commit-go A`, A commits once, and a second
    `git commit` is denied
- **Context check:** after about 10 routed interactions in a real project's orch,
  measure growth per interaction from the transcript (assistant
  `usage.cache_read_input_tokens + input_tokens` deltas). Target ≤0.7k, versus about
  2k today.
