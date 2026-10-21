package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

// streamClaude prints stream-json as claude does: assistant events, then the
// result line. implement waits a while mid-stage so a viewer attaches inside it.
const streamClaude = `#!/bin/sh
prompt=$(cat)
say() { printf '{"type":"assistant","parent_tool_use_id":null,"message":{"content":[%s]}}\n' "$1"; }
result() { printf '{"type":"result","is_error":false,"total_cost_usd":0.1,"structured_output":{"status":"done","summary":"%s"}}\n' "$1"; }
long=$(printf 'all good %.0s' $(seq 1 25))
case "$prompt" in
/implement*)
	say '{"type":"tool_use","name":"Write","input":{"file_path":"ok.txt"}}'
	say '{"type":"text","text":"'"$long"'"}'
	say '{"type":"text","text":"\u001b]52;c;cHduZWQ=\u0007\u001b[2Jcleared\u202ereversed"}'
	sleep STAGE_SLEEP
	echo ok > ok.txt && git add ok.txt && git -c user.name=t -c user.email=t@example.com commit -q -m ok && result "added ok";;
/rubric*) say '{"type":"tool_use","name":"Bash","input":{"command":"git diff main"}}'; result "clean";;
/land*) result 'Add ok\n\nbody from land';;
esac
`

func TestLogsFollowPrintsTheJobLiveInOrderAndExitsWhenItShips(t *testing.T) {
	e, id := startStreamJob(t, "2")
	tty := exec.Command("script", "-q", "/dev/null", "sh", "-c", "stty cols 60; exec \"$0\" logs -f \"$1\"", filepath.Join(bin, "loopctl"), id)
	tty.Env = e.cmd("loopctl").Env
	var ttyOut strings.Builder
	tty.Stdout = &ttyOut
	if err := tty.Start(); err != nil {
		t.Fatal(err)
	}

	out, code := e.loopctl("logs", "-f", id)
	if code != 0 {
		t.Fatalf("logs -f exit %d:\n%s", code, out)
	}
	long := strings.Repeat("all good ", 25)
	want := []string{
		"implement 1  start",
		"implement 1  Write ok.txt",
		"implement 1  " + strings.TrimSpace(long),
		"implement 1  �]52;c;cHduZWQ=��[2Jcleared�reversed",
		"implement 1  done $0.10",
		"verify 1  verify: test -f ok.txt",
		"verify 1  pass: verify command exited 0",
		"rubric 1  start",
		"rubric 1  Bash git diff main",
		"rubric 1  done $0.10",
		"land  start",
		"land  done $0.10",
		"publish  draft PR https://github.com/me/app/pull/9",
	}
	stamped := regexp.MustCompile(`(?m)^\d\d:\d\d:\d\d (.*)$`)
	var events []string
	for _, m := range stamped.FindAllStringSubmatch(out, -1) {
		events = append(events, m[1])
	}
	if strings.Join(events, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s\nwant:\n%s\nfull output:\n%s", strings.Join(events, "\n"), strings.Join(want, "\n"), out)
	}
	if strings.ContainsAny(out, "\x1b\x07\u202e") {
		t.Fatalf("logs -f passed the model's control characters to the terminal:\n%q", out)
	}
	if !strings.HasPrefix(out, "job:") || !strings.HasSuffix(out, "job "+id+" done: https://github.com/me/app/pull/9\n") {
		t.Fatalf("logs -f does not open with the snapshot and end with the outcome:\n%s", out)
	}

	if err := tty.Wait(); err != nil {
		t.Fatalf("logs -f on a terminal: %v\n%s", err, ttyOut.String())
	}
	cut := false
	for _, line := range strings.Split(ttyOut.String(), "\n") {
		line = strings.TrimRight(line, "\r")
		if !stamped.MatchString(line) {
			continue
		}
		if n := utf8.RuneCountInString(line); n > 60 {
			t.Fatalf("line of %d characters on a 60-column terminal: %q", n, line)
		}
		cut = cut || strings.HasSuffix(line, "…")
	}
	if !cut {
		t.Fatalf("no line was cut to the terminal's width:\n%s", ttyOut.String())
	}
}

func TestLogsFollowSaysLoopdStoppedWhenLoopdGoesAway(t *testing.T) {
	for name, sig := range map[string]syscall.Signal{"killed": syscall.SIGKILL, "stopped": syscall.SIGTERM} {
		t.Run(name, func(t *testing.T) { followUntilLoopdGets(t, sig) })
	}
}

func followUntilLoopdGets(t *testing.T, sig syscall.Signal) {
	e, id := startStreamJob(t, "10")
	outFile := filepath.Join(t.TempDir(), "out")
	out, err := os.Create(outFile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	follow := e.cmd("loopctl", "logs", "-f", id)
	follow.Stdout, follow.Stderr = out, out
	if err := follow.Start(); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(readFile(t, outFile), "implement 1  Write ok.txt"); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("logs -f never showed the stage:\n%s", readFile(t, outFile))
		}
	}
	if err := e.loopd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	err = follow.Wait()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() == 0 || !strings.Contains(readFile(t, outFile), "loopd stopped") {
		t.Fatalf("logs -f after loopd died: %v\n%s", err, readFile(t, outFile))
	}
}

type streamEnv struct {
	env
	loopd *exec.Cmd
}

// startStreamJob runs a Job of a timer Loop whose claude prints stream-json
// and whose implement stage sleeps for sleep seconds, and returns its id.
func startStreamJob(t *testing.T, sleep string) (streamEnv, string) {
	t.Helper()
	return startJobOf(t, strings.Replace(streamClaude, "STAGE_SLEEP", sleep, 1))
}

// startJobOf runs a Job of a timer Loop whose claude is the script claude,
// and returns its id.
func startJobOf(t *testing.T, claude string) (streamEnv, string) {
	t.Helper()
	return startJobWithin(t, claude, "1m")
}

// startJobWithin is startJobOf on a Loop whose bounds.timeout is timeout.
func startJobWithin(t *testing.T, claude, timeout string) (streamEnv, string) {
	t.Helper()
	e := newEnv(t, "exit 0")
	fakes := strings.SplitN(e.path, ":", 2)[0]
	writeExec(t, filepath.Join(fakes, "claude"), claude)
	writeExec(t, filepath.Join(fakes, "gh"), "#!/bin/sh\n[ \"$1\" = auth ] && exit 0\ncat >/dev/null\necho https://github.com/me/app/pull/9\n")
	writeExec(t, filepath.Join(fakes, "osascript"), "#!/bin/sh\n")

	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	gitIn(t, root, "init", "-q", "--bare", "-b", "main", origin)
	checkout := filepath.Join(root, "checkout")
	gitIn(t, root, "clone", "-q", origin, checkout)
	gitIn(t, checkout, "commit", "-q", "--allow-empty", "-m", "base")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:main")
	gitIn(t, checkout, "remote", "set-head", "origin", "main")
	file := filepath.Join(root, "deps.yaml")
	loop := "name: deps\ncheckout: " + checkout + "\ntrigger:\n  timer: { every: 1h }\ntask: add ok.txt\nverify: test -f ok.txt\nbounds: { max_iterations: 2, timeout: " + timeout + " }\n"
	if err := os.WriteFile(file, []byte(loop), 0o600); err != nil {
		t.Fatal(err)
	}
	e.load(file)
	loopd := e.startLoopd()
	m := regexp.MustCompile(`job ([0-9a-f]{12}) running`).FindStringSubmatch(e.mustLoopctl("run", "deps"))
	if m == nil {
		t.Fatal("run printed no job id")
	}
	return streamEnv{env: e, loopd: loopd}, m[1]
}
