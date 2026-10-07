package hook

import (
	"strings"
)

// segment is one simple command of a Bash tool call: its words (quotes removed)
// and the targets of its output redirections.
type segment struct {
	args   []string
	redirs []string
}

// splitShell is a best-effort shell splitter: quotes, command separators (; & | &&
// || newline), output redirections and heredoc bodies (skipped). $(…) and `…` stay
// inside the word they appear in. Good enough to spot git/sed/tee/> usage.
func splitShell(cmd string) []segment {
	var segs []segment
	var cur segment
	var word strings.Builder
	inWord, redirNext := false, false
	var heredocs []string

	endWord := func() {
		if !inWord {
			return
		}
		w := word.String()
		word.Reset()
		inWord = false
		if redirNext {
			redirNext = false
			if !strings.HasPrefix(w, "&") {
				cur.redirs = append(cur.redirs, w)
			}
			return
		}
		cur.args = append(cur.args, w)
	}
	endSeg := func() {
		endWord()
		if len(cur.args) > 0 || len(cur.redirs) > 0 {
			segs = append(segs, cur)
		}
		cur = segment{}
	}

	r := []rune(cmd)
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '\\' && i+1 < len(r):
			i++
			if r[i] != '\n' {
				word.WriteRune(r[i])
				inWord = true
			}
		case c == '\'':
			inWord = true
			for i++; i < len(r) && r[i] != '\''; i++ {
				word.WriteRune(r[i])
			}
		case c == '"':
			inWord = true
			for i++; i < len(r) && r[i] != '"'; i++ {
				if r[i] == '\\' && i+1 < len(r) {
					i++
				}
				word.WriteRune(r[i])
			}
		case c == '$' && i+1 < len(r) && r[i+1] == '(':
			// keep $(…) as part of the word, balanced
			inWord = true
			depth := 0
			for ; i < len(r); i++ {
				word.WriteRune(r[i])
				if r[i] == '(' {
					depth++
				} else if r[i] == ')' {
					if depth--; depth == 0 {
						break
					}
				}
			}
		case c == ' ' || c == '\t':
			endWord()
		case c == '\n':
			endSeg()
			for _, d := range heredocs {
				i = skipHeredoc(r, i+1, d) - 1
			}
			heredocs = nil
		case c == ';' || c == '|' || c == '&' && !(i+1 < len(r) && r[i+1] == '>'):
			endSeg()
			if i+1 < len(r) && (r[i+1] == c) {
				i++
			}
		case c == '<' && i+1 < len(r) && r[i+1] == '<':
			endWord()
			i += 2
			if i < len(r) && r[i] == '<' { // here-string
				break
			}
			if i < len(r) && r[i] == '-' {
				i++
			}
			for i < len(r) && r[i] == ' ' {
				i++
			}
			var d strings.Builder
			for ; i < len(r) && !strings.ContainsRune(" \t\n;&|", r[i]); i++ {
				if r[i] != '\'' && r[i] != '"' {
					d.WriteRune(r[i])
				}
			}
			i--
			heredocs = append(heredocs, d.String())
		case c == '>' || c == '&' && i+1 < len(r) && r[i+1] == '>':
			// fd number before > ("2>") is not a word
			if inWord && isDigits(word.String()) {
				word.Reset()
				inWord = false
			}
			endWord()
			if c == '&' {
				i++
			}
			for i+1 < len(r) && (r[i+1] == '>' || r[i+1] == '|') {
				i++
			}
			if i+1 < len(r) && r[i+1] == '&' { // fd dup: 2>&1, >&-
				for i++; i+1 < len(r) && (isDigits(string(r[i+1])) || r[i+1] == '-'); i++ {
				}
				break
			}
			redirNext = true
		default:
			word.WriteRune(c)
			inWord = true
		}
	}
	endSeg()
	return segs
}

// skipHeredoc returns the index just after the line that ends heredoc delim.
func skipHeredoc(r []rune, i int, delim string) int {
	for i < len(r) {
		j := i
		for j < len(r) && r[j] != '\n' {
			j++
		}
		line := strings.TrimSpace(string(r[i:j]))
		i = j + 1
		if line == delim {
			return i
		}
	}
	return len(r)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// gitCall is a git invocation found in a segment.
type gitCall struct {
	dir  string // -C dir, "" if none
	sub  string
	args []string
}

// parseGit returns the git call in seg, if seg runs git.
func parseGit(seg segment) (gitCall, bool) {
	a := seg.args
	for len(a) > 0 && strings.Contains(a[0], "=") && !strings.HasPrefix(a[0], "-") {
		a = a[1:] // env assignments
	}
	if len(a) > 0 && (a[0] == "command" || a[0] == "exec") {
		a = a[1:]
	}
	if len(a) == 0 || a[0] != "git" && !strings.HasSuffix(a[0], "/git") {
		return gitCall{}, false
	}
	var g gitCall
	for i := 1; i < len(a); i++ {
		switch {
		case a[i] == "-C" && i+1 < len(a):
			i++
			g.dir = a[i]
		case (a[i] == "-c" || a[i] == "--git-dir" || a[i] == "--work-tree" || a[i] == "--namespace") && i+1 < len(a):
			i++
		case strings.HasPrefix(a[i], "-"):
		default:
			g.sub, g.args = a[i], a[i+1:]
			return g, true
		}
	}
	return g, true
}

// stagesAll: git add -A/--all/-u/--update/./:/ — stages more than the worker's files.
func (g gitCall) stagesAll() bool {
	if g.sub != "add" {
		return false
	}
	for _, a := range g.args {
		switch a {
		case "-A", "--all", ".", ":/", "-u", "--update", "*":
			return true
		}
		if isShortCluster(a, 'A') || isShortCluster(a, 'u') {
			return true
		}
	}
	return false
}

// commitsAll: git commit -a/--all (or a cluster like -am).
func (g gitCall) commitsAll() bool {
	if g.sub != "commit" {
		return false
	}
	for _, a := range g.args {
		if a == "--all" || isShortCluster(a, 'a') {
			return true
		}
		if a == "--" {
			break
		}
	}
	return false
}

// isShortCluster: a is "-xyz" (not "--long") containing flag f before any value
// flag (-m/-F/-C/-c take the rest of the word).
func isShortCluster(a string, f rune) bool {
	if len(a) < 2 || a[0] != '-' || a[1] == '-' {
		return false
	}
	for _, c := range a[1:] {
		if c == f {
			return true
		}
		if strings.ContainsRune("mFCctS", c) {
			return false
		}
	}
	return false
}

// editTargets returns files a segment writes in place: redirection targets,
// sed -i files, tee files.
func editTargets(seg segment) []string {
	out := append([]string(nil), seg.redirs...)
	a := seg.args
	if len(a) == 0 {
		return out
	}
	switch a[0] {
	case "sed", "gsed":
		inPlace, script := false, false
		var files []string
		for i := 1; i < len(a); i++ {
			switch {
			case a[i] == "-e" || a[i] == "-f" || a[i] == "--expression" || a[i] == "--file":
				script = true
				i++
			case a[i] == "-i" || strings.HasPrefix(a[i], "-i") || strings.HasPrefix(a[i], "--in-place") || isShortCluster(a[i], 'i'):
				inPlace = true
				if a[i] == "-i" && i+1 < len(a) && a[i+1] == "" { // BSD: -i ''
					i++
				}
			case strings.HasPrefix(a[i], "-"):
			default:
				files = append(files, a[i])
			}
		}
		if !inPlace {
			return out
		}
		if !script && len(files) > 0 {
			files = files[1:]
		}
		out = append(out, files...)
	case "tee":
		for _, x := range a[1:] {
			if !strings.HasPrefix(x, "-") {
				out = append(out, x)
			}
		}
	}
	return out
}
