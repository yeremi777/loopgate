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

	"github.com/yeremi777/loopgate/internal/gittest"
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

func fixLoop(checkout string) string {
	return "name: fix\ncheckout: " + checkout + "\ntrigger:\n  github: { repo: me/app, label: agent, every: 10m }\nverify: \"true\"\nbounds: { max_iterations: 5, timeout: 20m }\n"
}

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
	if err := os.WriteFile(file, []byte(fixLoop(gittest.Checkout(t, "https://github.com/me/app.git"))), 0o600); err != nil {
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

func TestAddRunsTheTrialGateAndRefusesARedOne(t *testing.T) {
	env := newEnv(t, "exit 0")
	env.startLoopd()
	checkout := gittest.Checkout(t, "https://github.com/me/app.git")
	write := func(verify string) string {
		file := filepath.Join(t.TempDir(), "fix.yaml")
		loop := strings.Replace(fixLoop(checkout), `verify: "true"`, "verify: "+verify, 1)
		if err := os.WriteFile(file, []byte(loop), 0o600); err != nil {
			t.Fatal(err)
		}
		return file
	}

	out, code := env.loopctl("add", write("echo main is red; exit 1"))
	if code == 0 || !strings.Contains(out, "main is red") || !strings.Contains(out, "not added") {
		t.Fatalf("red add: exit %d, output %q", code, out)
	}
	if out := env.mustLoopctl("list"); hasRow(out, "fix", "active") {
		t.Fatalf("red loop listed: %q", out)
	}

	out = env.mustLoopctl("add", write("echo main is green"))
	if !strings.Contains(out, "added fix") {
		t.Fatalf("green add output %q", out)
	}
	if out := env.mustLoopctl("list"); !hasRow(out, "fix", "active") {
		t.Fatalf("green loop not listed: %q", out)
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

// load puts a Loop file where loopd reads it at start, for a Loop whose
// verify command fails on main until a Job runs, which add would refuse.
func (e env) load(file string) {
	e.t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		e.t.Fatal(err)
	}
	name := strings.TrimSuffix(filepath.Base(file), ".yaml")
	if err := os.MkdirAll(filepath.Join(e.home, "loops"), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.home, "loops", name+".yaml"), data, 0o600); err != nil {
		e.t.Fatal(err)
	}
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
	stageSkills(t, home)
	calls := fakeLaunchd(t, e)
	gone := filepath.Join(t.TempDir(), "gone")
	run := func(args ...string) string {
		t.Helper()
		cmd := e.cmd("loopctl", args...)
		cmd.Env = append(cmd.Env, "HOME="+home, "PATH="+gone+":"+e.path)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("loopctl %v: %v: %s", args, err, out)
		}
		return string(out)
	}

	out := run("install")
	fakes := strings.SplitN(e.path, ":", 2)[0]
	if !strings.Contains(out, filepath.Join(fakes, "gh")) || !strings.Contains(out, "loopd answers") {
		t.Fatalf("install output %q; want where gh resolved and that loopd answers", out)
	}
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
	path, err := exec.Command("plutil", "-extract", "EnvironmentVariables.PATH", "raw", plist).Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(path), gone) {
		t.Fatalf("agent PATH keeps the missing %s: %s", gone, path)
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

func TestInstallFailsWithLoopdsErrorWhenLoopdCannotStart(t *testing.T) {
	e := newEnv(t, "exit 1")
	fakeLaunchd(t, e)
	home := t.TempDir()
	stageSkills(t, home)
	cmd := e.cmd("loopctl", "install")
	cmd.Env = append(cmd.Env, "HOME="+home)
	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "gh auth login") {
		t.Fatalf("err %v, output %q; want failure naming gh auth login", err, out)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("install took %s to see loopd's error", took)
	}
}

func TestInstallRefusesWithoutTheStageSkillsAndChangesNothing(t *testing.T) {
	e := newEnv(t, "exit 0")
	calls := fakeLaunchd(t, e)
	home := t.TempDir()
	stageSkills(t, home)
	if err := os.Remove(filepath.Join(home, ".claude/skills/rubric")); err != nil {
		t.Fatal(err)
	}
	cmd := e.cmd("loopctl", "install")
	cmd.Env = append(cmd.Env, "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "missing stage skills in ~/.claude/skills: rubric; run `npx skills add pwguler/skills -g -a claude-code`") {
		t.Fatalf("err %v, output %q; want failure naming rubric and how to install it", err, out)
	}
	for _, untouched := range []string{calls, filepath.Join(home, "Library"), filepath.Join(home, ".claude/skills/loopgate")} {
		if _, err := os.Stat(untouched); !os.IsNotExist(err) {
			t.Fatalf("%s exists after a refused install", untouched)
		}
	}
}

// stageSkills installs the stage skills under home as npx skills does: each
// ~/.claude/skills entry links to its copy in ~/.agents/skills.
func stageSkills(t *testing.T, home string) {
	t.Helper()
	for _, name := range []string{"drill", "implement", "rubric", "land", "tldr"} {
		src := filepath.Join(home, ".agents/skills", name)
		if err := os.MkdirAll(src, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(home, ".claude/skills", name)
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("../../.agents/skills", name), link); err != nil {
			t.Fatal(err)
		}
	}
}

// fakeLaunchd puts a launchctl on e's PATH that records its calls, starts the
// plist's program with the plist's env and log as launchd does on bootstrap,
// and stops it on bootout. It also adds a claude for loopd to find.
func fakeLaunchd(t *testing.T, e env) (calls string) {
	t.Helper()
	dir := t.TempDir()
	calls, pid := filepath.Join(dir, "calls"), filepath.Join(dir, "pid")
	fakes := strings.SplitN(e.path, ":", 2)[0]
	writeExec(t, filepath.Join(fakes, "claude"), "#!/bin/sh\nexit 0\n")
	writeExec(t, filepath.Join(fakes, "launchctl"), `#!/bin/sh
echo "$*" >> `+calls+`
case "$1" in
bootout) [ -f `+pid+` ] && kill $(cat `+pid+`) && rm `+pid+`; exit 3;;
bootstrap)
	plist=$3
	x() { plutil -extract "$1" raw "$plist"; }
	LOOPD_HOME=$(x EnvironmentVariables.LOOPD_HOME) PATH=$(x EnvironmentVariables.PATH) "$(x ProgramArguments.0)" >> "$(x StandardErrorPath)" 2>&1 &
	echo $! > `+pid+`;;
esac
exit 0
`)
	t.Cleanup(func() {
		if data, err := os.ReadFile(pid); err == nil {
			_ = exec.Command("kill", strings.TrimSpace(string(data))).Run()
		}
	})
	return calls
}

func TestHelpExitsZeroAndExplainsStop(t *testing.T) {
	e := newEnv(t, "exit 0")
	for _, arg := range []string{"help", "-h", "--help"} {
		out, code := e.loopctl(arg)
		if code != 0 || !strings.Contains(out, "takeover <job>") {
			t.Fatalf("loopctl %s: exit %d, %q", arg, code, out)
		}
		if !strings.Contains(out, "stop <loop>") || strings.Contains(out, "<name>") || !strings.Contains(out, "end the loop's running job") {
			t.Fatalf("usage does not say what stop does:\n%s", out)
		}
	}
}

// Go stamps the tag on a tagged commit and a pseudo-version naming the
// commit otherwise; loopctl prints whichever it got.
func TestVersionPrintsTheVersionGoStamped(t *testing.T) {
	e := newEnv(t, "exit 0")
	mod, err := exec.Command("go", "version", "-m", filepath.Join(bin, "loopctl")).Output()
	if err != nil {
		t.Fatal(err)
	}
	var stamped string
	for _, line := range strings.Split(string(mod), "\n") {
		if f := strings.Fields(line); len(f) >= 3 && f[0] == "mod" {
			stamped = f[2]
		}
	}
	if !strings.HasPrefix(stamped, "v") {
		t.Fatalf("go stamped no version into loopctl:\n%s", mod)
	}
	for _, arg := range []string{"version", "--version"} {
		if out, code := e.loopctl(arg); code != 0 || out != "loopgate "+stamped+"\n" {
			t.Fatalf("loopctl %s: exit %d, %q; want %q", arg, code, out, "loopgate "+stamped+"\n")
		}
	}
}
