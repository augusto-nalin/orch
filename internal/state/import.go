package state

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var (
	claimRe = regexp.MustCompile(`^- (.+?) — (\S+) \(since (.*)\)$`)
	openQRe = regexp.MustCompile(`^- \[Q(\d+)\] (\S+?): (.*) \(asked (\S+)\)$`)
	qIDRe   = regexp.MustCompile(`\[Q(\d+)\]`)
)

// Parse builds a State from the markdown files written by orch-state.sh. Board
// rows and claims are parsed; question lines that aren't a plain open question
// are kept verbatim (Raw). Answered ones are marked "imported".
func Parse(board, questions string) *State {
	s := New()
	s.init()
	section := ""
	for _, l := range strings.Split(board, "\n") {
		switch {
		case strings.HasPrefix(l, "## "):
			section = strings.TrimSpace(l[3:])
		case strings.HasPrefix(l, "| ") && section == "":
			c := strings.Split(l, "|")
			if len(c) < 8 {
				continue
			}
			for i := range c {
				c[i] = strings.TrimSpace(c[i])
			}
			if c[1] == "issue" || strings.HasPrefix(c[1], "---") {
				continue
			}
			s.Rows = append(s.Rows, Row{Issue: c[1], Session: c[2], Status: c[3], Files: c[4], Outcome: c[5], Updated: c[6]})
		case section == "Claims":
			if m := claimRe.FindStringSubmatch(l); m != nil {
				kind := "file"
				if !strings.ContainsAny(m[1], "./") {
					kind = "res"
				}
				s.Claims = append(s.Claims, Claim{Item: m[1], Kind: kind, Issue: m[2], Since: m[3]})
			}
		}
	}

	section = ""
	maxQ := 0
	for _, l := range strings.Split(questions, "\n") {
		for _, m := range qIDRe.FindAllStringSubmatch(l, -1) {
			if n, _ := strconv.Atoi(m[1]); n > maxQ {
				maxQ = n
			}
		}
		switch {
		case strings.HasPrefix(l, "## "):
			section = strings.TrimSpace(l[3:])
		case strings.HasPrefix(l, "# "), section == "":
		case strings.HasPrefix(l, "- "):
			if m := openQRe.FindStringSubmatch(l); section == "Open" && m != nil && !strings.Contains(l, " → ") {
				id, _ := strconv.Atoi(m[1])
				s.Questions = append(s.Questions, Question{ID: id, Issue: m[2], Text: m[3], Asked: m[4]})
				continue
			}
			q := Question{Raw: l, Answered: "imported"}
			if m := qIDRe.FindStringSubmatch(l); m != nil {
				q.ID, _ = strconv.Atoi(m[1])
			}
			if section == "Open" && !strings.Contains(l, " → ") {
				q.Answered = "" // open, but not in the plain format: kept verbatim
			}
			s.Questions = append(s.Questions, q)
		case strings.TrimSpace(l) != "" && len(s.Questions) > 0:
			// continuation of a multi-line entry
			last := &s.Questions[len(s.Questions)-1]
			if last.Raw == "" {
				last.Raw = last.Line()
			}
			last.Raw += "\n" + l
		}
	}
	s.NextQ = maxQ + 1
	return s
}

// Import parses board.md and questions.md in the state dir into state.json. The
// originals are kept as *.pre-import. Refuses if state.json exists, unless force.
func (st *Store) Import(force bool) (*State, error) {
	var out *State
	err := st.Update(func(s *State, logf func(string, ...any)) error {
		if st.Exists() && !force {
			return errors.New("state.json exists (use --force)")
		}
		board, err := os.ReadFile(st.path("board.md"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		qs, err := os.ReadFile(st.path("questions.md"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		for name, b := range map[string][]byte{"board.md": board, "questions.md": qs} {
			if b != nil {
				if err := os.WriteFile(st.path(name+".pre-import"), b, 0o644); err != nil {
					return err
				}
			}
		}
		*s = *Parse(string(board), string(qs))
		out = s
		logf("orchctl import: %d rows, %d claims, %d questions", len(s.Rows), len(s.Claims), len(s.Questions))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("import: %w", err)
	}
	return out, nil
}
