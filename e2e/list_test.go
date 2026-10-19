package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// askClaude ships a task like fakeClaude, but blocks a task that starts with
// "ask" on a long question of two lines that carries an escape sequence.
const askClaude = `#!/bin/sh
prompt=$(cat)
case "$prompt" in
/implement\ ask*) printf '%s\n' '{"type":"result","is_error":false,"total_cost_usd":0.1,"structured_output":{"status":"blocked","question":"\u001b[2Jwhich database should the new cache use, postgres in production or sqlite on each machine?\nsecond line","summary":"stopped"}}'; exit 0;;
esac
printf '%s' "$prompt" | {
` + fakeClaudeBody + `}
`

func TestListShowsJobsWithTheIdsThatCommandsTake(t *testing.T) {
	e := newEnv(t, "exit 0")
	fakes := strings.SplitN(e.path, ":", 2)[0]
	writeExec(t, filepath.Join(fakes, "claude"), askClaude)
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
	for name, task := range map[string]string{"deps": "add ok.txt", "ask": "ask which db"} {
		file := filepath.Join(root, name+".yaml")
		loop := "name: " + name + "\ncheckout: " + checkout + "\ntrigger:\n  timer: { every: 1h }\ntask: " + task + "\nverify: test -f ok.txt\nbounds: { max_iterations: 2, timeout: 1m }\n"
		if err := os.WriteFile(file, []byte(loop), 0o600); err != nil {
			t.Fatal(err)
		}
		e.load(file)
	}
	e.startLoopd()

	if out := e.mustLoopctl("list"); strings.Contains(out, "JOB") || !hasRow(out, "deps", "active") {
		t.Fatalf("list before any job:\n%s", out)
	}
	for _, run := range [][2]string{{"deps", "done"}, {"ask", "blocked"}} {
		loop, state := run[0], run[1]
		id := regexp.MustCompile(`job (\d+) running`).FindStringSubmatch(e.mustLoopctl("run", loop))[1]
		for deadline := time.Now().Add(30 * time.Second); field(e.mustLoopctl("logs", id), "state") != state; time.Sleep(100 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("job %s of %s never reached %s", id, loop, state)
			}
		}
	}

	out := e.mustLoopctl("list")
	loops, jobs, ok := strings.Cut(out, "\n\n")
	if !ok || !hasRow(loops, "ask", "active") || !hasRow(loops, "deps", "active") {
		t.Fatalf("list has no loops table then a blank line:\n%s", out)
	}
	rows := strings.Split(strings.TrimSuffix(jobs, "\n"), "\n")
	if len(rows) != 3 || strings.Join(strings.Fields(rows[0]), " ") != "JOB LOOP STATE WORK PR / QUESTION" {
		t.Fatalf("jobs table:\n%s", jobs)
	}
	question := "�[2Jwhich database should the new cache use, postgres in pr…"
	for i, want := range [][]string{
		{"2", "ask", "blocked", "task ask which db", question},
		{"1", "deps", "done", "task add ok.txt", "https://github.com/me/app/pull/9"},
	} {
		if got := strings.Fields(rows[i+1]); strings.Join(got, " ") != strings.Join(strings.Fields(strings.Join(want, " ")), " ") {
			t.Fatalf("jobs row %d %q, want %q", i+1, rows[i+1], want)
		}
	}
	if strings.ContainsRune(out, '\x1b') || strings.Contains(out, "second line") {
		t.Fatalf("list printed a raw escape or a second line:\n%q", out)
	}
}
