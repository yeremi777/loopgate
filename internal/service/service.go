// Package service installs loopd as a macOS launchd agent.
package service

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
)

const Label = "loopgate.loopd"

// Config is what the agent needs: launchd starts loopd with almost no PATH,
// so the PATH that finds claude, gh and git is captured at install time.
type Config struct {
	Loopd string
	Home  string
	Path  string
}

var plist = template.Must(template.New("plist").Funcs(template.FuncMap{"x": escape}).Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{x .Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{x .Loopd}}</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>{{x .Path}}</string>
		<key>LOOPD_HOME</key>
		<string>{{x .Home}}</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>StandardOutPath</key>
	<string>{{x .Log}}</string>
	<key>StandardErrorPath</key>
	<string>{{x .Log}}</string>
</dict>
</plist>
`))

// Install writes the agent's plist into agentsDir and returns its path.
func Install(agentsDir string, cfg Config) (string, error) {
	switch {
	case !filepath.IsAbs(cfg.Loopd):
		return "", errors.New("loopd path must be absolute")
	case !filepath.IsAbs(cfg.Home):
		return "", errors.New("loopd home must be absolute")
	case cfg.Path == "":
		return "", errors.New("PATH is empty")
	}
	var b bytes.Buffer
	err := plist.Execute(&b, struct {
		Config
		Label, Log string
	}{cfg, Label, filepath.Join(cfg.Home, "loopd.log")})
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(agentsDir, Label+".plist")
	return path, os.WriteFile(path, b.Bytes(), 0o644)
}

func escape(s string) (string, error) {
	var b bytes.Buffer
	err := xml.EscapeText(&b, []byte(s))
	return b.String(), err
}

// Load starts the agent now and at every login, replacing a loaded copy.
func Load(plist string) error {
	domain := "gui/" + strconv.Itoa(os.Getuid())
	_ = exec.Command("launchctl", "bootout", domain+"/"+Label).Run()
	if out, err := exec.Command("launchctl", "bootstrap", domain, plist).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Unload stops the agent; an agent that is not loaded is already stopped.
func Unload() {
	_ = exec.Command("launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/"+Label).Run()
}

// ResolvePath returns path with each link to a link collapsed and each
// relative or missing entry dropped. A version manager's per-shell link (fnm's)
// points at its stable alias link, which then stays in loopd's PATH and
// follows the manager's default; a link to a real directory, such as
// Homebrew's opt link, stays as it is and follows upgrades.
func ResolvePath(path string) string {
	var out []string
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}
		dir = collapseLinks(dir)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			out = append(out, dir)
		}
	}
	return strings.Join(out, string(filepath.ListSeparator))
}

// collapseLinks walks dir from the root and replaces each link whose target
// is itself a link with that target, until a link points at a real file.
func collapseLinks(dir string) string {
	out := "/"
	for _, part := range strings.Split(filepath.Clean(dir), "/")[1:] {
		next := filepath.Join(out, part)
		for hops := 0; hops < 40; hops++ {
			target, ok := linkTarget(next)
			if !ok {
				break
			}
			if _, linked := linkTarget(target); !linked {
				break
			}
			next = target
		}
		out = next
	}
	return out
}

func linkTarget(path string) (string, bool) {
	target, err := os.Readlink(path)
	if err != nil {
		return "", false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return target, true
}
