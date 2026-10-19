package e2e

import (
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// askClaude ships a task like fakeClaude, but blocks a task that starts with
// "ask" on a long question of two lines that carries an escape sequence, and
// reports an error for a task that starts with "boom".
const askClaude = `#!/bin/sh
prompt=$(cat)
case "$prompt" in
/implement\ ask*) printf '%s\n' '{"type":"result","is_error":false,"total_cost_usd":0.1,"structured_output":{"status":"blocked","question":"\u001b[2Jwhich database should the new cache use, postgres in production or sqlite on each machine?\nsecond line","summary":"stopped"}}'; exit 0;;
/implement\ boom*) printf '%s\n' '{"type":"result","is_error":true,"subtype":"error_max_turns"}'; exit 0;;
esac
printf '%s' "$prompt" | {
` + fakeClaudeBody + `}
`

func TestListAgainstAnOlderLoopdNamesInstallAndPrintsNothing(t *testing.T) {
	e := newEnv(t, "exit 0")
	ln, err := net.Listen("unix", filepath.Join(e.home, "loopd.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	old := http.NewServeMux()
	old.HandleFunc("GET /loops", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"name":"fix","paused":false,"trigger":"timer","checkout":"/repo"}]}`))
	})
	go func() { _ = http.Serve(ln, old) }()

	out, code := e.loopctl("list")
	if code == 0 || !strings.Contains(out, "404") || !strings.Contains(out, "loopctl install") || strings.Contains(out, "fix") {
		t.Fatalf("list against a loopd without jobs: exit %d\n%s", code, out)
	}
}

func TestListWithoutLoopsSaysHowToAddOne(t *testing.T) {
	e := newEnv(t, "exit 0")
	e.startLoopd()
	if out := e.mustLoopctl("list"); out != "no loops; add one with loopctl add <loop.yaml>\n" {
		t.Fatalf("list without loops: %q", out)
	}
}

func TestListShowsEachLoopWithItsJobsAndColorsOnlyATerminal(t *testing.T) {
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
	for name, task := range map[string]string{"deps": "add ok.txt", "ask": "ask which db", "boom": "boom"} {
		file := filepath.Join(root, name+".yaml")
		loop := "name: " + name + "\ncheckout: " + checkout + "\ntrigger:\n  timer: { every: 1h }\ntask: " + task + "\nverify: test -f ok.txt\nbounds: { max_iterations: 2, timeout: 1m }\n"
		if err := os.WriteFile(file, []byte(loop), 0o600); err != nil {
			t.Fatal(err)
		}
		e.load(file)
	}
	e.startLoopd()

	for _, run := range [][2]string{{"deps", "done"}, {"ask", "blocked"}, {"boom", "failed"}} {
		loop, state := run[0], run[1]
		id := regexp.MustCompile(`job (\d+) running`).FindStringSubmatch(e.mustLoopctl("run", loop))[1]
		for deadline := time.Now().Add(30 * time.Second); field(e.mustLoopctl("logs", id), "state") != state; time.Sleep(100 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("job %s of %s never reached %s", id, loop, state)
			}
		}
	}
	e.mustLoopctl("remove", "deps")

	list := e.cmd("loopctl", "list")
	list.Env = append(list.Env, "HOME="+root)
	plain, err := list.CombinedOutput()
	if err != nil {
		t.Fatalf("list: %v\n%s", err, plain)
	}
	want := strings.Join([]string{
		"ask   active  timer",
		"  checkout  ~/checkout",
		"  JOB  STATE    WORK               RESULT",
		"  2    blocked  task ask which db  �[2Jwhich database should the new cache use, postgres in pr…",
		"",
		"boom  active  timer",
		"  checkout  ~/checkout",
		"  JOB  STATE   WORK       RESULT",
		"  3    failed  task boom  claude reported error: error_max_turns",
		"",
		"deps  removed",
		"  JOB  STATE  WORK             RESULT",
		"  1    done   task add ok.txt  https://github.com/me/app/pull/9",
		"",
	}, "\n")
	if string(plain) != want {
		t.Fatalf("list:\n%s\nwant:\n%s", plain, want)
	}

	escape := regexp.MustCompile("\x1b\\[[0-9;]*m")
	onTerminal := func(env ...string) string {
		tty := exec.Command("script", "-q", "/dev/null", filepath.Join(bin, "loopctl"), "list")
		tty.Env = append(append(list.Env, "NO_COLOR="), env...)
		out, err := tty.Output()
		if err != nil {
			t.Fatalf("list on a terminal: %v\n%s", err, out)
		}
		// script echoes the end of its empty stdin as ^D and two backspaces.
		return strings.TrimPrefix(strings.ReplaceAll(string(out), "\r\n", "\n"), "^D\b\b")
	}
	colored := onTerminal()
	for _, code := range []string{"\x1b[1mask", "\x1b[33mblocked", "\x1b[31mfailed", "\x1b[31mremoved", "\x1b[32mdone", "\x1b[32mactive", "\x1b[2mJOB"} {
		if !strings.Contains(colored, code) {
			t.Fatalf("list on a terminal lacks %q:\n%q", code, colored)
		}
	}
	if got := escape.ReplaceAllString(colored, ""); got != want {
		t.Fatalf("colors changed the text or its alignment:\n%s\nwant:\n%s", got, want)
	}
	if got := onTerminal("NO_COLOR=1"); got != want {
		t.Fatalf("list on a terminal with NO_COLOR:\n%q", got)
	}
}
