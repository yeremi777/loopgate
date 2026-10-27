package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// hangFirst hangs on the first stage, as a long stage would, then behaves like
// fakeClaude. The returned wait blocks until that first stage has started.
func hangFirst(t *testing.T) (script string, wait func()) {
	marker := filepath.Join(t.TempDir(), "hung-once")
	script = "#!/bin/sh\nif [ ! -f " + marker + " ]; then touch " + marker + "; cat >/dev/null; sleep 300; exit 1; fi\n" + fakeClaudeBody
	return script, func() {
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if _, err := os.Stat(marker); err == nil {
				return
			}
		}
		t.Fatal("first stage never started")
	}
}

func TestTakeoverThenHandbackShipsTheHumansWork(t *testing.T) {
	e := newEnv(t, "exit 0")
	claude, started := hangFirst(t)
	e.timerLoop(t, "work", claude)
	out := e.mustLoopctl("run", "work")
	id := regexp.MustCompile(`job ([0-9a-f]{12}) running`).FindStringSubmatch(out)[1]
	started()

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
}

func TestPrefixOfAJobIDInAnyCaseNamesTheJobInEveryJobCommand(t *testing.T) {
	e := newEnv(t, "exit 0")
	claude, started := hangFirst(t)
	checkout := e.timerLoop(t, "work", claude)
	out := e.mustLoopctl("run", "work")
	m := regexp.MustCompile(`job ([0-9a-f]{12}) running on (\S+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("run output %q", out)
	}
	id, branch := m[1], m[2]
	if !strings.HasSuffix(branch, "-"+id) {
		t.Fatalf("branch %s does not end in the job id %s", branch, id)
	}
	if want := filepath.Join(checkout+"-worktrees", id); !strings.HasSuffix(out, " in "+want+"\n") {
		t.Fatalf("run output %q; want the worktree %s", out, want)
	}
	prefix := strings.ToUpper(id[:4])
	started()

	if got := field(e.mustLoopctl("logs", prefix), "job"); got != id+" of loop work" {
		t.Fatalf("logs %s shows job %q", prefix, got)
	}
	if taken := e.mustLoopctl("takeover", prefix); !strings.HasPrefix(taken, "job "+id+" is yours") {
		t.Fatalf("takeover %s: %q", prefix, taken)
	}
	if back := e.mustLoopctl("handback", id[:6]); !strings.HasPrefix(back, "job "+id+" is back") {
		t.Fatalf("handback %s: %q", id[:6], back)
	}
	if followed := e.mustLoopctl("logs", "-f", prefix); !strings.HasSuffix(followed, "job "+id+" done: https://github.com/me/app/pull/5\n") {
		t.Fatalf("logs -f %s:\n%s", prefix, followed)
	}
	for _, unknown := range []string{"zz", id + "0"} {
		if out, code := e.loopctl("logs", unknown); code == 0 || !strings.Contains(out, "not found") {
			t.Fatalf("logs %s: exit %d, %q", unknown, code, out)
		}
	}
}

func TestRemoveRefusesAJobInProgressThenRemovesTheEndedJobAndPrintsItsPR(t *testing.T) {
	e := newEnv(t, "exit 0")
	claude, started := hangFirst(t)
	checkout := e.timerLoop(t, "work", claude)
	m := regexp.MustCompile(`job ([0-9a-f]{12}) running on (\S+) in (\S+)`).FindStringSubmatch(e.mustLoopctl("run", "work"))
	id, branch, wt := m[1], m[2], m[3]
	started()
	if out, code := e.loopctl("rm", id[:5]); code == 0 || !strings.Contains(out, "job "+id+" is running") {
		t.Fatalf("rm of a running job: exit %d, %q", code, out)
	}
	e.mustLoopctl("takeover", id)
	if out, code := e.loopctl("rm", id); code == 0 || !strings.Contains(out, "job "+id+" is manual") {
		t.Fatalf("rm of a manual job: exit %d, %q", code, out)
	}
	e.mustLoopctl("handback", id)
	e.waitState(t, id, "done")

	out := e.mustLoopctl("rm", strings.ToUpper(id[:5]))
	if want := "removed job " + id + " of loop work\npr: https://github.com/me/app/pull/5\n"; out != want {
		t.Fatalf("rm output %q, want %q", out, want)
	}
	for _, path := range []string{wt, checkout + "-worktrees", filepath.Join(e.home, "jobs", id)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s stays after rm: %v", path, err)
		}
	}
	if list := gitIn(t, checkout, "worktree", "list"); strings.Contains(list, id) {
		t.Fatalf("worktree still registered:\n%s", list)
	}
	if heads := gitIn(t, checkout, "branch", "--list", branch); heads != "" {
		t.Fatalf("branch %s stays: %q", branch, heads)
	}
	if out, code := e.loopctl("logs", id); code == 0 || !strings.Contains(out, "not found") {
		t.Fatalf("logs of a removed job: exit %d, %q", code, out)
	}
	if ids := e.jobIDs(); len(ids) != 0 {
		t.Fatalf("list still shows %v", ids)
	}
}

func TestStopEndsARunningJob(t *testing.T) {
	e := newEnv(t, "exit 0")
	claude, started := hangFirst(t)
	e.timerLoop(t, "work", claude)
	id := regexp.MustCompile(`job ([0-9a-f]{12}) running`).FindStringSubmatch(e.mustLoopctl("run", "work"))[1]
	started()
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
	e.load(file)
	e.startLoopd()
	return checkout
}

func (e env) waitState(t *testing.T, id, want string) {
	t.Helper()
	var logs string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if out, code := e.loopctl("logs", id); code == 0 && field(out, "state") == want {
			return
		} else {
			logs = out
		}
	}
	t.Fatalf("job %s never reached %s:\n%s", id, want, logs)
}
