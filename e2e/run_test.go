package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const fakeClaude = "#!/bin/sh\n" + fakeClaudeBody

const fakeClaudeBody = `prompt=$(cat)
result() { printf '{"is_error":false,"total_cost_usd":0.1,"structured_output":{"status":"done","summary":"%s"}}\n' "$1"; }
case "$prompt" in
/implement*) echo ok > ok.txt && git add ok.txt && git -c user.name=t -c user.email=t@example.com commit -q -m ok && result "added ok";;
/rubric*) result "clean";;
/land*) result 'Add ok\n\nbody from land';;
esac
`

func TestRunShipsDraftPRAndLogsShowIt(t *testing.T) {
	e := newEnv(t, "exit 0")
	rec := t.TempDir()
	fakes := strings.SplitN(e.path, ":", 2)[0]
	writeExec(t, filepath.Join(fakes, "claude"), fakeClaude)
	writeExec(t, filepath.Join(fakes, "gh"), `#!/bin/sh
[ "$1" = auth ] && exit 0
printf '%s\n' "$@" > `+rec+`/gh.args
cat > `+rec+`/gh.body
echo https://github.com/me/app/pull/9
`)
	writeExec(t, filepath.Join(fakes, "osascript"), `#!/bin/sh
printf '%s\n' "$@" > `+rec+`/notify
`)

	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	gitIn(t, root, "init", "-q", "--bare", "-b", "main", origin)
	checkout := filepath.Join(root, "checkout")
	gitIn(t, root, "clone", "-q", origin, checkout)
	gitIn(t, checkout, "commit", "-q", "--allow-empty", "-m", "base")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:main")
	gitIn(t, checkout, "remote", "set-head", "origin", "main")

	file := filepath.Join(root, "deps.yaml")
	loop := "name: deps\ncheckout: " + checkout + "\ntrigger:\n  timer: { every: 1h }\ntask: add ok.txt\nverify: test -f ok.txt\nbounds: { max_iterations: 2, timeout: 1m }\n"
	if err := os.WriteFile(file, []byte(loop), 0o600); err != nil {
		t.Fatal(err)
	}
	e.startLoopd()
	e.mustLoopctl("add", file)

	out := e.mustLoopctl("run", "deps")
	m := regexp.MustCompile(`job (\d+) running on (loop/deps-\S+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("run output %q", out)
	}
	id, branch := m[1], m[2]

	var logs string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if logs = e.mustLoopctl("logs", id); field(logs, "state") == "done" {
			break
		}
	}
	for name, want := range map[string]string{"state": "done", "pr": "https://github.com/me/app/pull/9", "cost": "$0.30", "work": "task add ok.txt"} {
		if got := field(logs, name); got != want {
			t.Fatalf("logs %s = %q, want %q:\n%s", name, got, want, logs)
		}
	}
	if heads := gitIn(t, root, "ls-remote", origin, "refs/heads/"+branch); heads == "" {
		t.Fatalf("branch %s not pushed to origin", branch)
	}
	if body := readFile(t, filepath.Join(rec, "gh.body")); !strings.Contains(body, "## loopgate") || !strings.Contains(body, "body from land") {
		t.Fatalf("pr body:\n%s", body)
	}
	if args := readFile(t, filepath.Join(rec, "gh.args")); !strings.Contains(args, "--draft\n") {
		t.Fatalf("gh args %q lack --draft", args)
	}
	note := filepath.Join(rec, "notify")
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(note); err == nil {
			break
		}
	}
	if got := readFile(t, note); !strings.Contains(got, "https://github.com/me/app/pull/9") {
		t.Fatalf("notification %q", got)
	}
}

func TestRunWhileBusyAndUnknownJob(t *testing.T) {
	e := newEnv(t, "exit 0")
	e.startLoopd()
	if out, code := e.loopctl("logs", "99"); code == 0 || !strings.Contains(out, "not found") {
		t.Fatalf("logs of unknown job: exit %d, %q", code, out)
	}
	if out, code := e.loopctl("run", "nope"); code == 0 || !strings.Contains(out, "unknown loop") {
		t.Fatalf("run of unknown loop: exit %d, %q", code, out)
	}
}

// field returns the value of a "name: value" line of loopctl logs.
func field(logs, name string) string {
	for _, line := range strings.Split(logs, "\n") {
		if v, ok := strings.CutPrefix(line, name+":"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func writeExec(t *testing.T, path, script string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTimerLoopFiresWithoutRun(t *testing.T) {
	e := newEnv(t, "exit 0")
	fakes := strings.SplitN(e.path, ":", 2)[0]
	writeExec(t, filepath.Join(fakes, "claude"), fakeClaude)
	writeExec(t, filepath.Join(fakes, "gh"), "#!/bin/sh\n[ \"$1\" = auth ] && exit 0\ncat >/dev/null\necho https://github.com/me/app/pull/3\n")
	writeExec(t, filepath.Join(fakes, "osascript"), "#!/bin/sh\n")

	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	gitIn(t, root, "init", "-q", "--bare", "-b", "main", origin)
	checkout := filepath.Join(root, "checkout")
	gitIn(t, root, "clone", "-q", origin, checkout)
	gitIn(t, checkout, "commit", "-q", "--allow-empty", "-m", "base")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:main")
	gitIn(t, checkout, "remote", "set-head", "origin", "main")
	file := filepath.Join(root, "tick.yaml")
	loop := "name: tick\ncheckout: " + checkout + "\ntrigger:\n  timer: { every: 1s }\ntask: add ok.txt\nverify: test -f ok.txt\nbounds: { max_iterations: 1, timeout: 1m }\n"
	if err := os.WriteFile(file, []byte(loop), 0o600); err != nil {
		t.Fatal(err)
	}
	e.startLoopd()
	e.mustLoopctl("add", file)
	for deadline := time.Now().Add(40 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		if out, code := e.loopctl("logs", "1"); code == 0 && field(out, "state") == "done" {
			return
		}
	}
	out, _ := e.loopctl("logs", "1")
	t.Fatalf("timer loop never ran a job to done:\n%s", out)
}
