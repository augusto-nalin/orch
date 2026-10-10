package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"2.2.0", "2.1.0", true},
		{"2.10.0", "2.9.9", true},
		{"2.1.0", "2.1.0", false},
		{"2.0.9", "2.1.0", false},
		{"3", "2.9.9", true},
		{"", "2.1.0", false},
		{"-", "2.1.0", false},
		{"<html>", "2.1.0", false},
		{"2.4.0", "2.4.0-dev", true},
		{"2.4.0-dev", "2.4.0", false},
		{"2.4.0-dev", "2.4.0-dev", false},
		{"2.4.1-dev", "2.4.0", true},
		{"2.4.0", "2.3.0-dev", true},
	} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestUpdateCheck(t *testing.T) {
	t.Setenv("ORCH_HOME", t.TempDir())
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755)
	os.WriteFile(filepath.Join(root, ".claude-plugin", "plugin.json"), []byte(`{"name":"orch","version":"2.1.0"}`), 0o644)
	calls, latest := 0, "2.2.0"
	defer func(f func() string) { fetchLatest = f }(fetchLatest)
	fetchLatest = func() string { calls++; return latest }
	defer func(v string) { version = v }(version)
	version = "2.1.0"

	now := time.Unix(1_800_000_000, 0)
	if got := updateCheck(root, now); got != "orch 2.2.0 is available (you have 2.1.0): git pull && make build" {
		t.Fatalf("first check: %q", got)
	}
	latest = "9.9.9"
	if got := updateCheck(root, now.Add(time.Hour)); got == "" || calls != 1 {
		t.Fatalf("within a day: %q, %d fetches; want the cached notice, 1 fetch", got, calls)
	}
	if got := updateCheck(root, now.Add(25*time.Hour)); calls != 2 || got != "orch 9.9.9 is available (you have 2.1.0): git pull && make build" {
		t.Fatalf("next day: %q, %d fetches", got, calls)
	}
	latest = ""
	if got := updateCheck(root, now.Add(50*time.Hour)); got != "" || calls != 3 {
		t.Fatalf("offline: %q, %d fetches", got, calls)
	}
	if got := updateCheck(root, now.Add(50*time.Hour+30*time.Minute)); got != "" || calls != 3 {
		t.Fatalf("offline, cached: %q, %d fetches; want no refetch within an hour", got, calls)
	}
	latest = "9.9.9"
	if got := updateCheck(root, now.Add(51*time.Hour)); calls != 4 || got == "" {
		t.Fatalf("back online: %q, %d fetches; want a retry after an hour", got, calls)
	}
}

func TestUpdateCheckDev(t *testing.T) {
	t.Setenv("ORCH_HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "plugins", "cache", "orch", "orch", "2.1.0")
	os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755)
	os.WriteFile(filepath.Join(root, ".claude-plugin", "plugin.json"), []byte(`{"version":"2.1.0"}`), 0o644)
	defer func(f func() string) { fetchLatest = f }(fetchLatest)
	fetchLatest = func() string { return "2.2.0" }
	defer func(v string) { version = v }(version)
	version = "dev"
	if got := updateCheck(root, time.Now()); got != "orch 2.2.0 is available (you have 2.1.0-dev): orchctl update" {
		t.Fatalf("older: %q", got)
	}
	os.Remove(filepath.Join(os.Getenv("ORCH_HOME"), "update-check"))
	fetchLatest = func() string { return "2.1.0" }
	if got := updateCheck(root, time.Now()); got != "orch 2.1.0 is available (you have 2.1.0-dev): orchctl update" {
		t.Fatalf("same version: %q", got)
	}
}

func TestUpdate(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	cache := filepath.Join(cfg, "plugins", "cache", "orch", "orch")
	oldRoot, newRoot := filepath.Join(cache, "2.1.0"), filepath.Join(cache, "2.2.0")
	for _, r := range []string{oldRoot, newRoot} {
		os.MkdirAll(filepath.Join(r, "bin"), 0o755)
		os.MkdirAll(filepath.Join(r, ".claude-plugin"), 0o755)
		os.WriteFile(filepath.Join(r, ".claude-plugin", "plugin.json"), []byte(`{"version":"`+filepath.Base(r)+`"}`), 0o644)
	}
	os.WriteFile(filepath.Join(oldRoot, "bin", "orchctl-dev"), nil, 0o755)
	os.WriteFile(filepath.Join(cfg, "plugins", "installed_plugins.json"),
		[]byte(`{"plugins":{"orch@orch":[{"installPath":"`+newRoot+`"}]}}`), 0o644)
	var calls []string
	defer func(f func(string, ...string) error) { runCmd = f }(runCmd)
	runCmd = func(name string, args ...string) error {
		calls = append(calls, strings.Join(append([]string{name}, args...), " "))
		return nil
	}

	out, err := update(oldRoot)
	if err != nil || out != "orch 2.2.0 installed — restart your claude sessions to load it" {
		t.Fatalf("update: %q, %v", out, err)
	}
	want := []string{
		"claude plugin marketplace update orch",
		"claude plugin update orch@orch",
		"bash " + filepath.Join(newRoot, "scripts", "orch-setup.sh"),
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("ran %q, want %q", calls, want)
	}
	if _, err := os.Stat(filepath.Join(oldRoot, "bin", "orchctl-dev")); !os.IsNotExist(err) {
		t.Fatalf("dev build still there: %v", err)
	}

	if _, err := update(t.TempDir()); err == nil || !strings.Contains(err.Error(), "git pull") {
		t.Fatalf("clone: %v", err)
	}
}
