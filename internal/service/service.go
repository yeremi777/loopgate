// Package service installs loopd as a macOS launchd agent.
package service

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"
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

// unloadWait bounds how long Load waits for a loaded loopd to stop, which
// ends its running Job first.
const unloadWait = 60 * time.Second

// Load starts the agent now and at every login, replacing a loaded copy, and
// tells out when it waits for that copy to stop.
func Load(plist string, out io.Writer) error {
	domain := "gui/" + strconv.Itoa(os.Getuid())
	bootout, _ := exec.Command("launchctl", "bootout", domain+"/"+Label).CombinedOutput()
	// bootout returns while launchd is still stopping the old loopd, and
	// bootstrap fails with an I/O error until launchd no longer has it.
	loaded := func() bool { return exec.Command("launchctl", "print", domain+"/"+Label).Run() == nil }
	for deadline, waiting := time.Now().Add(unloadWait), false; loaded(); time.Sleep(100 * time.Millisecond) {
		if !waiting {
			_, _ = fmt.Fprintln(out, "waiting for the running loopd to stop")
			waiting = true
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the running loopd did not stop within %d seconds (launchctl bootout: %s); run loopctl install again",
				int(unloadWait.Seconds()), strings.TrimSpace(string(bootout)))
		}
	}
	if out, err := exec.Command("launchctl", "bootstrap", domain, plist).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Unload stops the agent; an agent that is not loaded is already stopped.
func Unload() {
	_ = exec.Command("launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/"+Label).Run()
}

// ResolvePath returns path with each link to a link replaced by its target
// and each relative or missing entry dropped. A version manager's per-shell
// link (fnm's) points at its stable alias link, which then stays in loopd's
// PATH and follows the manager's default; a link to a real directory, such as
// Homebrew's opt link, stays as it is and follows upgrades. Only one hop is
// taken, so a chain such as Nix's profile links keeps its stable middle link.
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
// is itself a link with that target.
func collapseLinks(dir string) string {
	out := "/"
	for _, part := range strings.Split(filepath.Clean(dir), "/")[1:] {
		next := filepath.Join(out, part)
		if target, ok := linkTarget(next); ok {
			if _, linked := linkTarget(target); linked {
				next = target
			}
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
