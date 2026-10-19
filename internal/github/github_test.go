package github

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeremi777/loopgate/internal/job"
)

func TestPublishPushesBranchAndOpensDraftPR(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	wt := filepath.Join(root, "wt")
	git(t, root, "clone", "-q", origin, wt)
	git(t, wt, "checkout", "-q", "-b", "loop/fix-42")
	git(t, wt, "commit", "-q", "--allow-empty", "-m", "work")

	rec := filepath.Join(root, "rec")
	gh := filepath.Join(root, "gh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + rec + ".args\ncat > " + rec + ".body\necho 'https://github.com/me/app/pull/7'\n"
	if err := os.WriteFile(gh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	url, err := Publisher{GH: gh}.Publish(context.Background(), wt, "loop/fix-42", "main", job.PR{Title: "Fix the thing", Body: "body text"})
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://github.com/me/app/pull/7" {
		t.Fatalf("url %q", url)
	}
	if got, want := git(t, origin, "rev-parse", "refs/heads/loop/fix-42"), git(t, wt, "rev-parse", "HEAD"); got != want {
		t.Fatalf("origin branch at %q, want %q", got, want)
	}
	args := read(t, rec+".args")
	want := "pr\ncreate\n--draft\n--base\nmain\n--head\nloop/fix-42\n--title\nFix the thing\n--body-file\n-\n"
	if args != want {
		t.Fatalf("gh args %q, want %q", args, want)
	}
	if body := read(t, rec+".body"); body != "body text" {
		t.Fatalf("body %q", body)
	}
}

func TestPublishRefusesToRewriteTheRemoteBranch(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	other := filepath.Join(root, "other")
	git(t, root, "clone", "-q", origin, other)
	git(t, other, "checkout", "-q", "-b", "loop/fix-42")
	git(t, other, "commit", "-q", "--allow-empty", "-m", "someone else")
	git(t, other, "push", "-q", "origin", "loop/fix-42")
	wt := filepath.Join(root, "wt")
	git(t, root, "clone", "-q", origin, wt)
	git(t, wt, "checkout", "-q", "--orphan", "loop/fix-42")
	git(t, wt, "commit", "-q", "--allow-empty", "-m", "unrelated")

	gh := filepath.Join(root, "gh")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\necho should-not-run; exit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := (Publisher{GH: gh}).Publish(context.Background(), wt, "loop/fix-42", "main", job.PR{Title: "t"}); err == nil {
		t.Fatal("publish rewrote a remote branch it did not own")
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
