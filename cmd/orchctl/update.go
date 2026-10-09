package main

// Update check: at most once a day, `orch` asks GitHub for the latest release's
// latest-version.txt and says so when it is newer than this plugin.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"orch/internal/project"
)

const (
	releaseRepo = "augusto-nalin/orch"
	latestURL   = "https://github.com/" + releaseRepo + "/releases/latest/download/latest-version.txt"
)

// fetchLatest returns the latest released version: anonymously, or through gh
// (the user's login) while the repo is private.
var fetchLatest = func() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if req, err := http.NewRequestWithContext(ctx, "GET", latestURL, nil); err == nil {
		if resp, err := http.DefaultClient.Do(req); err == nil {
			defer resp.Body.Close()
			if b, err := io.ReadAll(io.LimitReader(resp.Body, 64)); err == nil && resp.StatusCode == 200 {
				return strings.TrimSpace(string(b))
			}
		}
	}
	out, err := exec.CommandContext(ctx, "gh", "release", "download", "-R", releaseRepo,
		"-p", "latest-version.txt", "-O", "-").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func pluginVersion(root string) string {
	var m struct{ Version string }
	b, _ := os.ReadFile(filepath.Join(root, ".claude-plugin", "plugin.json"))
	json.Unmarshal(b, &m)
	return m.Version
}

// updateCheck returns a notice when a newer release exists, or "". The latest
// version is cached in the state home for a day.
func updateCheck(root string, now time.Time) string {
	have := pluginVersion(root)
	if have == "" {
		return ""
	}
	stamp := filepath.Join(project.Home(), "update-check")
	var latest string
	if b, err := os.ReadFile(stamp); err == nil {
		if f := strings.Fields(string(b)); len(f) >= 1 {
			if at, err := strconv.ParseInt(f[0], 10, 64); err == nil && now.Sub(time.Unix(at, 0)) < 24*time.Hour {
				latest = "-"
				if len(f) > 1 {
					latest = f[1]
				}
			}
		}
	}
	if latest == "" {
		latest = fetchLatest()
		os.MkdirAll(filepath.Dir(stamp), 0o755)
		os.WriteFile(stamp, []byte(fmt.Sprintf("%d %s\n", now.Unix(), latest)), 0o644)
	}
	if !newer(latest, have) {
		return ""
	}
	how := "git pull"
	if installed(root) {
		how = "claude plugin update orch@orch, then /orch:setup"
	}
	return fmt.Sprintf("orch %s is available (you have %s): %s", latest, have, how)
}

// newer reports whether version a is above b (x.y.z, missing parts are 0).
func newer(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var x, y int
		var err error
		if i < len(pa) {
			if x, err = strconv.Atoi(pa[i]); err != nil {
				return false
			}
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}
