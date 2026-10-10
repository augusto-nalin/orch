package main

// Update check: at most once a day, `orch` asks GitHub for the latest release's
// latest-version.txt and says so when it is newer than this plugin. `orchctl update`
// installs it.

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
// version is cached in the state home for a day; no answer (offline, or no
// release yet) only for an hour.
func updateCheck(root string, now time.Time) string {
	have := currentVersion(root)
	if have == "" || have == "dev" {
		return ""
	}
	stamp := filepath.Join(project.Home(), "update-check")
	var latest string
	if b, err := os.ReadFile(stamp); err == nil {
		if f := strings.Fields(string(b)); len(f) >= 1 {
			ttl := time.Hour
			if len(f) > 1 {
				ttl = 24 * time.Hour
			}
			if at, err := strconv.ParseInt(f[0], 10, 64); err == nil && now.Sub(time.Unix(at, 0)) < ttl {
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
	how := "git pull && make build"
	if installed(root) {
		how = "orchctl update"
	}
	return fmt.Sprintf("orch %s is available (you have %s): %s", latest, have, how)
}

// runCmd runs a command with its output on the terminal.
var runCmd = func(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

// update installs the latest release of the installed plugin (root sits in
// Claude's plugin cache at <marketplace>/<plugin>/<version>), drops the dev build
// copied into it, and runs setup from the new install so the wrappers and the
// release binary are in place. A clone updates with git instead.
func update(root string) (string, error) {
	if !installed(root) {
		return "", fmt.Errorf("%s is a clone, not an installed plugin — run: git pull && make build", root)
	}
	plugin := filepath.Dir(root)
	market, name := filepath.Base(filepath.Dir(plugin)), filepath.Base(plugin)
	if err := runCmd("claude", "plugin", "marketplace", "update", market); err != nil {
		return "", fmt.Errorf("claude plugin marketplace update %s: %w", market, err)
	}
	if err := runCmd("claude", "plugin", "update", name+"@"+market); err != nil {
		return "", fmt.Errorf("claude plugin update %s@%s: %w", name, market, err)
	}
	newRoot := installPath(name+"@"+market, root)
	for _, r := range []string{root, newRoot} {
		for _, dev := range []string{"orchctl-dev", "orchctl-dev.exe"} {
			if err := os.Remove(filepath.Join(r, "bin", dev)); err != nil && !os.IsNotExist(err) {
				return "", err
			}
		}
	}
	if err := runCmd("bash", filepath.Join(newRoot, "scripts", "orch-setup.sh")); err != nil {
		return "", fmt.Errorf("setup: %w", err)
	}
	return fmt.Sprintf("orch %s installed — restart your claude sessions to load it", pluginVersion(newRoot)), nil
}

// installPath is the plugin's current install in Claude's plugin registry, or
// fallback.
func installPath(id, fallback string) string {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".claude")
	}
	var reg struct {
		Plugins map[string][]struct{ InstallPath string }
	}
	b, _ := os.ReadFile(filepath.Join(dir, "plugins", "installed_plugins.json"))
	json.Unmarshal(b, &reg)
	if e := reg.Plugins[id]; len(e) > 0 && e[len(e)-1].InstallPath != "" {
		return e[len(e)-1].InstallPath
	}
	return fallback
}

// currentVersion is this binary's version: the release's, or <plugin version>-dev
// for a dev build, which sorts below the release of that version.
func currentVersion(root string) string {
	if version != "dev" {
		return version
	}
	if v := pluginVersion(root); v != "" {
		return v + "-dev"
	}
	return version
}

// newer reports whether version a is above b (x.y.z[-pre], missing parts are 0;
// a pre-release such as 2.4.0-dev is below 2.4.0).
func newer(a, b string) bool {
	a, preA, _ := strings.Cut(a, "-")
	b, preB, _ := strings.Cut(b, "-")
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
	return preA == "" && preB != ""
}
