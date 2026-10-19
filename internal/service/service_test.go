package service

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInstallWritesLintCleanLaunchAgent(t *testing.T) {
	agents := t.TempDir()
	cfg := Config{Loopd: "/opt/loopgate/bin/loopd", Home: "/Users/me/.loopd", Path: "/opt/homebrew/bin:/usr/bin:/bin"}
	path, err := Install(agents, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(agents, Label+".plist"); path != want {
		t.Fatalf("path %q, want %q", path, want)
	}
	if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint: %v: %s", err, out)
	}
	out, err := exec.Command("plutil", "-convert", "json", "-o", "-", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Label                string            `json:"Label"`
		ProgramArguments     []string          `json:"ProgramArguments"`
		RunAtLoad            bool              `json:"RunAtLoad"`
		EnvironmentVariables map[string]string `json:"EnvironmentVariables"`
		StandardErrorPath    string            `json:"StandardErrorPath"`
		KeepAlive            map[string]bool   `json:"KeepAlive"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Label != Label || !reflect.DeepEqual(got.ProgramArguments, []string{cfg.Loopd}) || !got.RunAtLoad {
		t.Fatalf("plist %+v", got)
	}
	if want := map[string]string{"PATH": cfg.Path, "LOOPD_HOME": cfg.Home}; !reflect.DeepEqual(got.EnvironmentVariables, want) {
		t.Fatalf("env %v, want %v", got.EnvironmentVariables, want)
	}
	if got.StandardErrorPath != "/Users/me/.loopd/loopd.log" {
		t.Fatalf("log path %q", got.StandardErrorPath)
	}
	if ok, set := got.KeepAlive["SuccessfulExit"]; !set || ok {
		t.Fatalf("KeepAlive %v, want restart only after a crash", got.KeepAlive)
	}
}

func TestInstallEscapesXML(t *testing.T) {
	path, err := Install(t.TempDir(), Config{Loopd: "/a&b/loopd", Home: "/h<x>", Path: "/bin"})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint: %v: %s", err, out)
	}
}

func TestInstallRefusesRelativeOrMissingParts(t *testing.T) {
	for _, cfg := range []Config{
		{Loopd: "loopd", Home: "/h", Path: "/bin"},
		{Loopd: "/x/loopd", Home: "", Path: "/bin"},
		{Loopd: "/x/loopd", Home: "/h", Path: ""},
	} {
		if _, err := Install(t.TempDir(), cfg); err == nil {
			t.Fatalf("config %+v accepted", cfg)
		}
	}
}

func TestResolvePathCollapsesLinksToLinksAndDropsWhatLaunchdCannotUse(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	version := filepath.Join(root, "node-versions", "v20")
	plain := filepath.Join(root, "plain")
	for _, dir := range []string{filepath.Join(version, "bin"), plain} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "aliases-default")
	shell := filepath.Join(root, "multishell")
	opt := filepath.Join(root, "opt-node")
	for link, target := range map[string]string{alias: version, shell: alias, opt: version} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	path := strings.Join([]string{
		filepath.Join(shell, "bin"),
		filepath.Join(opt, "bin"),
		filepath.Join(root, "gone"),
		"", ".", "bin",
		plain,
	}, ":")
	want := strings.Join([]string{filepath.Join(alias, "bin"), filepath.Join(opt, "bin"), plain}, ":")
	if got := ResolvePath(path); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
