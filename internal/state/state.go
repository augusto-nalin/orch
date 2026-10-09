// Package state holds a project's orch state in state.json, guarded by an exclusive
// flock on state.lock for every read-modify-write. board.md and questions.md are
// rendered from it after each write; log.md is append-only.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const Version = 1

type Row struct {
	Issue   string `json:"issue"`
	Session string `json:"session"`
	Status  string `json:"status"`
	Files   string `json:"files,omitempty"`
	Outcome string `json:"outcome,omitempty"`
	Updated string `json:"updated"`
}

// Claim is a file (path relative to the checkout top; a trailing "/" covers a
// directory, a bare name matches that basename anywhere) or a resource ("build").
type Claim struct {
	Item  string `json:"item"`
	Kind  string `json:"kind"` // "file" or "res"
	Issue string `json:"issue"`
	Since string `json:"since"`
}

type Question struct {
	ID       int    `json:"id,omitempty"` // 0 for imported free-form lines
	Issue    string `json:"issue,omitempty"`
	Text     string `json:"text,omitempty"`
	Asked    string `json:"asked,omitempty"`
	Answered string `json:"answered,omitempty"` // date; empty while open
	Note     string `json:"note,omitempty"`
	Raw      string `json:"raw,omitempty"` // imported line, rendered verbatim
}

type Session struct {
	Role  string `json:"role"` // "orch" or "worker"
	Issue string `json:"issue,omitempty"`
	Name  string `json:"name,omitempty"`
}

type State struct {
	Version    int                 `json:"version"`
	Rows       []Row               `json:"rows"`
	Claims     []Claim             `json:"claims"`
	Pending    map[string][]Claim  `json:"pending,omitempty"`   // issue → its refused request, granted whole or by handoff
	Contested  map[string][]string `json:"contested,omitempty"` // item key → issues refused, awaiting `orchctl order`
	Queues     map[string][]string `json:"queues,omitempty"`    // item key → issues in the order the orch set
	Tokens     map[string]int      `json:"tokens,omitempty"`    // issue → commit tokens
	Paused     map[string]bool     `json:"paused,omitempty"`
	Questions  []Question          `json:"questions"`
	NextQ      int                 `json:"next_q"`
	Sessions   map[string]Session  `json:"sessions,omitempty"`   // session_id → role
	Heads      map[string]string   `json:"heads,omitempty"`      // issue → HEAD before its pending git commit
	Stalled    map[string]string   `json:"stalled,omitempty"`    // issue → why its last turn died (API error), until it runs again
	Spoke      map[string]bool     `json:"spoke,omitempty"`      // issue → messaged the orch or started a wait this turn
	Unreported map[string]string   `json:"unreported,omitempty"` // issue → sha of a commit it hasn't told the orch about yet
}

func New() *State {
	return &State{Version: Version, NextQ: 1}
}

func (s *State) init() {
	if s.Pending == nil {
		s.Pending = map[string][]Claim{}
	}
	if s.Contested == nil {
		s.Contested = map[string][]string{}
	}
	if s.Queues == nil {
		s.Queues = map[string][]string{}
	}
	if s.Tokens == nil {
		s.Tokens = map[string]int{}
	}
	if s.Paused == nil {
		s.Paused = map[string]bool{}
	}
	if s.Sessions == nil {
		s.Sessions = map[string]Session{}
	}
	if s.Heads == nil {
		s.Heads = map[string]string{}
	}
	if s.Stalled == nil {
		s.Stalled = map[string]string{}
	}
	if s.Spoke == nil {
		s.Spoke = map[string]bool{}
	}
	if s.Unreported == nil {
		s.Unreported = map[string]string{}
	}
	if s.NextQ < 1 {
		s.NextQ = 1
	}
}

// Store is one project's state dir.
type Store struct {
	Dir     string
	Project string
	Now     func() time.Time
}

func Open(dir, project string) *Store {
	return &Store{Dir: dir, Project: project, Now: time.Now}
}

func (st *Store) Today() string { return st.Now().Format("2006-01-02") }

func (st *Store) path(name string) string { return filepath.Join(st.Dir, name) }

// Exists reports whether state.json has been created.
func (st *Store) Exists() bool {
	_, err := os.Stat(st.path("state.json"))
	return err == nil
}

func (st *Store) lock(how int) (func(), error) {
	if err := os.MkdirAll(filepath.Join(st.Dir, "issues"), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(st.path("state.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		return nil, err
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

func (st *Store) load() (*State, error) {
	b, err := os.ReadFile(st.path("state.json"))
	if errors.Is(err, os.ErrNotExist) {
		s := New()
		s.init()
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	s := &State{}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("state.json: %w", err)
	}
	s.init()
	return s, nil
}

func (st *Store) save(s *State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(st.path("state.json"), append(b, '\n')); err != nil {
		return err
	}
	if err := writeAtomic(st.path("board.md"), []byte(RenderBoard(s))); err != nil {
		return err
	}
	return writeAtomic(st.path("questions.md"), []byte(RenderQuestions(s)))
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// View runs fn on the current state under a shared lock.
func (st *Store) View(fn func(*State) error) error {
	unlock, err := st.lock(syscall.LOCK_SH)
	if err != nil {
		return err
	}
	defer unlock()
	s, err := st.load()
	if err != nil {
		return err
	}
	return fn(s)
}

// Update runs fn under the exclusive lock and saves if it returns nil. Lines fn
// passes to logf are appended to log.md after the save, still under the lock.
func (st *Store) Update(fn func(s *State, logf func(string, ...any)) error) error {
	unlock, err := st.lock(syscall.LOCK_EX)
	if err != nil {
		return err
	}
	defer unlock()
	s, err := st.load()
	if err != nil {
		return err
	}
	var lines []string
	logf := func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }
	if err := fn(s, logf); err != nil {
		return err
	}
	if err := st.save(s); err != nil {
		return err
	}
	return st.appendLog(lines...)
}

// Log appends dated lines to log.md.
func (st *Store) Log(lines ...string) error {
	unlock, err := st.lock(syscall.LOCK_EX)
	if err != nil {
		return err
	}
	defer unlock()
	return st.appendLog(lines...)
}

func (st *Store) appendLog(lines ...string) error {
	if len(lines) == 0 {
		return nil
	}
	p := st.path("log.md")
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(p, []byte("# Log\n\n"), 0o644); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	today := st.Today()
	for _, l := range lines {
		if _, err := fmt.Fprintf(f, "%s %s\n", today, l); err != nil {
			return err
		}
	}
	return nil
}
