// Package e2e drives the built loopd and loopctl binaries.
package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "lgbin")
	if err != nil {
		panic(err)
	}
	bin = dir
	build := exec.Command("go", "build", "-o", dir, "../cmd/loopd", "../cmd/loopctl")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

const fixLoop = `
name: fix
checkout: /src/app
trigger:
  github: { repo: me/app, label: agent, every: 10m }
verify: go test ./...
bounds: { max_iterations: 5, timeout: 20m }
`

func TestLoopctlWithoutLoopd(t *testing.T) {
	env := newEnv(t, "exit 0")
	out, code := env.loopctl("list")
	if code == 0 || !strings.Contains(out, "loopd not running") {
		t.Fatalf("exit %d, output %q; want non-zero and loopd not running", code, out)
	}
}

func TestLoopdRefusesWithoutGHLogin(t *testing.T) {
	env := newEnv(t, "exit 1")
	cmd := env.cmd("loopd")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "gh auth login") {
		t.Fatalf("err %v, output %q; want failure naming gh auth login", err, out)
	}
}

func TestLoopdServesLoopctlAndKeepsStateAcrossRestart(t *testing.T) {
	env := newEnv(t, "exit 0")
	loopd := env.startLoopd()

	file := filepath.Join(t.TempDir(), "fix.yaml")
	if err := os.WriteFile(file, []byte(fixLoop), 0o600); err != nil {
		t.Fatal(err)
	}
	env.mustLoopctl("add", file)
	env.mustLoopctl("pause", "fix")
	if out := env.mustLoopctl("list"); !hasRow(out, "fix", "paused") {
		t.Fatalf("list after pause: %q", out)
	}

	second, err := env.cmd("loopd").CombinedOutput()
	if want := "pid " + strconv.Itoa(loopd.Process.Pid); err == nil || !strings.Contains(string(second), want) {
		t.Fatalf("second loopd: err %v, output %q; want failure naming %q", err, second, want)
	}

	if err := loopd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := loopd.Wait(); err != nil {
		t.Fatalf("loopd exit after SIGTERM: %v", err)
	}
	env.startLoopd()
	if out := env.mustLoopctl("list"); !hasRow(out, "fix", "paused") {
		t.Fatalf("list after restart: %q", out)
	}
}

type env struct {
	t    *testing.T
	home string
	path string
}

func newEnv(t *testing.T, ghBody string) env {
	t.Helper()
	home, err := os.MkdirTemp("", "lg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	ghDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(ghDir, "gh"), []byte("#!/bin/sh\n"+ghBody+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return env{t: t, home: home, path: ghDir + ":" + os.Getenv("PATH")}
}

func (e env) cmd(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(filepath.Join(bin, name), args...)
	cmd.Env = append(os.Environ(), "LOOPD_HOME="+e.home, "PATH="+e.path)
	return cmd
}

func (e env) loopctl(args ...string) (string, int) {
	out, err := e.cmd("loopctl", args...).CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok {
		return string(out), exit.ExitCode()
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return string(out), 0
}

func (e env) mustLoopctl(args ...string) string {
	e.t.Helper()
	out, code := e.loopctl(args...)
	if code != 0 {
		e.t.Fatalf("loopctl %v: exit %d: %s", args, code, out)
	}
	return out
}

func (e env) startLoopd() *exec.Cmd {
	e.t.Helper()
	cmd := e.cmd("loopd")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, code := e.loopctl("list"); code == 0 {
			return cmd
		}
	}
	e.t.Fatalf("loopd did not answer: %s", stderr.String())
	return nil
}

func hasRow(out, name, state string) bool {
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == name && f[1] == state {
			return true
		}
	}
	return false
}


func TestInstallStartsTheAgentAndInstallsTheSkillThenUninstallUndoesIt(t *testing.T) {
	e := newEnv(t, "exit 0")
	home := t.TempDir()
	calls := filepath.Join(t.TempDir(), "launchctl")
	fakes := strings.SplitN(e.path, ":", 2)[0]
	writeExec(t, filepath.Join(fakes, "launchctl"), "#!/bin/sh\necho \"$*\" >> "+calls+"\n[ \"$1\" = bootout ] && exit 3\nexit 0\n")
	run := func(args ...string) string {
		t.Helper()
		cmd := e.cmd("loopctl", args...)
		cmd.Env = append(cmd.Env, "HOME="+home)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("loopctl %v: %v: %s", args, err, out)
		}
		return string(out)
	}

	run("install")
	plist := filepath.Join(home, "Library/LaunchAgents/loopgate.loopd.plist")
	if lint, err := exec.Command("plutil", "-lint", plist).CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint: %v: %s", err, lint)
	}
	args, err := exec.Command("plutil", "-extract", "ProgramArguments.0", "raw", plist).Output()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(bin, "loopd"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(args)); got != want {
		t.Fatalf("agent runs %q, want the loopd next to loopctl", got)
	}
	skill := filepath.Join(home, ".claude/skills/loopgate/SKILL.md")
	if body := readFile(t, skill); !strings.Contains(body, "name: loopgate") {
		t.Fatalf("skill not installed: %q", body)
	}
	uid := strconv.Itoa(os.Getuid())
	if got := readFile(t, calls); got != "bootout gui/"+uid+"/loopgate.loopd\nbootstrap gui/"+uid+" "+plist+"\n" {
		t.Fatalf("launchctl calls %q", got)
	}

	run("uninstall")
	for _, gone := range []string{plist, skill} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s still there after uninstall", gone)
		}
	}
}

func TestHelpExitsZeroAndExplainsStop(t *testing.T) {
	e := newEnv(t, "exit 0")
	for _, arg := range []string{"help", "-h", "--help"} {
		out, code := e.loopctl(arg)
		if code != 0 || !strings.Contains(out, "takeover <job>") {
			t.Fatalf("loopctl %s: exit %d, %q", arg, code, out)
		}
		if !strings.Contains(out, "stop <name>") || !strings.Contains(out, "end the loop's running job") {
			t.Fatalf("usage does not say what stop does:\n%s", out)
		}
	}
}

func TestVersionNamesTheCommitItWasBuiltFrom(t *testing.T) {
	e := newEnv(t, "exit 0")
	head, err := exec.Command("git", "rev-parse", "--short=12", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{"version", "--version"} {
		out, code := e.loopctl(arg)
		if code != 0 || !strings.HasPrefix(out, "loopgate v") || !strings.Contains(out, strings.TrimSpace(string(head))) {
			t.Fatalf("loopctl %s: exit %d, %q; want a version naming commit %s", arg, code, out, head)
		}
	}
}
