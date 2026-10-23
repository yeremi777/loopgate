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

// askClaude ships a task like fakeClaude, except by the task's first word:
// "ask" blocks on a long question of two lines that carries an escape
// sequence, "hand" blocks on a short one, "boom" reports an error, "tire"
// reports done without committing, and "slow" takes 5 seconds first.
const askClaude = `#!/bin/sh
prompt=$(cat)
r() { printf '{"type":"result","is_error":false,"total_cost_usd":0.1,"structured_output":%s}\n' "$1"; }
case "$prompt" in
/implement\ ask*) r '{"status":"blocked","question":"\u001b[2Jwhich database should the new cache use, postgres in production or sqlite on each machine?\nsecond line","summary":"stopped"}'; exit 0;;
/implement\ hand*) r '{"status":"blocked","question":"hand it over?","summary":"stopped"}'; exit 0;;
/implement\ boom*) printf '%s\n' '{"type":"result","is_error":true,"subtype":"error_max_turns"}'; exit 0;;
/implement\ tire*) r '{"status":"done","summary":"trust me"}'; exit 0;;
/implement\ slow*) sleep 5;;
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

func TestListAgainstALoopdWithIntegerJobIDsNamesInstallAndPrintsNothing(t *testing.T) {
	e := newEnv(t, "exit 0")
	ln, err := net.Listen("unix", filepath.Join(e.home, "loopd.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	job := `{"ID":1,"Loop":"fix","State":"done","Work":"task x","Branch":"loop/fix-20260101-000000-1","Worktree":"/h/jobs/1/worktree","PRURL":"https://github.com/me/app/pull/1"}`
	old := http.NewServeMux()
	old.HandleFunc("GET /loops", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"name":"fix","paused":false,"trigger":"timer","checkout":"/repo"}]}`))
	})
	old.HandleFunc("GET /jobs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[` + job + `]}`))
	})
	old.HandleFunc("GET /jobs/1", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"job":` + job + `,"progress":""}}`))
	})
	old.HandleFunc("POST /jobs/1/takeover", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":` + job + `}`))
	})
	go func() { _ = http.Serve(ln, old) }()

	for _, args := range [][]string{{"list"}, {"logs", "1"}, {"takeover", "1"}} {
		out, code := e.loopctl(args...)
		if code == 0 || !strings.Contains(out, "200 OK") || !strings.Contains(out, "loopctl install") || strings.Contains(out, "fix") {
			t.Fatalf("loopctl %v against a loopd with integer job ids: exit %d\n%s", args, code, out)
		}
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
	for name, task := range map[string]string{"add": "add ok.txt", "ask": "ask which db", "hand": "hand", "boom": "boom", "tire": "tire", "slow": "slow", "idle": "add ok.txt"} {
		file := filepath.Join(root, name+".yaml")
		loop := "name: " + name + "\ncheckout: " + checkout + "\ntrigger:\n  timer: { every: 1h }\ntask: " + task + "\nverify: test -f ok.txt\nbounds: { max_iterations: 1, timeout: 1m }\n"
		if err := os.WriteFile(file, []byte(loop), 0o600); err != nil {
			t.Fatal(err)
		}
		e.load(file)
	}
	e.startLoopd()

	id := map[string]string{}
	for _, run := range [][2]string{{"add", "done"}, {"ask", "blocked"}, {"hand", "blocked"}, {"boom", "failed"}, {"tire", "exhausted"}} {
		loop, state := run[0], run[1]
		id[loop] = regexp.MustCompile(`job ([0-9a-f]{12}) running`).FindStringSubmatch(e.mustLoopctl("run", loop))[1]
		for deadline := time.Now().Add(30 * time.Second); field(e.mustLoopctl("logs", id[loop]), "state") != state; time.Sleep(100 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("job %s of %s never reached %s", id[loop], loop, state)
			}
		}
	}
	e.mustLoopctl("takeover", id["hand"])
	e.mustLoopctl("remove", "add")
	id["slow"] = regexp.MustCompile(`job ([0-9a-f]{12}) running`).FindStringSubmatch(e.mustLoopctl("run", "slow"))[1]

	list := e.cmd("loopctl", "list")
	list.Env = append(list.Env, "HOME="+root+"/")
	plain, err := list.CombinedOutput()
	if err != nil {
		t.Fatalf("list: %v\n%s", err, plain)
	}
	job := "  JOB" + strings.Repeat(" ", 11)
	want := strings.Join([]string{
		"add   removed",
		job + "STATE  WORK             RESULT",
		"  " + id["add"] + "  done   task add ok.txt  https://github.com/me/app/pull/9",
		"",
		"ask   active  timer",
		"  checkout: ~/checkout",
		job + "STATE    WORK               RESULT",
		"  " + id["ask"] + "  blocked  task ask which db  �[2Jwhich database should the new cache use, postgres in pr…",
		"",
		"boom  active  timer",
		"  checkout: ~/checkout",
		job + "STATE   WORK       RESULT",
		"  " + id["boom"] + "  failed  task boom  claude reported error: error_max_turns",
		"",
		"hand  active  timer",
		"  checkout: ~/checkout",
		job + "STATE   WORK       RESULT",
		"  " + id["hand"] + "  manual  task hand  taken over",
		"",
		"idle  active  timer",
		"  checkout: ~/checkout",
		"  no jobs",
		"",
		"slow  active  timer",
		"  checkout: ~/checkout",
		job + "STATE    WORK       RESULT",
		"  " + id["slow"] + "  running  task slow",
		"",
		"tire  active  timer",
		"  checkout: ~/checkout",
		job + "STATE      WORK       RESULT",
		"  " + id["tire"] + "  exhausted  task tire  1 implement runs did not pass the checks",
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
	for _, code := range []string{"\x1b[1mask", "\x1b[33mblocked", "\x1b[31mfailed", "\x1b[31mremoved", "\x1b[32mdone", "\x1b[32mactive", "\x1b[36mrunning", "\x1b[33mmanual", "\x1b[31mexhausted", "\x1b[2mJOB"} {
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
