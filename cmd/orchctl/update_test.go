package main

import (
	"os"
	"path/filepath"
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

	now := time.Unix(1_800_000_000, 0)
	if got := updateCheck(root, now); got != "orch 2.2.0 is available (you have 2.1.0): git pull" {
		t.Fatalf("first check: %q", got)
	}
	latest = "9.9.9"
	if got := updateCheck(root, now.Add(time.Hour)); got == "" || calls != 1 {
		t.Fatalf("within a day: %q, %d fetches; want the cached notice, 1 fetch", got, calls)
	}
	if got := updateCheck(root, now.Add(25*time.Hour)); calls != 2 || got != "orch 9.9.9 is available (you have 2.1.0): git pull" {
		t.Fatalf("next day: %q, %d fetches", got, calls)
	}
	latest = ""
	if got := updateCheck(root, now.Add(50*time.Hour)); got != "" || calls != 3 {
		t.Fatalf("offline: %q, %d fetches", got, calls)
	}
	if got := updateCheck(root, now.Add(51*time.Hour)); got != "" || calls != 3 {
		t.Fatalf("offline, cached: %q, %d fetches; want no refetch within a day", got, calls)
	}
}
