// Package broker implements the mechanical steps of the orch protocol on top of
// state: claims and contention, commit tokens, pause flags, the board, the
// question index and the session registry. Every call is one locked update.
//
// Contention rule: the broker never hands a contested item to anyone on its own.
// A refused request is recorded as contested; the orch sets the turn order with
// Order, and releases then pass the item down that queue.
package broker

import (
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"orch/internal/state"
)

type Broker struct {
	St   *state.Store
	Poll time.Duration // Wait poll interval
}

func New(st *state.Store) *Broker { return &Broker{St: st, Poll: 500 * time.Millisecond} }

type logf = func(string, ...any)

// ---- claims ----

// overlaps reports whether two claims cover a common file or the same resource.
func overlaps(a, b state.Claim) bool {
	if a.Kind != b.Kind {
		return false
	}
	if a.Kind == "res" {
		return a.Item == b.Item
	}
	return covers(a.Item, b.Item) || covers(b.Item, a.Item)
}

// covers: equal paths; a directory claim ("dir/") covers everything under it; a
// bare name (no "/", as in imported claims) covers that basename anywhere.
func covers(c, p string) bool {
	switch {
	case c == p:
		return true
	case strings.HasSuffix(c, "/"):
		return strings.HasPrefix(p, c) || p+"/" == c
	case !strings.Contains(c, "/"):
		return path.Base(strings.TrimSuffix(p, "/")) == c
	}
	return false
}

// holder returns who keeps issue from taking c: another issue's overlapping claim,
// or "orch" when the item is free but contested or queued for someone else.
func holder(s *state.State, c state.Claim, issue string) string {
	for _, h := range s.Claims {
		if h.Issue != issue && overlaps(h, c) {
			return h.Issue
		}
	}
	for k, q := range s.Queues {
		if len(q) > 0 && q[0] != issue && overlaps(keyClaim(k), c) {
			return q[0] + " (queued)"
		}
	}
	for k, q := range s.Contested {
		if len(q) > 0 && slices.ContainsFunc(q, func(i string) bool { return i != issue }) && overlaps(keyClaim(k), c) {
			return "orch (contested)"
		}
	}
	return ""
}

func keyClaim(k string) state.Claim {
	if strings.HasPrefix(k, "res:") {
		return state.Claim{Kind: "res", Item: state.KeyItem(k)}
	}
	return state.Claim{Kind: "file", Item: k}
}

func holds(s *state.State, issue string, c state.Claim) bool {
	return slices.ContainsFunc(s.Claims, func(h state.Claim) bool {
		return h.Issue == issue && h.Kind == c.Kind && covers(h.Item, c.Item)
	})
}

func addClaim(s *state.State, c state.Claim) {
	if !holds(s, c.Issue, c) {
		s.Claims = append(s.Claims, c)
	}
}

// Claim requests items (kind "file" or "res") for issue, all or nothing. It prints
// "GO", or "HELD <item> by <holder>[; …]" and records the request as contested.
func (b *Broker) Claim(issue, kind string, items []string) (string, error) {
	var out string
	err := b.St.Update(func(s *state.State, log logf) error {
		today := b.St.Today()
		var want []state.Claim
		var held []string
		for _, it := range items {
			c := state.Claim{Item: it, Kind: kind, Issue: issue, Since: today}
			want = append(want, c)
			if h := holder(s, c, issue); h != "" {
				held = append(held, it+" by "+h)
				k := c.Key()
				if !slices.Contains(s.Queues[k], issue) && !slices.Contains(s.Contested[k], issue) {
					s.Contested[k] = append(s.Contested[k], issue)
				}
			}
		}
		if len(held) > 0 {
			s.Pending[issue] = mergeClaims(s.Pending[issue], want)
			out = "HELD " + strings.Join(held, "; ")
			log("%s %s %s → %s", issue, verb(kind), strings.Join(items, ", "), out)
			return nil
		}
		for _, c := range want {
			addClaim(s, c)
			s.Contested[c.Key()] = remove(s.Contested[c.Key()], issue)
		}
		prune(s)
		delete(s.Pending, issue)
		out = "GO"
		log("%s %s %s → GO", issue, verb(kind), strings.Join(items, ", "))
		return nil
	})
	return out, err
}

func verb(kind string) string {
	if kind == "res" {
		return "need"
	}
	return "claim"
}

func mergeClaims(have, add []state.Claim) []state.Claim {
	for _, c := range add {
		if !slices.ContainsFunc(have, func(h state.Claim) bool { return h.Key() == c.Key() }) {
			have = append(have, c)
		}
	}
	return have
}

// Release drops issue's claims on items, or all of them (and its waiting requests)
// when items is empty. Queued items pass to the next issue in the orch's order.
func (b *Broker) Release(issue string, items []string) (string, error) {
	var out string
	err := b.St.Update(func(s *state.State, log logf) error {
		out = release(s, b.St.Today(), issue, items, log)
		return nil
	})
	return out, err
}

func release(s *state.State, today, issue string, items []string, log logf) string {
	var dropped []string
	s.Claims = slices.DeleteFunc(s.Claims, func(c state.Claim) bool {
		if c.Issue != issue || len(items) > 0 && !slices.Contains(items, c.Item) {
			return false
		}
		dropped = append(dropped, c.Item)
		return true
	})
	if len(items) == 0 {
		delete(s.Pending, issue)
		for k := range s.Contested {
			s.Contested[k] = remove(s.Contested[k], issue)
		}
		for k := range s.Queues {
			s.Queues[k] = remove(s.Queues[k], issue)
		}
		prune(s)
	}
	if len(dropped) == 0 {
		return "ok"
	}
	parts := []string{"ok"}
	parts = append(parts, handoffs(s, today, log)...)
	for _, k := range sortedKeys(s.Contested) {
		if ownerOf(s, keyClaim(k)) == "" {
			parts = append(parts, state.KeyItem(k)+" free, contested — needs orch order")
		}
	}
	log("%s release %s", issue, strings.Join(dropped, ", "))
	return strings.Join(parts, "; ")
}

func ownerOf(s *state.State, c state.Claim) string {
	for _, h := range s.Claims {
		if overlaps(h, c) {
			return h.Issue
		}
	}
	return ""
}

// handoffs grants every free queued item to the head of its queue.
func handoffs(s *state.State, today string, log logf) []string {
	var out []string
	for _, k := range sortedKeys(s.Queues) {
		q := s.Queues[k]
		if len(q) == 0 {
			continue
		}
		head := q[0]
		c := keyClaim(k)
		if o := ownerOf(s, c); o != "" && o != head {
			continue
		}
		c.Issue, c.Since = head, today
		if p := pendingFor(s, head, k); p != nil {
			c.Item = p.Item
		}
		addClaim(s, c)
		s.Queues[k] = q[1:]
		grantPending(s, today, head)
		out = append(out, state.KeyItem(k)+" → "+head)
		log("%s → %s (orch order)", state.KeyItem(k), head)
	}
	prune(s)
	return out
}

func pendingFor(s *state.State, issue, key string) *state.Claim {
	for i, p := range s.Pending[issue] {
		if p.Key() == key {
			return &s.Pending[issue][i]
		}
	}
	return nil
}

// grantPending gives issue the rest of its refused request where now free and
// uncontested; once it holds all of it, the request is done (wait go → GO).
func grantPending(s *state.State, today, issue string) {
	all := true
	for _, p := range s.Pending[issue] {
		if holds(s, issue, p) {
			continue
		}
		if holder(s, p, issue) != "" {
			all = false
			continue
		}
		p.Issue, p.Since = issue, today
		addClaim(s, p)
		s.Contested[p.Key()] = remove(s.Contested[p.Key()], issue)
	}
	if all {
		delete(s.Pending, issue)
	}
}

// Order sets the turn order for a contested item. The first issue gets it now if
// it is free; the rest get it in order as it is released.
func (b *Broker) Order(item string, issues []string) (string, error) {
	var out string
	err := b.St.Update(func(s *state.State, log logf) error {
		k := item
		if _, ok := s.Contested["res:"+item]; ok {
			k = "res:" + item
		} else if _, ok := s.Queues["res:"+item]; ok {
			k = "res:" + item
		}
		owner := ownerOf(s, keyClaim(k))
		var q []string
		for _, i := range issues {
			s.Contested[k] = remove(s.Contested[k], i)
			if i != owner && !slices.Contains(q, i) {
				q = append(q, i)
			}
		}
		s.Queues[k] = q
		log("orch order %s: %s", item, strings.Join(issues, ", "))
		if h := handoffs(s, b.St.Today(), log); len(h) > 0 {
			out = "GO " + strings.Join(h, "; ")
		} else if owner != "" {
			out = "queued after " + owner
		} else {
			out = "queued"
		}
		prune(s)
		return nil
	})
	return out, err
}

// ---- waits, tokens, pause ----

var ErrTimeout = errors.New("timeout")

// Wait blocks until issue's claim/need request is granted ("go" → "GO") or it has a
// commit token ("commit" → "COMMIT"). timeout 0 waits forever.
func (b *Broker) Wait(issue, what string, timeout time.Duration) (string, error) {
	if what != "go" && what != "commit" {
		return "", fmt.Errorf("wait: want go|commit, got %q", what)
	}
	deadline := time.Now().Add(timeout)
	for {
		var done bool
		err := b.St.View(func(s *state.State) error {
			if what == "go" {
				done = len(s.Pending[issue]) == 0
			} else {
				done = s.Tokens[issue] > 0
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		if done {
			return strings.ToUpper(what), nil
		}
		if timeout > 0 && time.Now().After(deadline) {
			return "", ErrTimeout
		}
		time.Sleep(b.Poll)
	}
}

func (b *Broker) CommitGo(issue string, n int) (string, error) {
	if n < 1 {
		n = 1
	}
	return "ok", b.St.Update(func(s *state.State, log logf) error {
		s.Tokens[issue] += n
		log("%s commit-go ×%d", issue, n)
		return nil
	})
}

// Consume takes one commit token; false if issue had none.
func (b *Broker) Consume(issue string) (bool, error) {
	var ok bool
	err := b.St.Update(func(s *state.State, log logf) error {
		if s.Tokens[issue] > 0 {
			s.Tokens[issue]--
			if s.Tokens[issue] == 0 {
				delete(s.Tokens, issue)
			}
			ok = true
		}
		return nil
	})
	return ok, err
}

func (b *Broker) SetPaused(issue string, on bool) (string, error) {
	return "ok", b.St.Update(func(s *state.State, log logf) error {
		if on {
			s.Paused[issue] = true
			log("%s paused", issue)
		} else {
			delete(s.Paused, issue)
			log("%s resumed", issue)
		}
		return nil
	})
}

// ---- board, questions, log ----

// Row adds or updates a board row. done/dropped also releases the issue's claims
// and clears its requests, tokens and pause flag.
func (b *Broker) Row(issue, status, outcome string) (string, error) {
	out := "ok"
	err := b.St.Update(func(s *state.State, log logf) error {
		today := b.St.Today()
		i := slices.IndexFunc(s.Rows, func(r state.Row) bool { return r.Issue == issue })
		if i < 0 {
			s.Rows = append(s.Rows, state.Row{Issue: issue, Session: b.St.Project + "-" + issue})
			i = len(s.Rows) - 1
		}
		r := &s.Rows[i]
		r.Status, r.Updated = status, today
		if outcome != "" {
			r.Outcome = outcome
		}
		if outcome != "" {
			log("%s %s: %s", issue, status, outcome)
		} else {
			log("%s %s", issue, status)
		}
		if status == "done" || status == "dropped" {
			out = release(s, today, issue, nil, log)
			delete(s.Tokens, issue)
			delete(s.Paused, issue)
		}
		return nil
	})
	return out, err
}

func (b *Broker) Q(issue, text string) (string, error) {
	var id int
	err := b.St.Update(func(s *state.State, log logf) error {
		id = s.NextQ
		s.NextQ++
		s.Questions = append(s.Questions, state.Question{ID: id, Issue: issue, Text: text, Asked: b.St.Today()})
		log("Q%d %s: %s", id, issue, text)
		return nil
	})
	return fmt.Sprintf("Q%d", id), err
}

// A marks question qid ("Q12", "[Q12]" or "12") answered. The verbatim answer
// lives in the worker's issue file; note is a short gist.
func (b *Broker) A(qid, note string) (string, error) {
	id := 0
	fmt.Sscanf(strings.TrimLeft(strings.Trim(qid, "[]"), "Qq"), "%d", &id)
	return "ok", b.St.Update(func(s *state.State, log logf) error {
		i := slices.IndexFunc(s.Questions, func(q state.Question) bool { return q.ID == id && q.Answered == "" })
		if i < 0 {
			return fmt.Errorf("no open Q%d", id)
		}
		q := &s.Questions[i]
		q.Answered, q.Note = b.St.Today(), note
		if q.Raw != "" {
			q.Raw += " → answered " + q.Answered
			if note != "" {
				q.Raw += " — " + note
			}
		}
		if note != "" {
			log("Q%d answered — %s", id, note)
		} else {
			log("Q%d answered", id)
		}
		return nil
	})
}

func (b *Broker) Log(text string) (string, error) { return "ok", b.St.Log(text) }

// ---- registry ----

func (b *Broker) Register(sessionID string, sess state.Session) error {
	return b.St.Update(func(s *state.State, log logf) error {
		if old, ok := s.Sessions[sessionID]; ok && old == sess {
			return nil
		}
		s.Sessions[sessionID] = sess
		log("session %s registered as %s %s", short(sessionID), sess.Role, sess.Issue)
		return nil
	})
}

func (b *Broker) Lookup(sessionID string) (state.Session, bool, error) {
	var sess state.Session
	var ok bool
	err := b.St.View(func(s *state.State) error {
		sess, ok = s.Sessions[sessionID]
		return nil
	})
	return sess, ok, err
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// ---- views ----

// Full is the orch's startup view (same content as orch-state.sh full).
func (b *Broker) Full(root string) (string, error) {
	var w strings.Builder
	err := b.St.View(func(s *state.State) error {
		fmt.Fprintf(&w, "project: %s\nmain checkout: %s\nstate dir: %s\norchestrator session name: %s-orch\n\n",
			b.St.Project, root, b.St.Dir, b.St.Project)
		w.WriteString("## Board (not done/dropped)\n| issue | session | status | files | last outcome | updated |\n|---|---|---|---|---|---|\n")
		hidden := 0
		for _, r := range s.Rows {
			if r.Status == "done" || r.Status == "dropped" {
				hidden++
				continue
			}
			fmt.Fprintf(&w, "| %s | %s | %s | %s | %s | %s |\n", r.Issue, r.Session, r.Status, r.Files, r.Outcome, r.Updated)
		}
		fmt.Fprintf(&w, "(%d done/dropped rows hidden — see board.md)\n\n## Claims\n", hidden)
		for _, c := range s.Claims {
			fmt.Fprintf(&w, "- %s — %s (since %s)\n", c.Item, c.Issue, c.Since)
		}
		w.WriteString("\n" + state.RenderWaiting(s))
		var flags []string
		for _, i := range sortedKeys(s.Paused) {
			flags = append(flags, i+" paused")
		}
		for _, i := range sortedKeys(s.Tokens) {
			flags = append(flags, fmt.Sprintf("%s commit-go ×%d", i, s.Tokens[i]))
		}
		if len(flags) > 0 {
			w.WriteString("\n## Flags\n- " + strings.Join(flags, "\n- ") + "\n")
		}
		w.WriteString("\n## Open questions\n")
		for _, q := range s.Questions {
			if q.Answered == "" {
				w.WriteString(q.Line() + "\n")
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	w.WriteString("\n## Last log entries\n")
	if l, err := os.ReadFile(b.St.Dir + "/log.md"); err == nil {
		lines := strings.Split(strings.TrimRight(string(l), "\n"), "\n")
		w.WriteString(strings.Join(lines[max(0, len(lines)-10):], "\n") + "\n")
	}
	return w.String(), nil
}

// ---- helpers ----

func remove(q []string, x string) []string {
	return slices.DeleteFunc(q, func(i string) bool { return i == x })
}

func prune(s *state.State) {
	for k, q := range s.Contested {
		if len(q) == 0 {
			delete(s.Contested, k)
		}
	}
	for k, q := range s.Queues {
		if len(q) == 0 {
			delete(s.Queues, k)
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}
