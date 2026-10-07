package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSanitize(t *testing.T) {
	if got := Sanitize("my repo.v2"); got != "my-repo-v2" {
		t.Fatal(got)
	}
}

func TestResolveRepoAndWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	t.Setenv("ORCH_HOME", "/state")
	base, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(base, "my.repo")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	os.MkdirAll(filepath.Join(repo, "sub"), 0o755)
	run(repo, "init", "-q")
	run(repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "x")
	wt := filepath.Join(base, "wt")
	run(repo, "worktree", "add", "-q", wt)

	p := Resolve(filepath.Join(repo, "sub"))
	if p.Name != "my-repo" || p.Root != repo || p.Top != repo || p.StateDir != "/state/my-repo" {
		t.Fatalf("%+v", p)
	}
	w := Resolve(wt)
	if w.Name != "my-repo" || w.Root != repo || w.Top != wt {
		t.Fatalf("worktree %+v", w)
	}

	for _, c := range []struct {
		path, rel string
		ok        bool
	}{
		{filepath.Join(repo, "sub/new.go"), "sub/new.go", true},
		{"sub/x.go", "sub/x.go", true},
		{filepath.Join(wt, "a/b.go"), "a/b.go", true},
		{filepath.Join(base, "elsewhere.go"), "", false},
		{"/etc/hosts", "", false},
	} {
		rel, ok := p.Rel(c.path)
		if rel != c.rel || ok != c.ok {
			t.Errorf("Rel(%s) = %q %v", c.path, rel, ok)
		}
	}

	out := Resolve(base)
	if out.Root != base || out.Name != Sanitize(filepath.Base(base)) {
		t.Fatalf("outside repo %+v", out)
	}
}
