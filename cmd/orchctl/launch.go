package main

// Launching sessions. The plugin root is found from this binary (<root>/bin/orchctl),
// so a clone anywhere and an installed plugin both work without hardcoded paths.

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"orch/internal/project"
)

func pluginRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	return filepath.Dir(filepath.Dir(exe)), nil
}

// installed: the plugin sits in Claude's plugin cache, so it is on in every session
// and --plugin-dir would load it a second time.
func installed(root string) bool {
	return strings.Contains(filepath.ToSlash(root), "/plugins/cache/")
}

// settingsFile is the --settings file for role ("orch" or "worker"). The plugin's
// files name the default state home; any other one ($ORCH_HOME, $XDG_STATE_HOME) gets
// a copy with it swapped in, kept in the state home.
func settingsFile(root, role string) (string, error) {
	src := filepath.Join(root, role+"-settings.json")
	home, _ := os.UserHomeDir()
	sh := project.Home()
	if sh == filepath.Join(home, ".local", "state", "orch") {
		return src, nil
	}
	rel, err := filepath.Rel(home, sh)
	rel = filepath.ToSlash(rel)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("state home %s is outside your home dir; the sandbox and permission rules can't name it", sh)
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	out := strings.ReplaceAll(string(b), "~/.local/state/orch", "~/"+rel)
	dst := filepath.Join(sh, role+"-settings.json")
	if err := os.MkdirAll(sh, 0o755); err != nil {
		return "", err
	}
	return dst, os.WriteFile(dst, []byte(out), 0o644)
}

// claudeFlags are the flags every orch or worker session starts with.
func claudeFlags(root, role string) ([]string, error) {
	set, err := settingsFile(root, role)
	if err != nil {
		return nil, err
	}
	flags := []string{"--settings", set}
	if !installed(root) {
		flags = append([]string{"--plugin-dir", root}, flags...)
	}
	return flags, nil
}

// shellQuote joins args for a shell command line (the iTerm launcher types it).
func shellQuote(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

// lastLine runs a command and returns its last non-empty output line.
func lastLine(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if err != nil {
		return "", fmt.Errorf("%s: %v: %s", name, err, last)
	}
	return last, nil
}

// launch starts (spawn) or reopens (reopen) the worker session for issue in the
// background, then shows it in the orchestrator screen.
func launch(p project.Project, cmd, issue, text string) (string, error) {
	root, err := pluginRoot()
	if err != nil {
		return "", err
	}
	flags, err := claudeFlags(root, "worker")
	if err != nil {
		return "", err
	}
	name := p.Name + "-" + issue
	args := []string{"--bg"}
	if cmd == "reopen" {
		// By id: --resume <name> --bg starts a copy under a new id, and once two
		// sessions share the name, the next one stalls in the resume picker.
		list, err := agents()
		if err != nil {
			return "", err
		}
		// The resume gets a new id; a blocked one left behind is a second worker.
		stopped, err := stopBlocked(list, name)
		if err != nil {
			return "", err
		}
		id, err := lastSession(list, name, hasTranscript)
		if err != nil {
			if stopped > 0 {
				err = fmt.Errorf("%v (stopped %d blocked duplicate(s))", err, stopped)
			}
			return "", err
		}
		args = []string{"--resume", id, "--bg"}
	}
	args = append(append(args, flags...), "--name", name, text)
	out, err := lastLine("claude", args...)
	if err != nil {
		return "", err
	}
	pane, err := script(root, "orch-pane.sh", name)
	if err != nil {
		return out + "\n" + err.Error(), nil
	}
	return out + "\n" + pane, nil
}

// script runs one of the plugin's scripts.
func script(root, file string, args ...string) (string, error) {
	return lastLine("bash", append([]string{filepath.Join(root, "scripts", file)}, args...)...)
}

// agent is one session in `claude agents --json --all`.
type agent struct {
	ID, Kind, Name, SessionID, State string
	StartedAt                        int64
	Pid                              *int
}

// working: it runs a process. live: working, or blocked — waiting for input or after
// an API error, with no process, hidden from ListAgents, yet alive until `claude stop`.
func (a agent) working() bool { return a.Pid != nil }
func (a agent) live() bool {
	return a.working() || a.State != "done" && a.State != "stopped" && a.State != "failed"
}

func agents() ([]agent, error) {
	out, err := exec.Command("claude", "agents", "--json", "--all").Output()
	if err != nil {
		return nil, fmt.Errorf("claude agents: %v", err)
	}
	return parseAgents(out)
}

func parseAgents(out []byte) ([]agent, error) {
	var list []agent
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("claude agents: %v", err)
	}
	return list, nil
}

// named is the background sessions called name, oldest first.
func named(list []agent, name string) []agent {
	var out []agent
	for _, a := range list {
		if a.Kind == "background" && a.Name == name {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b agent) int { return cmp.Compare(a.StartedAt, b.StartedAt) })
	return out
}

// lastSession is the session id of the newest background session called name that
// has a transcript (one stopped before its first turn has none, and resuming it
// fails); an error if none, or if one of them is working.
func lastSession(list []agent, name string, saved func(id string) bool) (string, error) {
	var id string
	for _, a := range named(list, name) {
		if a.working() {
			return "", fmt.Errorf("%s is still running; message it instead", name)
		}
		if a.live() || saved(a.SessionID) {
			id = a.SessionID
		}
	}
	if id == "" {
		return "", fmt.Errorf("no session named %s to reopen", name)
	}
	return id, nil
}

// stopBlocked stops the blocked sessions called name and says how many.
func stopBlocked(list []agent, name string) (int, error) {
	n := 0
	for _, a := range named(list, name) {
		if a.live() && !a.working() && a.ID != "" {
			if out, err := exec.Command("claude", "stop", a.ID).CombinedOutput(); err != nil {
				return n, fmt.Errorf("claude stop %s: %v: %s", a.ID, err, strings.TrimSpace(string(out)))
			}
			n++
		}
	}
	return n, nil
}

// alive says, per issue, whether its worker sessions run: "<issue> working|blocked|
// stopped|none [<id>…]", ids of the live ones. More than one id is a duplicate.
// Finished issues show only while a session of theirs still lives.
func alive(list []agent, project string, issues, finished []string) string {
	var lines []string
	for n, issue := range append(issues, finished...) {
		st, ids := "none", []string{}
		for _, a := range named(list, project+"-"+issue) {
			switch {
			case a.working():
				st = "working"
			case a.live() && st != "working":
				st = "blocked"
			case st == "none":
				st = "stopped"
			}
			if a.live() {
				ids = append(ids, a.ID)
			}
		}
		if n >= len(issues) && len(ids) == 0 {
			continue
		}
		lines = append(lines, strings.TrimSpace(issue+" "+st+" "+strings.Join(ids, " ")))
	}
	return strings.Join(lines, "\n")
}

// hasTranscript: claude kept a transcript for session id, under any project.
func hasTranscript(id string) bool {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".claude")
	}
	m, _ := filepath.Glob(filepath.Join(dir, "projects", "*", id+".jsonl"))
	return len(m) > 0
}
