package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
