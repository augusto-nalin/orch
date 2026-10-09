package main

// Launching sessions. The plugin root is found from this binary (<root>/bin/orchctl),
// so a clone anywhere and an installed plugin both work without hardcoded paths.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("state home %s is outside your home dir; the sandbox and permission rules can't name it", sh)
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	out := strings.ReplaceAll(string(b), "~/.local/state/orch", "~/"+filepath.ToSlash(rel))
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
		out, err := exec.Command("claude", "agents", "--json", "--all").Output()
		if err != nil {
			return "", fmt.Errorf("claude agents: %v", err)
		}
		id, err := lastSession(out, name)
		if err != nil {
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

// lastSession is the session id of the newest background session called name in
// `claude agents --json --all` output; an error if none, or if it still runs.
func lastSession(agents []byte, name string) (string, error) {
	var list []struct {
		Kind, Name, SessionID string
		StartedAt             int64
		Pid                   *int
	}
	if err := json.Unmarshal(agents, &list); err != nil {
		return "", fmt.Errorf("claude agents: %v", err)
	}
	var id string
	var at int64
	running := false
	for _, a := range list {
		if a.Kind == "background" && a.Name == name && a.StartedAt >= at {
			id, at, running = a.SessionID, a.StartedAt, a.Pid != nil
		}
	}
	switch {
	case id == "":
		return "", fmt.Errorf("no session named %s to reopen", name)
	case running:
		return "", fmt.Errorf("%s is still running; message it instead", name)
	}
	return id, nil
}
