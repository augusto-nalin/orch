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
