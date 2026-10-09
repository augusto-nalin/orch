package broker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"orch/internal/state"
)

func newBroker(t *testing.T) *Broker {
	t.Helper()
	st := state.Open(t.TempDir(), "proj")
	st.Now = func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }
	b := New(st)
	b.Poll = 5 * time.Millisecond
	return b
}

func must(t *testing.T, got string, err error, want string) {
	t.Helper()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func view(t *testing.T, b *Broker) *state.State {
	t.Helper()
	var out *state.State
	if err := b.St.View(func(s *state.State) error { out = s; return nil }); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestClaimHeldOrderRelease(t *testing.T) {
	b := newBroker(t)
	got, err := b.Claim("a", "file", []string{"x.go", "y.go"})
	must(t, got, err, "GO")
	got, err = b.Claim("a", "file", []string{"x.go"}) // re-claim own item
	must(t, got, err, "GO")

	got, err = b.Claim("b", "file", []string{"x.go", "z.go"})
	must(t, got, err, "HELD x.go by a")
	// all or nothing: z.go not granted
	if s := view(t, b); len(s.Claims) != 2 || s.Contested["x.go"][0] != "b" {
		t.Fatalf("claims %v contested %v", s.Claims, s.Contested)
	}
	got, err = b.Claim("c", "file", []string{"x.go"})
	must(t, got, err, "HELD x.go by a")

	// the orch orders c first, then b; a still holds it
	got, err = b.Order("x.go", []string{"c", "b"})
	must(t, got, err, "queued after a")
	// a third party can't jump the queue on z.go-free, x.go-queued
	got, err = b.Claim("d", "file", []string{"x.go"})
	must(t, got, err, "HELD x.go by a")

	got, err = b.Release("a", []string{"x.go"})
	must(t, got, err, "ok; x.go → c")
	if s := view(t, b); len(s.Pending["c"]) != 0 || len(s.Pending["b"]) == 0 {
		t.Fatalf("pending %v", s.Pending)
	}
	got, err = b.Wait("c", "go", time.Second)
	must(t, got, err, "GO")

	got, err = b.Release("c", nil)
	must(t, got, err, "ok; x.go → b")
	// b's whole request (x.go and the free z.go) is now granted
	s := view(t, b)
	if !holds(s, "b", state.Claim{Kind: "file", Item: "x.go"}) || !holds(s, "b", state.Claim{Kind: "file", Item: "z.go"}) {
		t.Fatalf("claims %v", s.Claims)
	}
	got, err = b.Wait("b", "go", time.Second)
	must(t, got, err, "GO")
	// d still contested x.go; no auto grant after b releases
	got, err = b.Release("b", nil)
	must(t, got, err, "ok; x.go free, contested — needs orchctl order")
	got, err = b.Claim("a", "file", []string{"x.go"})
	must(t, got, err, "HELD x.go by orch (contested)")
	got, err = b.Order("x.go", []string{"d"})
	must(t, got, err, "GO x.go → d")
}

func TestOrderFreeItemGrantsNow(t *testing.T) {
	b := newBroker(t)
	b.Claim("a", "res", []string{"build"})
	got, err := b.Claim("b", "res", []string{"build"})
	must(t, got, err, "HELD build by a")
	b.Release("a", nil)
	got, err = b.Order("build", []string{"b"})
	must(t, got, err, "GO build → b")
	got, err = b.Wait("b", "go", time.Second)
	must(t, got, err, "GO")
	// a file named build doesn't collide with the build resource
	got, err = b.Claim("a", "file", []string{"build"})
	must(t, got, err, "GO")
}

func TestWaitWakesOnHandoff(t *testing.T) {
	b := newBroker(t)
	b.Claim("a", "file", []string{"x.go"})
	b.Claim("b", "file", []string{"x.go"})
	b.Order("x.go", []string{"b"})
	done := make(chan string)
	go func() { got, _ := b.Wait("b", "go", 5*time.Second); done <- got }()
	select {
	case got := <-done:
		t.Fatalf("woke early: %q", got)
	case <-time.After(50 * time.Millisecond):
	}
	b.Release("a", nil)
	if got := <-done; got != "GO" {
		t.Fatalf("got %q", got)
	}
	if _, err := b.Wait("a", "commit", 20*time.Millisecond); err != ErrTimeout {
		t.Fatalf("want timeout, got %v", err)
	}
}

func TestDirAndBasenameClaims(t *testing.T) {
	b := newBroker(t)
	must2 := func(issue string, items []string, want string) {
		t.Helper()
		got, err := b.Claim(issue, "file", items)
		must(t, got, err, want)
	}
	must2("a", []string{"internal/state/"}, "GO")
	must2("b", []string{"internal/state/state.go"}, "HELD internal/state/state.go by a")
	must2("b", []string{"internal/"}, "HELD internal/ by a")
	must2("b", []string{"internal/broker/x.go"}, "GO")
	must2("c", []string{"Foo.java"}, "GO") // imported-style bare name
	must2("d", []string{"src/main/Foo.java"}, "HELD src/main/Foo.java by c")
}

func TestCommitTokens(t *testing.T) {
	b := newBroker(t)
	if ok, _ := b.Consume("a"); ok {
		t.Fatal("consumed without token")
	}
	b.CommitGo("a", 2)
	got, err := b.Wait("a", "commit", time.Second)
	must(t, got, err, "COMMIT")
	for i := 0; i < 2; i++ {
		if ok, _ := b.Consume("a"); !ok {
			t.Fatalf("consume %d failed", i)
		}
	}
	if ok, _ := b.Consume("a"); ok {
		t.Fatal("third consume")
	}
	if s := view(t, b); len(s.Tokens) != 0 {
		t.Fatalf("tokens %v", s.Tokens)
	}
}

func TestPaused(t *testing.T) {
	b := newBroker(t)
	b.SetPaused("a", true)
	if !view(t, b).Paused["a"] {
		t.Fatal("not paused")
	}
	b.SetPaused("a", false)
	if view(t, b).Paused["a"] {
		t.Fatal("still paused")
	}
}

func TestVersioning(t *testing.T) {
	b := newBroker(t)
	got, err := b.Versioning("")
	must(t, got, err, "unset")
	got, err = b.Versioning("on")
	must(t, got, err, "on")
	got, err = b.Versioning("")
	must(t, got, err, "on")
	if _, err := b.Versioning("maybe"); err == nil {
		t.Fatal("accepted maybe")
	}
	got, err = b.Versioning("off")
	must(t, got, err, "off")
	if v := view(t, b).Versioning; v != "off" {
		t.Fatalf("stored %q", v)
	}
}

func TestRowDoneCleansUp(t *testing.T) {
	b := newBroker(t)
	got, err := b.Row("a", "active", "spawned")
	must(t, got, err, "ok")
	b.Claim("a", "file", []string{"x.go"})
	b.CommitGo("a", 1)
	b.SetPaused("a", true)
	b.Claim("b", "file", []string{"x.go"})
	b.Order("x.go", []string{"b"})
	got, err = b.Row("a", "done", "abc123")
	must(t, got, err, "ok; x.go → b")
	s := view(t, b)
	if len(s.Tokens)+len(s.Paused) != 0 || s.Rows[0].Session != "proj-a" || s.Rows[0].Outcome != "abc123" {
		t.Fatalf("state %+v", s)
	}
	board, _ := os.ReadFile(filepath.Join(b.St.Dir, "board.md"))
	if !strings.Contains(string(board), "| a | proj-a | done |  | abc123 | 2026-10-06 |") ||
		!strings.Contains(string(board), "- x.go — b (since 2026-10-06)") {
		t.Fatalf("board:\n%s", board)
	}
}

func TestQuestions(t *testing.T) {
	b := newBroker(t)
	got, err := b.Q("a", "pick one")
	must(t, got, err, "Q1")
	got, err = b.Q("b", "other")
	must(t, got, err, "Q2")
	got, err = b.A("[Q1]", "A")
	must(t, got, err, "ok")
	if _, err := b.A("Q1", ""); err == nil {
		t.Fatal("answered twice")
	}
	qs, _ := os.ReadFile(filepath.Join(b.St.Dir, "questions.md"))
	want := "# Questions\n\n## Open\n- [Q2] b: other (asked 2026-10-06)\n\n## Answered\n- [Q1] a: answered 2026-10-06 — A → issues/a.md#Q1\n"
	if string(qs) != want {
		t.Fatalf("questions.md:\n%s", qs)
	}
	log, _ := os.ReadFile(filepath.Join(b.St.Dir, "log.md"))
	if !strings.Contains(string(log), "2026-10-06 Q1 a: pick one\n") {
		t.Fatalf("log:\n%s", log)
	}
}

func TestRegistry(t *testing.T) {
	b := newBroker(t)
	b.Register("sess-1", state.Session{Role: "worker", Issue: "a"})
	sess, ok, err := b.Lookup("sess-1")
	if err != nil || !ok || sess.Issue != "a" {
		t.Fatalf("%v %v %v", sess, ok, err)
	}
	if _, ok, _ := b.Lookup("nope"); ok {
		t.Fatal("found unknown session")
	}
}

// Many goroutines, each opening its own lock fd, must not lose updates.
func TestConcurrentWriters(t *testing.T) {
	b := newBroker(t)
	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// separate Store per goroutine, like separate processes
			st := state.Open(b.St.Dir, "proj")
			if _, err := New(st).Q(fmt.Sprintf("i%d", i), "q"); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	s := view(t, b)
	if len(s.Questions) != n || s.NextQ != n+1 {
		t.Fatalf("questions %d next %d", len(s.Questions), s.NextQ)
	}
	seen := map[int]bool{}
	for _, q := range s.Questions {
		if seen[q.ID] {
			t.Fatalf("dup Q%d", q.ID)
		}
		seen[q.ID] = true
	}
}

func TestFull(t *testing.T) {
	b := newBroker(t)
	b.Row("a", "active", "spawned")
	b.Row("old", "done", "x")
	b.Claim("a", "res", []string{"build"})
	b.Claim("b", "res", []string{"build"})
	b.Q("a", "pick")
	b.CommitGo("a", 1)
	got, err := b.Full("/repo")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"project: proj\n", "orchestrator session name: proj-orch\n", "versioning: unset — ask the user\n",
		"| a | proj-a | active |  | spawned | 2026-10-06 |\n",
		"(1 done/dropped rows hidden — see board.md)",
		"- build — a (since 2026-10-06)\n",
		"- build contested by b — needs orchctl order\n",
		"- a commit-go ×1\n",
		"- [Q1] a: pick (asked 2026-10-06)\n",
		"2026-10-06 a active: spawned\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "| old |") {
		t.Errorf("done row shown")
	}
}

func TestChecks(t *testing.T) {
	b := newBroker(t)
	if _, err := b.Checked("a"); err == nil {
		t.Fatal("checked with no CHECK")
	}
	b.CheckOpen("a")
	got, err := b.CheckState("a")
	must(t, got, err, "open")
	got, err = b.Checked("a")
	must(t, got, err, "ok")
	got, err = b.CheckState("a")
	must(t, got, err, "passed")

	// A commit ends the task: the next one needs its own CHECK.
	b.SetHead("a", "aaaaaaa")
	b.Committed("a", "bbbbbbb")
	got, err = b.CheckState("a")
	must(t, got, err, "")

	b.CheckOpen("a")
	b.Row("a", "reopened", "more")
	got, err = b.CheckState("a")
	must(t, got, err, "")
}
