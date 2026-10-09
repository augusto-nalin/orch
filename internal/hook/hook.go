// Package hook implements `orchctl hook <event>`: Claude Code hook handlers that
// keep the session registry and enforce the worker and orch rules on every tool
// call. They read the hook JSON on stdin and write a decision JSON (or nothing).
//
// Handlers fail open: any internal error is appended to the project's
// hook-errors.log and the tool call goes ahead, so a broker bug never blocks work.
package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"orch/internal/broker"
	"orch/internal/project"
	"orch/internal/state"
)

type Input struct {
	SessionID string         `json:"session_id"`
	Cwd       string         `json:"cwd"`
	Event     string         `json:"hook_event_name"`
	Prompt    string         `json:"prompt"`
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	// Stop / StopFailure
	StopHookActive bool   `json:"stop_hook_active"`
	Error          string `json:"error"`
	// Notification
	Message string `json:"message"`
}

// Agent is one row of `claude agents --json`.
type Agent struct {
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
}

// Env holds the side effects, swapped out in tests.
type Env struct {
	Notify func(title, msg string)
	Head   func(dir string) string // git HEAD sha of the repo at dir, "" if none
	Agents func() ([]Agent, error)
	Now    func() time.Time
}

func DefaultEnv() Env {
	return Env{
		Notify: func(title, msg string) {
			script := fmt.Sprintf("display notification %q with title %q", msg, title)
			if c := exec.Command("osascript", "-e", script); c.Start() == nil {
				c.Process.Release()
			}
		},
		Head: func(dir string) string {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").Output()
			if err != nil {
				return ""
			}
			return strings.TrimSpace(string(out))
		},
		Agents: func() ([]Agent, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "claude", "agents", "--json").Output()
			if err != nil {
				return nil, err
			}
			var a []Agent
			return a, json.Unmarshal(out, &a)
		},
		Now: time.Now,
	}
}

type handler struct {
	env  Env
	in   Input
	p    project.Project
	st   *state.Store
	b    *broker.Broker
	sess state.Session
}

// Run handles one hook event. It always returns exit code 0.
func Run(event string, stdin io.Reader, stdout io.Writer, env Env) int {
	h := &handler{env: env}
	defer func() {
		if r := recover(); r != nil {
			h.fail(event, fmt.Errorf("panic: %v", r))
		}
	}()
	if err := json.NewDecoder(stdin).Decode(&h.in); err != nil {
		h.fail(event, fmt.Errorf("decode input: %w", err))
		return 0
	}
	cwd := h.in.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	h.p = project.Resolve(cwd)
	h.st = state.Open(h.p.StateDir, h.p.Name)
	if env.Now != nil {
		h.st.Now = env.Now
	}
	h.b = broker.New(h.st)

	var out any
	var err error
	switch event {
	case "user-prompt":
		out, err = h.userPrompt()
	case "pre-tool":
		out, err = h.preTool()
	case "post-tool":
		out, err = h.postTool()
	case "stop":
		out, err = h.stop()
	case "stop-failure":
		err = h.stopFailure()
	case "notify":
		err = h.notify()
	default:
		err = fmt.Errorf("unknown event %q", event)
	}
	if err != nil {
		h.fail(event, err)
		return 0
	}
	if out != nil {
		enc := json.NewEncoder(stdout)
		enc.SetEscapeHTML(false)
		enc.Encode(out)
	}
	return 0
}

func (h *handler) fail(event string, err error) {
	dir := h.p.StateDir
	if dir == "" {
		dir = project.Home()
	}
	os.MkdirAll(dir, 0o755)
	f, ferr := os.OpenFile(filepath.Join(dir, "hook-errors.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s %s: %v\n", time.Now().Format(time.RFC3339), event, h.in.SessionID, err)
}

// ---- registry ----

var (
	orchRe   = regexp.MustCompile(`^\s*/(?:orch:)?orch(?:\s|$)`)
	workerRe = regexp.MustCompile(`^\s*/(?:orch:)?worker\s+([A-Za-z0-9_.-]+)`)
)

// userPrompt registers /orch and /worker sessions. Any other prompt (cross-session
// messages too) clears a worker's stall mark, and reminds the orch of stalled workers.
func (h *handler) userPrompt() (any, error) {
	if h.in.SessionID == "" {
		return nil, nil
	}
	if orchRe.MatchString(h.in.Prompt) {
		return nil, h.b.Register(h.in.SessionID, state.Session{Role: "orch", Name: h.p.Name + "-orch"})
	}
	if m := workerRe.FindStringSubmatch(h.in.Prompt); m != nil {
		issue := strings.TrimRight(m[1], ".")
		if err := h.b.Register(h.in.SessionID, state.Session{Role: "worker", Issue: issue, Name: h.p.Name + "-" + issue}); err != nil {
			return nil, err
		}
		return nil, h.b.Unstall(issue)
	}
	ok, err := h.role()
	if err != nil || !ok {
		return nil, err
	}
	if h.sess.Role == "worker" {
		return nil, h.b.Unstall(h.sess.Issue)
	}
	stalled, err := h.b.StalledList()
	if err != nil || len(stalled) == 0 {
		return nil, err
	}
	o := &postOut{}
	o.HookSpecificOutput.HookEventName = "UserPromptSubmit"
	o.HookSpecificOutput.AdditionalContext = "orch: stalled workers — an API error ended their turn, they sent nothing: " +
		strings.Join(stalled, "; ") + ". SendMessage each: \"continue where you stopped\"."
	return o, nil
}

// role finds this session's role; unknown sessions are looked up by name in
// `claude agents --json` (resumed sessions, or ones started before the plugin) and
// the answer is cached, "none" included. Projects without state.json are skipped.
func (h *handler) role() (bool, error) {
	if !h.st.Exists() || h.in.SessionID == "" {
		return false, nil
	}
	sess, ok, err := h.b.Lookup(h.in.SessionID)
	if err != nil {
		return false, err
	}
	if !ok {
		sess = state.Session{Role: "none"}
		if h.env.Agents != nil {
			agents, err := h.env.Agents()
			if err != nil {
				return false, fmt.Errorf("claude agents: %w", err)
			}
			for _, a := range agents {
				if a.SessionID != h.in.SessionID {
					continue
				}
				sess.Name = a.Name
				if a.Name == h.p.Name+"-orch" {
					sess.Role = "orch"
				} else if issue, ok := strings.CutPrefix(a.Name, h.p.Name+"-"); ok && h.onBoard(issue) {
					// only issues on the board: plain sessions get auto names like <project>-a3
					sess.Role, sess.Issue = "worker", issue
				}
			}
		}
		if err := h.b.Register(h.in.SessionID, sess); err != nil {
			return false, err
		}
	}
	h.sess = sess
	return sess.Role == "orch" || sess.Role == "worker", nil
}

func (h *handler) onBoard(issue string) bool {
	found := false
	h.st.View(func(s *state.State) error {
		for _, r := range s.Rows {
			found = found || r.Issue == issue
		}
		return nil
	})
	return issue != "" && found
}

// ---- decisions ----

type preOut struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

func deny(reason string) *preOut {
	o := &preOut{}
	o.HookSpecificOutput.HookEventName = "PreToolUse"
	o.HookSpecificOutput.PermissionDecision = "deny"
	o.HookSpecificOutput.PermissionDecisionReason = reason
	return o
}

type postOut struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func (h *handler) str(k string) string {
	s, _ := h.in.ToolInput[k].(string)
	return s
}

func (h *handler) preTool() (any, error) {
	ok, err := h.role()
	if err != nil || !ok {
		return nil, err
	}
	if h.sess.Role == "orch" {
		return h.orchPre()
	}
	switch h.in.ToolName {
	case "SendMessage":
		// By name, or a reply to the from= socket address (the orch is who messages workers).
		if to := h.str("to"); strings.HasPrefix(to, h.p.Name+"-orch") || strings.HasPrefix(to, "uds:") {
			if out, err := h.toOrch(h.str("message")); out != nil || err != nil {
				return out, err
			}
			return nil, h.b.Spoke(h.sess.Issue)
		}
		return nil, nil
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		path := h.str("file_path")
		if path == "" {
			path = h.str("notebook_path")
		}
		return h.checkEdit(path, "")
	case "Bash":
		return h.workerBash()
	}
	return nil, nil
}

// toOrch gates a worker's message to the orch on the user's hands-on checks: a
// CHECK opens one, and READY … done needs `checks: passed` (the orch ran
// `orchctl checked`) or `checks: none`, never with a CHECK still open.
func (h *handler) toOrch(msg string) (any, error) {
	line, _, _ := strings.Cut(strings.TrimSpace(msg), "\n")
	kind, _, _ := strings.Cut(line, " ")
	issue := h.sess.Issue
	switch kind {
	case "CHECK":
		return nil, h.b.CheckOpen(issue)
	case "READY":
	default:
		return nil, nil
	}
	done, checks := false, ""
	for _, f := range strings.Split(line, "|") {
		f = strings.TrimSpace(f)
		if f == "done" {
			done = true
		}
		if v, ok := strings.CutPrefix(f, "checks:"); ok {
			checks = strings.TrimSpace(v)
		}
	}
	if !done {
		return nil, nil
	}
	state, err := h.b.CheckState(issue)
	if err != nil {
		return nil, err
	}
	switch {
	case state == "open":
		return deny("your CHECK is still open: wait for the user's answer (A <Qn>). Something failed → fix it and send a new CHECK. " +
			"READY … done only after the orch marks the check passed"), nil
	case checks == "passed" && state != "passed":
		return deny("no passed CHECK on record: send CHECK with what the user should try, and wait for the answer"), nil
	case checks != "passed" && checks != "none":
		return deny("READY … done needs a checks field: `| checks: passed` (the user confirmed your CHECK) or " +
			"`| checks: none` (nothing for the user to try by hand)"), nil
	}
	return nil, nil
}

var imageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true, ".heic": true, ".tiff": true}

// orchPre: the orch reads only its state dir; repo files and images go to workers.
func (h *handler) orchPre() (any, error) {
	if h.in.ToolName != "Read" {
		return nil, nil
	}
	path := h.str("file_path")
	if path == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(h.in.Cwd, path)
	}
	if inside(path, h.p.StateDir) {
		return nil, nil
	}
	if imageExt[strings.ToLower(filepath.Ext(path))] {
		return deny("orch: no images in the orch context — send a pointer to the worker instead"), nil
	}
	if _, in := h.p.Rel(path); in {
		return deny("orch: no repo reads in the orch context — send a pointer to the worker instead"), nil
	}
	return nil, nil
}

func inside(path, dir string) bool {
	r, err := filepath.Rel(dir, path)
	return err == nil && r != ".." && !strings.HasPrefix(r, "../")
}

// checkEdit denies an edit of a project file this worker doesn't hold, or any
// project edit while paused. how names the Bash form for the reason.
func (h *handler) checkEdit(path, how string) (any, error) {
	if path == "" {
		return nil, nil
	}
	rel, in := h.p.Rel(path)
	if !in {
		return nil, nil
	}
	issue := h.sess.Issue
	var paused, held bool
	err := h.st.View(func(s *state.State) error {
		paused = s.Paused[issue]
		held = broker.Holds(s, issue, state.Claim{Kind: "file", Item: rel})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if paused {
		return deny(fmt.Sprintf("%s is paused by the orch: no edits until resumed", issue)), nil
	}
	if !held {
		return deny(fmt.Sprintf("claim first%s: orchctl claim %s %s", how, issue, rel)), nil
	}
	return nil, nil
}

func (h *handler) workerBash() (any, error) {
	issue := h.sess.Issue
	dir := h.in.Cwd // "" once a cd goes somewhere we can't work out
	vars := shellVars{"PWD": &dir}
	for _, seg := range splitShell(h.str("command")) {
		if vars.assign(seg) {
			continue
		}
		if len(seg.args) >= 1 && seg.args[0] == "cd" {
			if len(seg.args) > 1 {
				if t, ok := vars.expand(seg.args[1]); ok && (dir != "" || filepath.IsAbs(t) || strings.HasPrefix(t, "~/")) {
					dir = h.abs(dir, t)
				} else {
					dir = ""
				}
			}
			d := dir
			vars["PWD"] = &d
			if dir == "" {
				vars["PWD"] = nil
			}
			continue
		}
		if len(seg.args) >= 2 && filepath.Base(seg.args[0]) == "orchctl" && seg.args[1] == "wait" {
			if err := h.b.Spoke(issue); err != nil {
				return nil, err
			}
			continue
		}
		for _, t := range editTargets(seg) {
			x, ok := vars.expand(t)
			if !ok || dir == "" && !filepath.IsAbs(x) && !strings.HasPrefix(x, "~/") {
				return deny(fmt.Sprintf("can't tell which file %q is: use a literal path", t)), nil
			}
			if out, err := h.checkEdit(h.abs(dir, x), " (Bash edits count too)"); out != nil || err != nil {
				return out, err
			}
		}
		g, ok := parseGit(seg)
		if !ok {
			continue
		}
		gdir := dir
		if g.dir != "" {
			gdir = h.abs(dir, g.dir)
		}
		if !filepath.IsAbs(gdir) || project.Resolve(gdir).Root != h.p.Root {
			continue // another repo, or somewhere we can't work out
		}
		switch {
		case g.stagesAll():
			return deny("stage only your files, by path: git add <file>…"), nil
		case g.commitsAll():
			return deny("commit only your files, by path: git commit -- <file>…"), nil
		case g.sub == "commit":
			// A token is the orch's explicit go, so it overrides a pause (handover:
			// pause → READY → commit-go).
			var tokens int
			if err := h.st.View(func(s *state.State) error {
				tokens = s.Tokens[issue]
				return nil
			}); err != nil {
				return nil, err
			}
			if tokens <= 0 {
				return deny(fmt.Sprintf("wait for the orch: orchctl wait %s commit (run in background)", issue)), nil
			}
			if err := h.b.SetHead(issue, h.env.Head(gdir)); err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}

func (h *handler) abs(dir, p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(dir, p)
}

// postTool: after a worker's git commit lands, use the token, release its claims,
// log the sha and put it on the board; the worker then tells the orch (COMMITTED).
func (h *handler) postTool() (any, error) {
	if h.in.ToolName != "Bash" {
		return nil, nil
	}
	ok, err := h.role()
	if err != nil || !ok || h.sess.Role != "worker" {
		return nil, err
	}
	dir := h.in.Cwd
	for _, seg := range splitShell(h.str("command")) {
		if len(seg.args) > 1 && seg.args[0] == "cd" {
			dir = h.abs(dir, seg.args[1])
			continue
		}
		g, ok := parseGit(seg)
		if !ok || g.sub != "commit" {
			continue
		}
		gdir := dir
		if g.dir != "" {
			gdir = h.abs(dir, g.dir)
		}
		if project.Resolve(gdir).Root != h.p.Root {
			continue
		}
		sha, err := h.b.Committed(h.sess.Issue, h.env.Head(gdir))
		if err != nil || sha == "" {
			return nil, err
		}
		o := &postOut{}
		o.HookSpecificOutput.HookEventName = "PostToolUse"
		o.HookSpecificOutput.AdditionalContext = fmt.Sprintf("orch: commit %[2]s logged, claims released. Now tell the orch: COMMITTED %[1]s: %[2]s", h.sess.Issue, sha)
		return o, nil
	}
	return nil, nil
}

// ---- turn ends ----

type stopOut struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// stop: a worker that ends its turn holding claims without a word to the orch
// (or a wait) is sent back once — otherwise the orch never hears of it.
func (h *handler) stop() (any, error) {
	ok, err := h.role()
	if err != nil || !ok || h.sess.Role != "worker" {
		return nil, err
	}
	silent, sha, err := h.b.TurnEnd(h.sess.Issue)
	if err != nil || !silent || h.in.StopHookActive {
		return nil, err
	}
	if sha != "" {
		return &stopOut{Decision: "block", Reason: fmt.Sprintf(
			"orch: you committed %[2]s but haven't told the orch, so it still thinks the commit is pending. "+
				"Send it: COMMITTED %[1]s: %[2]s", h.sess.Issue, sha)}, nil
	}
	return &stopOut{Decision: "block", Reason: fmt.Sprintf(
		"orch: you hold claims and sent the orch nothing this turn, so it won't know. "+
			"Done → READY + orchctl wait %[1]s commit; need a decision → Q; blocked → HELD + orchctl wait %[1]s go; "+
			"not finished → keep going. If you were only talking with the user directly, end your turn again.", h.sess.Issue)}, nil
}

// notify: a worker's own terminal alerts are off (worker-settings.json), so a
// permission prompt — the one thing it needs the user for — is raised here.
func (h *handler) notify() error {
	ok, err := h.role()
	if err != nil || !ok || h.sess.Role != "worker" {
		return err
	}
	if h.env.Notify != nil {
		h.env.Notify("orch", h.sess.Name+": "+h.in.Message)
	}
	return nil
}

// stopFailure: an API error ended the turn (no Stop hook runs). Mark the worker
// stalled for the orch and tell the user.
func (h *handler) stopFailure() error {
	ok, err := h.role()
	if err != nil || !ok {
		return err
	}
	why := h.in.Error
	if why == "" {
		why = "API error"
	}
	if h.sess.Role == "worker" {
		if err := h.b.Stall(h.sess.Issue, why); err != nil {
			return err
		}
	}
	if h.env.Notify != nil {
		h.env.Notify("orch", h.sess.Name+" stopped: "+why)
	}
	return nil
}
