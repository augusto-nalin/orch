package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixtureStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{"board.md", "questions.md"} {
		b, err := os.ReadFile(filepath.Join("testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st := Open(dir, "demo")
	st.Now = func() time.Time { return time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC) }
	return st
}

func TestImport(t *testing.T) {
	st := fixtureStore(t)
	s, err := st.Import(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Rows) != 3 || s.Rows[1].Issue != "cache-layer" || s.Rows[1].Session != "demo-cache-layer" ||
		s.Rows[0].Outcome != "1111111 + 2222222; verified (Q3)" || s.Rows[2].Files != "—" {
		t.Fatalf("rows %+v", s.Rows)
	}
	kinds := map[string]string{}
	for _, c := range s.Claims {
		kinds[c.Item] = c.Kind
		if c.Issue != "cache-layer" || c.Since != "2026-10-01" {
			t.Fatalf("claim %+v", c)
		}
	}
	want := map[string]string{"Cache.java": "file", "CacheView.java (new)": "file", "docs/PLAN-cache.md": "file", "perf test": "res", "build": "res"}
	for k, v := range want {
		if kinds[k] != v {
			t.Errorf("claim %q kind %q, want %q", k, kinds[k], v)
		}
	}
	if s.NextQ != 10 {
		t.Errorf("next Q %d", s.NextQ)
	}
	open := 0
	for _, q := range s.Questions {
		if q.Answered == "" {
			open++
		}
	}
	if open != 2 {
		t.Errorf("open %d: %+v", open, s.Questions)
	}
	q8 := s.Questions[1]
	if q8.ID != 8 || q8.Issue != "cache-layer" || q8.Text != "eviction policy? A) LRU B) LFU" || q8.Asked != "2026-10-01" || q8.Raw != "" {
		t.Errorf("Q8 %+v", q8)
	}

	// originals kept, views re-rendered, second import refused
	if _, err := os.Stat(filepath.Join(st.Dir, "board.md.pre-import")); err != nil {
		t.Error(err)
	}
	board, _ := os.ReadFile(filepath.Join(st.Dir, "board.md"))
	for _, l := range []string{
		"| cache-layer | demo-cache-layer | active | Cache.java, docs/PLAN-cache.md | step 2 done 3333333; Q7 \"go\" | 2026-10-01 |",
		"- CacheView.java (new) — cache-layer (since 2026-10-01)",
		"## Waiting (turn order)\n- none",
	} {
		if !strings.Contains(string(board), l) {
			t.Errorf("board missing %q:\n%s", l, board)
		}
	}
	qs, _ := os.ReadFile(filepath.Join(st.Dir, "questions.md"))
	wantQ := `# Questions

## Open
- [Q8] cache-layer: eviction policy? A) LRU B) LFU (asked 2026-10-01)
- [Q9] cache-layer: free-form open question without the asked suffix

## Answered
- [Q7] cache-layer: step 2 check → GO step 3? → "go" (2026-10-01)
- [Q3] login-fix: in-game check. Answer (2026-09-29, dialog): "Works"
  second line of the answer
- [direct] login-fix: doc edits. Answer (2026-09-29, direct in worker): "go ahead"
`
	if string(qs) != wantQ {
		t.Errorf("questions.md:\n%s", qs)
	}
	if _, err := st.Import(false); err == nil {
		t.Error("second import not refused")
	}
	if _, err := st.Import(true); err != nil {
		t.Errorf("forced import: %v", err)
	}
}

func TestCorruptStateFails(t *testing.T) {
	st := Open(t.TempDir(), "demo")
	os.WriteFile(filepath.Join(st.Dir, "state.json"), []byte("{nope"), 0o644)
	if err := st.View(func(*State) error { return nil }); err == nil {
		t.Fatal("corrupt state.json read without error")
	}
}

func TestRoundTrip(t *testing.T) {
	st := Open(t.TempDir(), "demo")
	err := st.Update(func(s *State, logf func(string, ...any)) error {
		s.Rows = append(s.Rows, Row{Issue: "a", Session: "demo-a", Status: "active", Updated: "2026-10-06"})
		s.Tokens["a"] = 1
		logf("hello %s", "world")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	st.View(func(s *State) error {
		if len(s.Rows) != 1 || s.Tokens["a"] != 1 || s.Version != Version {
			t.Fatalf("%+v", s)
		}
		return nil
	})
	log, _ := os.ReadFile(filepath.Join(st.Dir, "log.md"))
	if !strings.HasPrefix(string(log), "# Log\n\n") || !strings.HasSuffix(string(log), " hello world\n") {
		t.Fatalf("log %q", log)
	}
}
