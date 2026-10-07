// Package project resolves the project a directory belongs to and its orch state
// dir. The rule matches orch-state.sh: the project is the basename of the main
// checkout (the parent of git's common dir, so worktrees map to their main
// checkout), with characters outside [A-Za-z0-9_-] replaced by '-'.
package project

import (
	"os"
	"path/filepath"
	"strings"
)

type Project struct {
	Name     string // sanitized project name
	Root     string // main checkout (parent of the git common dir)
	Top      string // top of the checkout containing the dir (a worktree, or Root)
	StateDir string // <home>/<name>
}

// Home is the base of all state dirs: $ORCH_HOME, else $XDG_STATE_HOME/orch, else
// ~/.local/state/orch. Not under ~/.claude: the Bash sandbox protects that even
// when allowed, and workers run orchctl through Bash.
func Home() string {
	if h := os.Getenv("ORCH_HOME"); h != "" {
		return h
	}
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "orch")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "orch")
}

// Resolve finds the project for dir. Outside a git repo, dir itself is the root.
func Resolve(dir string) Project {
	dir, _ = filepath.Abs(dir)
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	top, common := gitDirs(dir)
	root := dir
	if common != "" {
		root = filepath.Dir(common)
	}
	if top == "" {
		top = root
	}
	name := Sanitize(filepath.Base(root))
	return Project{Name: name, Root: root, Top: top, StateDir: filepath.Join(Home(), name)}
}

// Sanitize maps a directory name to a project name.
func Sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return '-'
	}, s)
}

// gitDirs walks up from dir to the first .git and returns the checkout top and the
// absolute git common dir, or empty strings outside a repo. No git exec: hooks call
// this on every tool use.
func gitDirs(dir string) (top, common string) {
	for d := dir; ; d = filepath.Dir(d) {
		g := filepath.Join(d, ".git")
		if fi, err := os.Stat(g); err == nil {
			if fi.IsDir() {
				return d, g
			}
			return d, commonFromFile(d, g)
		}
		if p := filepath.Dir(d); p == d {
			return "", ""
		}
	}
}

// commonFromFile follows a worktree's .git file ("gitdir: X") to X/commondir.
func commonFromFile(top, gitFile string) string {
	b, err := os.ReadFile(gitFile)
	if err != nil {
		return ""
	}
	gd := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(top, gd)
	}
	c, err := os.ReadFile(filepath.Join(gd, "commondir"))
	if err != nil {
		return gd
	}
	cd := strings.TrimSpace(string(c))
	if !filepath.IsAbs(cd) {
		cd = filepath.Join(gd, cd)
	}
	return filepath.Clean(cd)
}

// Rel returns path relative to the checkout top containing it, and whether it is
// inside a checkout of this project at all (main checkout or one of its worktrees).
func (p Project) Rel(path string) (string, bool) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.Top, path)
	}
	path = filepath.Clean(path)
	// Resolve symlinks on the deepest existing ancestor (new files don't exist yet).
	path = evalExisting(path)
	other := Resolve(filepath.Dir(path))
	if other.Root != p.Root {
		return "", false
	}
	r, err := filepath.Rel(other.Top, path)
	if err != nil || r == ".." || strings.HasPrefix(r, "../") {
		return "", false
	}
	return filepath.ToSlash(r), true
}

func evalExisting(path string) string {
	rest := ""
	for d := path; ; d = filepath.Dir(d) {
		if e, err := filepath.EvalSymlinks(d); err == nil {
			return filepath.Join(e, rest)
		}
		rest = filepath.Join(filepath.Base(d), rest)
		if p := filepath.Dir(d); p == d {
			return path
		}
	}
}
