package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tryClaude behaves like fakeClaude, and its land stage writes a test guide
// with a terminal escape in it.
const tryClaude = "#!/bin/sh\n" + `prompt=$(cat)
result() { printf '{"type":"result","is_error":false,"total_cost_usd":0.1,"structured_output":{"status":"done","summary":"%s"%s}}\n' "$1" "$2"; }
case "$prompt" in
/implement*) echo ok > ok.txt && git add ok.txt && git -c user.name=t -c user.email=t@example.com commit -q -m ok && result "added ok";;
/rubric*) result "clean";;
/land*) result 'Add ok\n\nbody from land' ',"try":"Start it with make run; it needs DATABASE_URL.\n\u001b[2JGET /ok answers ok."';;
esac
`

func TestTryCopiesTheIncludedFilesAndPrintsTheTestGuide(t *testing.T) {
	e := newEnv(t, "exit 0")
	checkout := e.timerLoop(t, "work", tryClaude)
	if err := os.WriteFile(filepath.Join(checkout, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, checkout, "add", ".gitignore")
	gitIn(t, checkout, "commit", "-q", "-m", "ignore .env")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:main")
	m := regexp.MustCompile(`job ([0-9a-f]{12}) running on \S+ in (\S+)`).FindStringSubmatch(e.mustLoopctl("run", "work"))
	id, wt := m[1], m[2]
	e.waitState(t, id, "done")
	try := func() string {
		t.Helper()
		cmd := e.cmd("loopctl", "try", id[:5])
		cmd.Env = append(cmd.Env, "HOME="+filepath.Dir(e.home))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("loopctl try: %v: %s", err, out)
		}
		return string(out)
	}
	worktree := "worktree: ~/" + filepath.Base(e.home) + "/jobs/" + id + "/worktree\n"
	include, ok := strings.CutPrefix(filepath.Join(checkout, ".worktreeinclude"), filepath.Dir(e.home)+"/")
	if !ok {
		t.Fatalf("checkout %s is not under %s", checkout, filepath.Dir(e.home))
	}
	guide := "\ntest guide:\nStart it with make run; it needs DATABASE_URL.\n�[2JGET /ok answers ok.\n"

	if out, want := try(), worktree+"copied: nothing; list the gitignored files the app needs, such as .env, in ~/"+include+"\n"+guide; out != want {
		t.Fatalf("try without .worktreeinclude:\n%q\nwant\n%q", out, want)
	}

	for name, body := range map[string]string{".worktreeinclude": ".env\n", ".env": "DATABASE_URL=postgres://secret\n"} {
		if err := os.WriteFile(filepath.Join(checkout, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if out, want := try(), worktree+"copied: .env\n"+guide; out != want {
		t.Fatalf("try with .worktreeinclude:\n%q\nwant\n%q", out, want)
	}
	if got := readFile(t, filepath.Join(wt, ".env")); got != "DATABASE_URL=postgres://secret\n" {
		t.Fatalf(".env in the worktree %q", got)
	}
	if status := gitIn(t, wt, "status", "--porcelain"); status != "" {
		t.Fatalf("worktree status after try %q", status)
	}
	if out, code := e.loopctl("takeover", id); code == 0 || !strings.Contains(out, "loopctl try "+id) {
		t.Fatalf("takeover of a done job: exit %d, %q", code, out)
	}
}

func TestTryOfAJobThatIsNotDoneNamesItsState(t *testing.T) {
	e := newEnv(t, "exit 0")
	claude, started := hangFirst(t)
	e.timerLoop(t, "work", claude)
	id := regexp.MustCompile(`job ([0-9a-f]{12}) running`).FindStringSubmatch(e.mustLoopctl("run", "work"))[1]
	started()
	if out, code := e.loopctl("try", id); code == 0 || !strings.Contains(out, "job "+id+" is running: only a done job can be tried") {
		t.Fatalf("try of a running job: exit %d, %q", code, out)
	}
}

func TestHelpNamesTry(t *testing.T) {
	e := newEnv(t, "exit 0")
	if out, code := e.loopctl("help"); code != 0 || !strings.Contains(out, "\n  loopctl try <job> ") {
		t.Fatalf("usage lacks a loopctl try <job> line: exit %d:\n%s", code, out)
	}
}
