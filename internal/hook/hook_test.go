package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orch/internal/broker"
	"orch/internal/project"
	"orch/internal/state"
)

type fixture struct {
	t        *testing.T
	repo     string
	p        project.Project
	b        *broker.Broker
	head     string
	notified []string
	agents   []Agent
	agentsN  int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("ORCH_HOME", t.TempDir())
	base, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(base, "demo")
	os.MkdirAll(filepath.Join(repo, "src"), 0o755)
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Skipf("git: %v %s", err, out)
	}
	f := &fixture{t: t, repo: repo, p: project.Resolve(repo), head: "aaaaaaa1"}
	f.b = broker.New(state.Open(f.p.StateDir, f.p.Name))
	return f
}

func (f *fixture) env() Env {
	return Env{
		Notify: func(title, msg string) { f.notified = append(f.notified, msg) },
		Head:   func(string) string { return f.head },
		Agents: func() ([]Agent, error) { f.agentsN++; return f.agents, nil },
		Now:    func() time.Time { return time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC) },
	}
}

// run feeds one hook event; returns stdout.
func (f *fixture) run(event, session string, in map[string]any) string {
	f.t.Helper()
	in["session_id"] = session
	if _, ok := in["cwd"]; !ok {
		in["cwd"] = f.repo
	}
	b, _ := json.Marshal(in)
	var out bytes.Buffer
	if code := Run(event, bytes.NewReader(b), &out, f.env()); code != 0 {
		f.t.Fatalf("exit %d", code)
	}
	return out.String()
}

func (f *fixture) prompt(session, text string) {
	f.t.Helper()
	if out := f.run("user-prompt", session, map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": text}); out != "" {
		f.t.Fatalf("user-prompt output %q", out)
	}
}

func tool(name string, input map[string]any) map[string]any {
	return map[string]any{"hook_event_name": "PreToolUse", "tool_name": name, "tool_input": input}
}

func decision(t *testing.T, out string) (string, string) {
	t.Helper()
	if out == "" {
		return "allow", ""
	}
	var o preOut
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("bad output %q: %v", out, err)
	}
	if o.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Fatalf("event %q", o.HookSpecificOutput.HookEventName)
	}
	return o.HookSpecificOutput.PermissionDecision, o.HookSpecificOutput.PermissionDecisionReason
}

func TestPreTool(t *testing.T) {
	f := newFixture(t)
	f.prompt("w1", "/orch:worker feat-a — do the thing")
	f.prompt("w2", "/worker feat-b")
	f.prompt("o1", "/orch:orch")
	f.b.Claim("feat-a", "file", []string{"src/a.go", "docs/"})
	f.b.CommitGo("feat-a", 1)
	stateDir := f.p.StateDir

	cases := []struct {
		name, session, tool string
		input               map[string]any
		want, reason        string
	}{
		{"claimed edit", "w1", "Edit", map[string]any{"file_path": f.repo + "/src/a.go"}, "allow", ""},
		{"claimed dir", "w1", "Write", map[string]any{"file_path": f.repo + "/docs/new/x.md"}, "allow", ""},
		{"unclaimed edit", "w1", "Edit", map[string]any{"file_path": f.repo + "/src/b.go"}, "deny", "claim first: orch claim feat-a src/b.go"},
		{"other's file", "w2", "Write", map[string]any{"file_path": f.repo + "/src/a.go"}, "deny", "orch claim feat-b src/a.go"},
		{"notebook", "w2", "NotebookEdit", map[string]any{"notebook_path": f.repo + "/n.ipynb"}, "deny", "orch claim feat-b n.ipynb"},
		{"outside checkout", "w2", "Write", map[string]any{"file_path": stateDir + "/issues/feat-b.md"}, "allow", ""},
		{"unregistered session", "x9", "Edit", map[string]any{"file_path": f.repo + "/src/b.go"}, "allow", ""},
		{"read is free", "w2", "Read", map[string]any{"file_path": f.repo + "/src/a.go"}, "allow", ""},
		{"git add -A", "w1", "Bash", map[string]any{"command": "git add -A"}, "deny", "by path"},
		{"git commit -am", "w1", "Bash", map[string]any{"command": "git add src/a.go && git commit -am x"}, "deny", "git commit -- <file>"},
		{"commit no token", "w2", "Bash", map[string]any{"command": "git commit -m x -- src/a.go"}, "deny", "orch wait feat-b commit"},
		{"commit other repo", "w2", "Bash", map[string]any{"command": "cd /tmp && git commit -m x"}, "allow", ""},
		{"sed -i unclaimed", "w1", "Bash", map[string]any{"command": "sed -i '' 's/a/b/' src/b.go"}, "deny", "claim first (Bash edits count too): orch claim feat-a src/b.go"},
		{"redirect claimed", "w1", "Bash", map[string]any{"command": "echo x > src/a.go"}, "allow", ""},
		{"redirect via cd", "w2", "Bash", map[string]any{"command": "cd src && cat a > b.go", "cwd": "/"}, "allow", ""},
		{"redirect into repo via cd", "w2", "Bash", map[string]any{"command": "cd " + f.repo + "/src && cat a > b.go"}, "deny", "orch claim feat-b src/b.go"},
		{"orch reads repo", "o1", "Read", map[string]any{"file_path": f.repo + "/src/a.go"}, "deny", "send a pointer to the worker"},
		{"orch reads image", "o1", "Read", map[string]any{"file_path": "/tmp/shot.png"}, "deny", "no images"},
		{"orch reads state", "o1", "Read", map[string]any{"file_path": stateDir + "/issues/feat-a.md"}, "allow", ""},
		{"orch reads image in state", "o1", "Read", map[string]any{"file_path": stateDir + "/img/x.png"}, "allow", ""},
		{"orch edits", "o1", "Edit", map[string]any{"file_path": f.repo + "/src/b.go"}, "allow", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := tool(c.tool, c.input)
			if cwd, ok := c.input["cwd"]; ok {
				in["cwd"] = cwd
			}
			got, reason := decision(t, f.run("pre-tool", c.session, in))
			if got != c.want || !strings.Contains(reason, c.reason) {
				t.Fatalf("got %s %q, want %s %q", got, reason, c.want, c.reason)
			}
		})
	}
}

func TestPaused(t *testing.T) {
	f := newFixture(t)
	f.prompt("w1", "/worker feat-a")
	f.b.Claim("feat-a", "file", []string{"src/a.go"})
	f.b.CommitGo("feat-a", 1)
	f.b.SetPaused("feat-a", true)
	for _, in := range []map[string]any{
		tool("Edit", map[string]any{"file_path": f.repo + "/src/a.go"}),
		tool("Bash", map[string]any{"command": "git commit -m x -- src/a.go"}),
	} {
		if got, reason := decision(t, f.run("pre-tool", "w1", in)); got != "deny" || !strings.Contains(reason, "paused") {
			t.Fatalf("got %s %q", got, reason)
		}
	}
}

func TestCommitFlow(t *testing.T) {
	f := newFixture(t)
	f.prompt("w1", "/worker feat-a")
	f.b.Claim("feat-a", "file", []string{"src/a.go"})
	f.b.CommitGo("feat-a", 1)
	commit := tool("Bash", map[string]any{"command": "git add src/a.go && git commit -q -m x -- src/a.go"})

	if got, _ := decision(t, f.run("pre-tool", "w1", commit)); got != "allow" {
		t.Fatalf("commit with token: %s", got)
	}
	if len(f.notified) != 1 || f.notified[0] != "demo-feat-a waits for commit approval" {
		t.Fatalf("notified %v", f.notified)
	}
	f.head = "bbbbbbb2c3" // the commit landed
	commit["hook_event_name"] = "PostToolUse"
	out := f.run("post-tool", "w1", commit)
	if !strings.Contains(out, `"additionalContext":"orch: commit bbbbbbb logged, claims released"`) {
		t.Fatalf("post out %q", out)
	}
	f.b.St.View(func(s *state.State) error {
		if len(s.Claims) != 0 || s.Tokens["feat-a"] != 0 || len(s.Heads) != 0 {
			t.Fatalf("after commit %+v", s)
		}
		return nil
	})
	log, _ := os.ReadFile(filepath.Join(f.p.StateDir, "log.md"))
	if !strings.Contains(string(log), "feat-a commit bbbbbbb") {
		t.Fatalf("log:\n%s", log)
	}
	// token used: a second commit is denied
	commit["hook_event_name"] = "PreToolUse"
	if got, _ := decision(t, f.run("pre-tool", "w1", commit)); got != "deny" {
		t.Fatalf("second commit: %s", got)
	}
}

func TestCommitFailedKeepsToken(t *testing.T) {
	f := newFixture(t)
	f.prompt("w1", "/worker feat-a")
	f.b.Claim("feat-a", "file", []string{"src/a.go"})
	f.b.CommitGo("feat-a", 1)
	commit := tool("Bash", map[string]any{"command": "git commit -m x -- src/a.go"})
	f.run("pre-tool", "w1", commit)
	// HEAD unchanged (hook rejected, nothing staged…)
	if out := f.run("post-tool", "w1", commit); out != "" {
		t.Fatalf("post out %q", out)
	}
	f.b.St.View(func(s *state.State) error {
		if s.Tokens["feat-a"] != 1 || len(s.Claims) != 1 {
			t.Fatalf("%+v", s)
		}
		return nil
	})
}

func TestAgentsFallback(t *testing.T) {
	f := newFixture(t)
	f.b.Row("feat-a", "active", "") // state.json exists, no registry entry
	f.b.Claim("feat-a", "file", []string{"src/a.go"})
	f.agents = []Agent{{SessionID: "w1", Name: "demo-feat-a"}, {SessionID: "o1", Name: "demo-orch"}}
	edit := tool("Edit", map[string]any{"file_path": f.repo + "/src/b.go"})
	if got, _ := decision(t, f.run("pre-tool", "w1", edit)); got != "deny" {
		t.Fatalf("resolved worker: %s", got)
	}
	if got, _ := decision(t, f.run("pre-tool", "w1", edit)); got != "deny" || f.agentsN != 1 {
		t.Fatalf("cached: %s, agents called %d", got, f.agentsN)
	}
	read := tool("Read", map[string]any{"file_path": f.repo + "/src/a.go"})
	if got, _ := decision(t, f.run("pre-tool", "o1", read)); got != "deny" {
		t.Fatalf("resolved orch: %s", got)
	}
	// unknown session cached as none: allowed, no second lookup
	f.run("pre-tool", "zz", edit)
	f.run("pre-tool", "zz", edit)
	if f.agentsN != 3 {
		t.Fatalf("agents called %d", f.agentsN)
	}
}

func TestNoStateNoEnforcement(t *testing.T) {
	f := newFixture(t)
	edit := tool("Edit", map[string]any{"file_path": f.repo + "/src/b.go"})
	if got, _ := decision(t, f.run("pre-tool", "w1", edit)); got != "allow" || f.agentsN != 0 {
		t.Fatalf("got %s, agents %d", got, f.agentsN)
	}
	if _, err := os.Stat(f.p.StateDir); err == nil {
		t.Fatal("state dir created for a project without orch")
	}
}

func TestFailOpen(t *testing.T) {
	f := newFixture(t)
	f.prompt("w1", "/worker feat-a")
	os.WriteFile(filepath.Join(f.p.StateDir, "state.json"), []byte("{corrupt"), 0o644)
	edit := tool("Edit", map[string]any{"file_path": f.repo + "/src/b.go"})
	if out := f.run("pre-tool", "w1", edit); out != "" {
		t.Fatalf("not fail-open: %q", out)
	}
	var out bytes.Buffer
	Run("pre-tool", strings.NewReader("not json"), &out, f.env())
	if out.Len() != 0 {
		t.Fatalf("bad input output %q", out.String())
	}
	errs, _ := os.ReadFile(filepath.Join(f.p.StateDir, "hook-errors.log"))
	if !strings.Contains(string(errs), "state.json") {
		t.Fatalf("hook-errors.log:\n%s", errs)
	}
	if _, err := os.Stat(filepath.Join(project.Home(), "hook-errors.log")); err != nil {
		t.Fatalf("decode error not logged: %v", err)
	}
}

func TestPromptRegistry(t *testing.T) {
	f := newFixture(t)
	for _, c := range []struct{ prompt, role, issue string }{
		{"/orch", "orch", ""},
		{"/orch:orch start", "orch", ""},
		{"/worker fix-1 — user: \"x\"", "worker", "fix-1"},
		{"  /orch:worker v2.", "worker", "v2"},
		{"/orchestra", "", ""},
		{"please /worker x", "", ""},
	} {
		f.prompt("s", c.prompt)
		sess, ok, _ := f.b.Lookup("s")
		if c.role == "" {
			if ok {
				t.Errorf("%q registered %+v", c.prompt, sess)
			}
			continue
		}
		if !ok || sess.Role != c.role || sess.Issue != c.issue {
			t.Errorf("%q → %+v", c.prompt, sess)
		}
		f.b.St.Update(func(s *state.State, _ func(string, ...any)) error { delete(s.Sessions, "s"); return nil })
	}
}
