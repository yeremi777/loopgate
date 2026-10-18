package verify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeremi777/loopgate/internal/runner"
)

func TestGatePassesCleanTreeWhoseCommandExitsZero(t *testing.T) {
	repo := newRepo(t)
	out, err := Gate(context.Background(), shell(), repo, "echo checked", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Outcome{Passed: true, Head: git(t, repo, "rev-parse", "HEAD"), Output: "checked\n"}); out != want {
		t.Fatalf("got %+v, want %+v", out, want)
	}
}

func TestGateFailsWhenCommandFails(t *testing.T) {
	repo := newRepo(t)
	out, err := Gate(context.Background(), shell(), repo, "echo 'FAIL TestX'; exit 1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if out.Passed || !strings.Contains(out.Output, "FAIL TestX") {
		t.Fatalf("got %+v, want failed with command output", out)
	}
}

func TestGateRefusesUncommittedWorkWithoutRunningCommand(t *testing.T) {
	for name, dirty := range map[string]string{"modified": "README", "untracked": "new.go"} {
		t.Run(name, func(t *testing.T) {
			repo := newRepo(t)
			write(t, filepath.Join(repo, dirty), "uncommitted")
			marker := filepath.Join(t.TempDir(), "ran")
			out, err := Gate(context.Background(), shell(), repo, "touch "+marker, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if out.Passed || !strings.Contains(out.Output, dirty) {
				t.Fatalf("got %+v, want failed naming %s", out, dirty)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("verify command ran on a dirty tree")
			}
		})
	}
}

func shell() runner.Runner { return runner.Runner{Environ: os.Environ()} }

func newRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	write(t, filepath.Join(repo, "README"), "v1")
	git(t, repo, "add", "README")
	git(t, repo, "commit", "-q", "-m", "v1")
	return repo
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

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
