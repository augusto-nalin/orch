package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstalled(t *testing.T) {
	if !installed("/Users/a/.claude/plugins/cache/orch/orch/2.1.0") {
		t.Error("cache path not seen as installed")
	}
	if installed("/Users/a/src/orch/plugin") {
		t.Error("clone seen as installed")
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote([]string{"--settings", "/a b/it's.json"}); got != `'--settings' '/a b/it'\''s.json'` {
		t.Errorf("got %s", got)
	}
}

func TestSettingsFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "worker-settings.json"), []byte(`["~/.local/state/orch", "Read(~/.local/state/orch/**)"]`), 0o644)

	t.Setenv("ORCH_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	if f, err := settingsFile(root, "worker"); err != nil || f != filepath.Join(root, "worker-settings.json") {
		t.Errorf("default home: %s %v", f, err)
	}

	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "st"))
	f, err := settingsFile(root, "worker")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f)
	if want := `["~/st/orch", "Read(~/st/orch/**)"]`; string(b) != want {
		t.Errorf("got %s, want %s", b, want)
	}

	t.Setenv("ORCH_HOME", "/elsewhere")
	if _, err := settingsFile(root, "worker"); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("want outside-home error, got %v", err)
	}
}

func TestLastSession(t *testing.T) {
	agents := []byte(`[
		{"id":"aaa","kind":"background","startedAt":100,"sessionId":"a-1","name":"p-x","state":"done"},
		{"id":"bbb","kind":"background","startedAt":300,"sessionId":"b-1","name":"p-x","state":"done"},
		{"id":"ccc","kind":"background","startedAt":200,"sessionId":"c-1","name":"p-x","state":"done"},
		{"id":"ddd","kind":"background","startedAt":900,"sessionId":"d-1","name":"p-y","state":"done"},
		{"id":"fff","kind":"background","startedAt":500,"sessionId":"f-1","name":"p-x","state":"stopped"},
		{"pid":7,"id":"eee","kind":"background","startedAt":400,"sessionId":"e-1","name":"p-z","state":"blocked"}
	]`)
	saved := func(id string) bool { return id != "f-1" }
	if id, err := lastSession(agents, "p-x", saved); err != nil || id != "b-1" {
		t.Errorf("newest with a transcript: %s %v", id, err)
	}
	if _, err := lastSession(agents, "p-z", saved); err == nil || !strings.Contains(err.Error(), "running") {
		t.Errorf("want running error, got %v", err)
	}
	if _, err := lastSession(agents, "p-q", saved); err == nil {
		t.Error("want error for unknown name")
	}
}
