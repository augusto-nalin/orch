// Command orchctl is the broker for the /orch and /worker skills: one-line commands
// over the project's state (see PLAN.md). The project is resolved from the cwd.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"orch/internal/broker"
	"orch/internal/hook"
	"orch/internal/project"
	"orch/internal/state"
)

const usage = `usage: orchctl <command>
worker:
  claim <issue> <file>…        GO | HELD <file> by <issue>
  need <issue> <resource>…     GO | HELD <resource> by <issue>
  release <issue> [<file>…]    drop claims (all, and waiting requests, if none given)
  wait [--timeout 30m] <issue> go|commit   block until granted / commit token
orch:
  full                         startup view
  spawn <issue> <prompt>       start the worker in the background + show its pane
  reopen <issue> <message>     reopen a stopped worker + show its pane
  pane <issue> | close <issue> show a worker's pane / stop it and close the pane
  row <issue> <status> [outcome]
  q <issue> <gist>             → Qn
  a <Qn> [note]
  checked <issue>              the user passed (or skipped) the worker's CHECK
  order <item> <issue>…        turn order for a contested item
  commit-go <issue> [n]
  pause <issue> | resume <issue>
  log <text>
hooks:
  hook <event>                 user-prompt|pre-tool|post-tool|stop|stop-failure|notify; hook JSON on stdin
setup:
  version                      release version, or dev
  dir | name | root            state dir / project name / plugin dir
  update-check                 say if a newer release exists (asks GitHub at most daily)
  flags orch|worker            claude flags for that session (shell-quoted)
  import [--force]             board.md + questions.md → state.json
`

// version is set by `make release`.
var version = "dev"

func main() {
	out, code := run(os.Args[1:], os.Stdout)
	if out != "" {
		fmt.Println(out)
	}
	os.Exit(code)
}

func run(args []string, stdout io.Writer) (string, int) {
	if len(args) == 0 {
		args = []string{"full"}
	}
	cwd, _ := os.Getwd()
	p := project.Resolve(cwd)
	st := state.Open(p.StateDir, p.Name)
	b := broker.New(st)
	cmd, args := args[0], args[1:]

	need := func(n int) bool {
		if len(args) < n {
			fmt.Fprint(os.Stderr, usage)
			return false
		}
		return true
	}
	res := func(out string, err error) (string, int) {
		if err != nil {
			fmt.Fprintln(os.Stderr, "orch:", err)
			return "", 2
		}
		if strings.HasPrefix(out, "HELD") {
			return out, 1
		}
		return out, 0
	}

	switch cmd {
	case "version":
		return version, 0
	case "dir":
		return p.StateDir, 0
	case "name":
		return p.Name, 0
	case "full":
		return res(b.Full(p.Root))
	case "root":
		return res(pluginRoot())
	case "update-check":
		root, err := pluginRoot()
		if err != nil {
			return res("", err)
		}
		return updateCheck(root, time.Now()), 0
	case "flags":
		if !need(1) {
			return "", 2
		}
		root, err := pluginRoot()
		if err != nil {
			return res("", err)
		}
		flags, err := claudeFlags(root, args[0])
		return res(shellQuote(flags), err)
	case "spawn", "reopen":
		if !need(2) {
			return "", 2
		}
		return res(launch(p, cmd, args[0], strings.Join(args[1:], " ")))
	case "pane", "close":
		if !need(1) {
			return "", 2
		}
		root, err := pluginRoot()
		if err != nil {
			return res("", err)
		}
		return res(script(root, "orch-"+cmd+".sh", p.Name+"-"+args[0]))
	case "row":
		if !need(2) {
			return "", 2
		}
		return res(b.Row(args[0], args[1], strings.Join(args[2:], " ")))
	case "claim", "need", "release":
		if !need(1) || cmd != "release" && !need(2) {
			return "", 2
		}
		items := args[1:]
		if cmd != "need" {
			items = normalize(p, cwd, items)
		}
		switch cmd {
		case "claim":
			return res(b.Claim(args[0], "file", items))
		case "need":
			return res(b.Claim(args[0], "res", items))
		}
		return res(b.Release(args[0], items))
	case "order":
		if !need(2) {
			return "", 2
		}
		item := args[0]
		if n := normalize(p, cwd, []string{item}); len(n) == 1 && strings.Contains(item, "/") {
			item = n[0]
		}
		return res(b.Order(item, args[1:]))
	case "wait":
		var timeout time.Duration
		if len(args) >= 2 && args[0] == "--timeout" {
			d, err := time.ParseDuration(args[1])
			if err != nil {
				return res("", err)
			}
			timeout, args = d, args[2:]
		}
		if !need(2) {
			return "", 2
		}
		out, err := b.Wait(args[0], args[1], timeout)
		if errors.Is(err, broker.ErrTimeout) {
			return "TIMEOUT", 1
		}
		return res(out, err)
	case "commit-go":
		if !need(1) {
			return "", 2
		}
		n := 1
		if len(args) > 1 {
			n, _ = strconv.Atoi(args[1])
		}
		return res(b.CommitGo(args[0], n))
	case "pause", "resume":
		if !need(1) {
			return "", 2
		}
		return res(b.SetPaused(args[0], cmd == "pause"))
	case "q":
		if !need(2) {
			return "", 2
		}
		return res(b.Q(args[0], strings.Join(args[1:], " ")))
	case "a":
		if !need(1) {
			return "", 2
		}
		return res(b.A(args[0], strings.Join(args[1:], " ")))
	case "checked":
		if !need(1) {
			return "", 2
		}
		return res(b.Checked(args[0]))
	case "log":
		if !need(1) {
			return "", 2
		}
		return res(b.Log(strings.Join(args, " ")))
	case "import":
		s, err := st.Import(len(args) > 0 && args[0] == "--force")
		if err != nil {
			return res("", err)
		}
		return fmt.Sprintf("imported %d rows, %d claims, %d questions", len(s.Rows), len(s.Claims), len(s.Questions)), 0
	case "hook":
		if !need(1) {
			return "", 2
		}
		return "", hook.Run(args[0], os.Stdin, stdout, hook.DefaultEnv())
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return "", 0
	}
	fmt.Fprint(os.Stderr, usage)
	return "", 2
}

// normalize turns file args into paths relative to their checkout top, so claims
// match what hooks see. A trailing "/" (or an existing dir) claims a directory.
// Paths outside the project are kept as given.
func normalize(p project.Project, cwd string, items []string) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		abs := it
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, it)
		}
		dir := strings.HasSuffix(it, "/")
		if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
			dir = true
		}
		rel, ok := p.Rel(abs)
		if !ok || rel == "." {
			out = append(out, it)
			continue
		}
		if dir {
			rel += "/"
		}
		out = append(out, rel)
	}
	return out
}
