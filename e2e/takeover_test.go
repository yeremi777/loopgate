package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// hangFirst hangs on the first stage, as a long stage would, then behaves like fakeClaude.
func hangFirst(t *testing.T) string {
	marker := filepath.Join(t.TempDir(), "hung-once")
	return "#!/bin/sh\nif [ ! -f " + marker + " ]; then touch " + marker + "; cat >/dev/null; sleep 300; exit 1; fi\n" + fakeClaudeBody
}

func TestTakeoverThenHandbackShipsTheHumansWork(t *testing.T) {
	e := newEnv(t, "exit 0")
	checkout := e.timerLoop(t, "work", hangFirst(t))
	out := e.mustLoopctl("run", "work")
	id := regexp.MustCompile(`job (\d+) running`).FindStringSubmatch(out)[1]
	time.Sleep(500 * time.Millisecond)

	taken := e.mustLoopctl("takeover", id)
	wt := regexp.MustCompile(`cd (\S+)`).FindStringSubmatch(taken)
	if wt == nil || !strings.Contains(taken, "loop/work-") {
		t.Fatalf("takeover output %q", taken)
	}
	if got := field(e.mustLoopctl("logs", id), "state"); got != "manual" {
		t.Fatalf("state after takeover %q", got)
	}
	if _, err := os.Stat(wt[1]); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt[1], "human.txt"), []byte("by hand"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt[1], "add", "human.txt")
	gitIn(t, wt[1], "commit", "-q", "-m", "human work")

	e.mustLoopctl("handback", id)
	e.waitState(t, id, "done")
	if log := gitIn(t, wt[1], "log", "--format=%s"); !strings.Contains(log, "human work") {
		t.Fatalf("handback lost the human's commit:\n%s", log)
	}
	_ = checkout
}

func TestStopEndsARunningJob(t *testing.T) {
	e := newEnv(t, "exit 0")
	e.timerLoop(t, "work", hangFirst(t))
	id := regexp.MustCompile(`job (\d+) running`).FindStringSubmatch(e.mustLoopctl("run", "work"))[1]
	time.Sleep(500 * time.Millisecond)
	e.mustLoopctl("stop", "work")
	e.waitState(t, id, "failed")
	if out, code := e.loopctl("stop", "work"); code == 0 || !strings.Contains(out, "no running job") {
		t.Fatalf("second stop: exit %d, %q", code, out)
	}
}

// timerLoop adds a timer loop named name whose stages run the claude script, and returns its checkout.
func (e env) timerLoop(t *testing.T, name, claude string) string {
	t.Helper()
	fakes := strings.SplitN(e.path, ":", 2)[0]
	writeExec(t, filepath.Join(fakes, "claude"), claude)
	writeExec(t, filepath.Join(fakes, "gh"), "#!/bin/sh\n[ \"$1\" = auth ] && exit 0\ncat >/dev/null\necho https://github.com/me/app/pull/5\n")
	writeExec(t, filepath.Join(fakes, "osascript"), "#!/bin/sh\n")
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	gitIn(t, root, "init", "-q", "--bare", "-b", "main", origin)
	checkout := filepath.Join(root, "checkout")
	gitIn(t, root, "clone", "-q", origin, checkout)
	gitIn(t, checkout, "commit", "-q", "--allow-empty", "-m", "base")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:main")
	gitIn(t, checkout, "remote", "set-head", "origin", "main")
	file := filepath.Join(root, name+".yaml")
	loop := "name: " + name + "\ncheckout: " + checkout + "\ntrigger:\n  timer: { every: 24h }\ntask: add ok.txt\nverify: test -f ok.txt\nbounds: { max_iterations: 2, timeout: 2m }\n"
	if err := os.WriteFile(file, []byte(loop), 0o600); err != nil {
		t.Fatal(err)
	}
	e.startLoopd()
	e.mustLoopctl("add", file)
	return checkout
}

func (e env) waitState(t *testing.T, id, want string) {
	t.Helper()
	var logs string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if logs = e.mustLoopctl("logs", id); field(logs, "state") == want {
			return
		}
	}
	t.Fatalf("job %s never reached %s:\n%s", id, want, logs)
}
