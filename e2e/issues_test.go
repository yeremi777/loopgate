package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitHubLoopBuildsALabeledIssueAndAnswersIt(t *testing.T) {
	e := newEnv(t, "exit 0")
	rec := t.TempDir()
	fakes := strings.SplitN(e.path, ":", 2)[0]
	claude := strings.Replace(fakeClaude, "/rubric*)", "/drill*) result \"add ok.txt\";;\n/rubric*)", 1)
	writeExec(t, filepath.Join(fakes, "claude"), claude)
	writeExec(t, filepath.Join(fakes, "gh"), `#!/bin/sh
case "$1 $2" in
"auth status") exit 0;;
"issue list") echo '[{"number":5,"title":"Add ok","body":"please add ok.txt"}]';;
"issue comment") cat > `+rec+`/comment.$3;;
"pr create") cat >/dev/null; echo https://github.com/me/app/pull/11;;
esac
`)
	writeExec(t, filepath.Join(fakes, "osascript"), "#!/bin/sh\n")

	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	gitIn(t, root, "init", "-q", "--bare", "-b", "main", origin)
	checkout := filepath.Join(root, "checkout")
	gitIn(t, root, "clone", "-q", origin, checkout)
	gitIn(t, checkout, "commit", "-q", "--allow-empty", "-m", "base")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:main")
	gitIn(t, checkout, "remote", "set-head", "origin", "main")
	gitIn(t, checkout, "remote", "set-url", "origin", "https://github.com/me/app.git")
	gitIn(t, checkout, "config", "url."+origin+".insteadOf", "https://github.com/me/app.git")
	file := filepath.Join(root, "issues.yaml")
	loop := "name: issues\ncheckout: " + checkout + "\ntrigger:\n  github: { repo: me/app, label: agent, every: 1s }\nverify: test -f ok.txt\nbounds: { max_iterations: 2, timeout: 1m }\n"
	if err := os.WriteFile(file, []byte(loop), 0o600); err != nil {
		t.Fatal(err)
	}
	e.startLoopd()
	e.mustLoopctl("add", file)

	comment := filepath.Join(rec, "comment.5")
	for deadline := time.Now().Add(45 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		if _, err := os.Stat(comment); err == nil {
			break
		}
	}
	if got := readFile(t, comment); !strings.Contains(got, "https://github.com/me/app/pull/11") {
		t.Fatalf("issue comment %q", got)
	}
	logs := e.mustLoopctl("logs", "1")
	if field(logs, "state") != "done" || !strings.HasPrefix(field(logs, "branch"), "loop/issues-5-") {
		t.Fatalf("logs:\n%s", logs)
	}
	time.Sleep(2500 * time.Millisecond)
	if out, code := e.loopctl("logs", "2"); code == 0 {
		t.Fatalf("issue 5 ran twice:\n%s", out)
	}
	e.mustLoopctl("requeue", "issues", "5")
	e.waitState(t, "2", "done")
}
