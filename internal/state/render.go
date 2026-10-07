package state

import (
	"fmt"
	"sort"
	"strings"
)

// Key identifies a claimable item in Contested/Queues: files by path, resources
// prefixed with "res:" so a resource never collides with a file of the same name.
func (c Claim) Key() string { return ItemKey(c.Kind, c.Item) }

func ItemKey(kind, item string) string {
	if kind == "res" {
		return "res:" + item
	}
	return item
}

// KeyItem is the display name of a key.
func KeyItem(key string) string { return strings.TrimPrefix(key, "res:") }

func RenderBoard(s *State) string {
	var b strings.Builder
	b.WriteString("# Board\n\n| issue | session | status | files | last outcome | updated |\n|---|---|---|---|---|---|\n")
	for _, r := range s.Rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", r.Issue, r.Session, r.Status, r.Files, r.Outcome, r.Updated)
	}
	b.WriteString("\n## Claims\n")
	for _, c := range s.Claims {
		fmt.Fprintf(&b, "- %s — %s (since %s)\n", c.Item, c.Issue, c.Since)
	}
	b.WriteString("\n" + RenderWaiting(s))
	return b.String()
}

// RenderWaiting lists ordered queues, then contested items awaiting `orch order`.
func RenderWaiting(s *State) string {
	var b strings.Builder
	b.WriteString("## Waiting (turn order)\n")
	n := 0
	for _, k := range sortedKeys(s.Queues) {
		if q := s.Queues[k]; len(q) > 0 {
			fmt.Fprintf(&b, "- %s → %s\n", KeyItem(k), strings.Join(q, ", "))
			n++
		}
	}
	for _, k := range sortedKeys(s.Contested) {
		if q := s.Contested[k]; len(q) > 0 {
			fmt.Fprintf(&b, "- %s contested by %s — needs orch order\n", KeyItem(k), strings.Join(q, ", "))
			n++
		}
	}
	if n == 0 {
		b.WriteString("- none\n")
	}
	return b.String()
}

func RenderQuestions(s *State) string {
	var b strings.Builder
	b.WriteString("# Questions\n\n## Open\n")
	for _, q := range s.Questions {
		if q.Answered == "" {
			b.WriteString(q.Line() + "\n")
		}
	}
	b.WriteString("\n## Answered\n")
	for _, q := range s.Questions {
		if q.Answered != "" {
			b.WriteString(q.Line() + "\n")
		}
	}
	return b.String()
}

// Line is the questions.md line (same format as orch-state.sh).
func (q Question) Line() string {
	if q.Raw != "" {
		return q.Raw
	}
	if q.Answered == "" {
		return fmt.Sprintf("- [Q%d] %s: %s (asked %s)", q.ID, q.Issue, q.Text, q.Asked)
	}
	l := fmt.Sprintf("- [Q%d] %s: answered %s", q.ID, q.Issue, q.Answered)
	if q.Note != "" {
		l += " — " + q.Note
	}
	return l + fmt.Sprintf(" → issues/%s.md#Q%d", q.Issue, q.ID)
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
