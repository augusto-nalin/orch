package hook

import (
	"reflect"
	"testing"
)

func TestSplitShell(t *testing.T) {
	cases := []struct {
		cmd  string
		want []segment
	}{
		{`git add a.go && git commit -m "x y" -- a.go`, []segment{
			{args: []string{"git", "add", "a.go"}},
			{args: []string{"git", "commit", "-m", "x y", "--", "a.go"}},
		}},
		{`echo hi > out.txt 2>&1; cat a | tee -a log.txt`, []segment{
			{args: []string{"echo", "hi"}, redirs: []string{"out.txt"}},
			{args: []string{"cat", "a"}},
			{args: []string{"tee", "-a", "log.txt"}},
		}},
		{"cat > f.go <<'EOF'\ngit add .\nEOF\nls", []segment{
			{args: []string{"cat"}, redirs: []string{"f.go"}},
			{args: []string{"ls"}},
		}},
		{`git commit -m "$(cat <<'EOF'
msg; git add -A
EOF
)"`, []segment{
			{args: []string{"git", "commit", "-m", "$(cat <<'EOF'\nmsg; git add -A\nEOF\n)"}},
		}},
		{`cmd 2>/dev/null &>all.log`, []segment{
			{args: []string{"cmd"}, redirs: []string{"/dev/null", "all.log"}},
		}},
	}
	for _, c := range cases {
		if got := splitShell(c.cmd); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q:\n got %#v\nwant %#v", c.cmd, got, c.want)
		}
	}
}

func TestGitFlags(t *testing.T) {
	cases := []struct {
		cmd                 string
		isGit, all, commAll bool
		sub                 string
	}{
		{"git add -A", true, true, false, "add"},
		{"git add --all", true, true, false, "add"},
		{"git add .", true, true, false, "add"},
		{"git add -u", true, true, false, "add"},
		{"git add a.go b.go", true, false, false, "add"},
		{"git -C /x add -A", true, true, false, "add"},
		{"git commit -am msg", true, false, true, "commit"},
		{"git commit -a -m msg", true, false, true, "commit"},
		{"git commit --all", true, false, true, "commit"},
		{"git commit -m all -- a.go", true, false, false, "commit"},
		{"git commit -ma", true, false, false, "commit"}, // -m takes "a"
		{"GIT_EDITOR=true git -c core.x=1 commit -q -- a", true, false, false, "commit"},
		{"/usr/bin/git status", true, false, false, "status"},
		{"echo git add -A", false, false, false, ""},
	}
	for _, c := range cases {
		g, ok := parseGit(splitShell(c.cmd)[0])
		if ok != c.isGit || g.stagesAll() != c.all || g.commitsAll() != c.commAll || g.sub != c.sub {
			t.Errorf("%q: git=%v all=%v commitAll=%v sub=%q", c.cmd, ok, g.stagesAll(), g.commitsAll(), g.sub)
		}
	}
}

func TestEditTargets(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{"sed -i '' 's/a/b/' x.go y.go", []string{"x.go", "y.go"}},
		{"sed -i.bak -e 's/a/b/' x.go", []string{"x.go"}},
		{"sed -n 's/a/b/p' x.go", nil},
		{"tee -a f.txt", []string{"f.txt"}},
		{"echo x >> f.txt", []string{"f.txt"}},
		{"grep x y", nil},
	}
	for _, c := range cases {
		if got := editTargets(splitShell(c.cmd)[0]); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %#v want %#v", c.cmd, got, c.want)
		}
	}
}
