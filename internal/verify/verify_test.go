package verify

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeremi777/loopgate/internal/config"
	"github.com/yeremi777/loopgate/internal/runner"
)

func TestGatePassesCleanTreeWhoseCommandExitsZero(t *testing.T) {
	repo := newRepo(t)
	out, err := Gate(context.Background(), shell(), repo, config.Loop{Verify: "echo checked"}, time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Outcome{Passed: true, Head: git(t, repo, "rev-parse", "HEAD"), Output: "checked\n"}); out != want {
		t.Fatalf("got %+v, want %+v", out, want)
	}
}

func TestGateFailsWhenCommandFails(t *testing.T) {
	repo := newRepo(t)
	out, err := Gate(context.Background(), shell(), repo, config.Loop{Verify: "echo 'FAIL TestX'; exit 1"}, time.Minute, nil)
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
			setupMarker, marker := filepath.Join(t.TempDir(), "setup-ran"), filepath.Join(t.TempDir(), "ran")
			out, err := Gate(context.Background(), shell(), repo, config.Loop{Setup: "touch " + setupMarker, Verify: "touch " + marker}, time.Minute, nil)
			if err != nil {
				t.Fatal(err)
			}
			if out.Passed || !strings.Contains(out.Output, dirty) {
				t.Fatalf("got %+v, want failed naming %s", out, dirty)
			}
			for _, m := range []string{setupMarker, marker} {
				if _, err := os.Stat(m); !os.IsNotExist(err) {
					t.Fatalf("%s: a command ran on a dirty tree", filepath.Base(m))
				}
			}
		})
	}
}

func TestGateRunsSetupBeforeTheVerifyCommand(t *testing.T) {
	repo := newRepo(t)
	write(t, filepath.Join(repo, ".gitignore"), "node_modules/\n")
	git(t, repo, "add", ".gitignore")
	git(t, repo, "commit", "-q", "-m", "ignore deps")
	loop := config.Loop{Setup: "mkdir -p node_modules && echo installed", Verify: "test -d node_modules && echo checked"}
	out, err := Gate(context.Background(), shell(), repo, loop, time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Outcome{Passed: true, Head: git(t, repo, "rev-parse", "HEAD"), Output: "checked\n"}); out != want {
		t.Fatalf("got %+v, want %+v", out, want)
	}
}

func TestGateLetsSetupReachTheLoopsNetwork(t *testing.T) {
	repo := newRepo(t)
	// The proxy answers 200 when it reaches example.com and 502 when this machine is offline.
	loop := config.Loop{Setup: "curl -s -m 5 -o /dev/null -w 'connect-%{http_connect}\\n' https://example.com/; exit 1", Verify: "true", Network: []string{"example.com"}}
	out, err := Gate(context.Background(), shell(), repo, loop, time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Output, "connect-200") && !strings.Contains(out.Output, "connect-502") {
		t.Fatalf("setup did not get a tunnel to its network domain:\n%s", out.Output)
	}
}

func TestGateFailsOnSetupThatFailsOrChangesTheTree(t *testing.T) {
	cases := map[string]struct{ setup, want string }{
		"exits non-zero":    {"echo 'npm error E404'; curl -s -m 5 https://example.com/; exit 1", "example.com"},
		"changes a tracked": {"echo v2 > README; echo rewrote-readme", "rewrote-readme"},
		"leaves untracked":  {"echo x > package-lock.json; curl -s -m 5 https://example.com/", "example.com"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			repo := newRepo(t)
			marker := filepath.Join(t.TempDir(), "ran")
			out, err := Gate(context.Background(), shell(), repo, config.Loop{Setup: c.setup, Verify: "touch " + marker}, time.Minute, nil)
			if err != nil {
				t.Fatal(err)
			}
			if out.Passed || !strings.Contains(out.Output, "setup") || !strings.Contains(out.Output, c.want) {
				t.Fatalf("got %+v, want failed naming setup and %s", out, c.want)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("verify command ran after a failed setup")
			}
		})
	}
}

// A setup that swallows a refused download passes, but verify then fails
// for want of it, and the output must say which host was refused.
func TestGateNamesRefusedHostsWhenVerifyFailsAfterSetup(t *testing.T) {
	repo := newRepo(t)
	loop := config.Loop{Setup: "curl -s -m 5 https://example.com/ || true", Verify: "echo 'module not found'; exit 1"}
	out, err := Gate(context.Background(), shell(), repo, loop, time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Passed || !strings.Contains(out.Output, "module not found") || !strings.Contains(out.Output, "example.com") {
		t.Fatalf("got %+v, want failed naming the verify error and example.com", out)
	}
}

func TestGateNamesRefusedHostsWhenSetupTimesOut(t *testing.T) {
	repo := newRepo(t)
	loop := config.Loop{Setup: "curl -s -m 5 https://example.com/; sleep 60", Verify: "true"}
	_, err := Gate(context.Background(), shell(), repo, loop, 3*time.Second, nil)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "example.com") {
		t.Fatalf("got %v, want a deadline error naming example.com", err)
	}
}

func shell() runner.Runner { return runner.Runner{Environ: os.Environ(), Cache: os.TempDir()} }

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

func TestShipCheckAllowsOnlyMarkdownAfterTheGatedCommit(t *testing.T) {
	commit := func(repo string, files ...string) {
		for _, f := range files {
			write(t, filepath.Join(repo, f), "x")
		}
		git(t, repo, "add", "-A")
		git(t, repo, "commit", "-q", "-m", "c")
	}
	t.Run("unchanged", func(t *testing.T) {
		repo := newRepo(t)
		if err := ShipCheck(context.Background(), repo, git(t, repo, "rev-parse", "HEAD"), nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("markdown commits", func(t *testing.T) {
		repo := newRepo(t)
		gated := git(t, repo, "rev-parse", "HEAD")
		commit(repo, "docs/adr/0002-x.md", "CONTEXT.md")
		if err := ShipCheck(context.Background(), repo, gated, nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("code commit", func(t *testing.T) {
		repo := newRepo(t)
		gated := git(t, repo, "rev-parse", "HEAD")
		commit(repo, "CONTEXT.md", "main.go")
		if err := ShipCheck(context.Background(), repo, gated, nil); err == nil || !strings.Contains(err.Error(), "main.go") {
			t.Fatalf("want error naming main.go, got %v", err)
		}
	})
	t.Run("uncommitted", func(t *testing.T) {
		repo := newRepo(t)
		gated := git(t, repo, "rev-parse", "HEAD")
		write(t, filepath.Join(repo, "notes.md"), "x")
		if err := ShipCheck(context.Background(), repo, gated, nil); err == nil {
			t.Fatal("dirty tree passed")
		}
	})
	t.Run("history rewritten", func(t *testing.T) {
		repo := newRepo(t)
		commit(repo, "a.go")
		gated := git(t, repo, "rev-parse", "HEAD")
		git(t, repo, "reset", "-q", "--hard", "HEAD~1")
		commit(repo, "b.md")
		if err := ShipCheck(context.Background(), repo, gated, nil); err == nil {
			t.Fatal("head that dropped the gated commit passed")
		}
	})
	t.Run("code renamed to markdown", func(t *testing.T) {
		repo := newRepo(t)
		commit(repo, "main_test.go")
		gated := git(t, repo, "rev-parse", "HEAD")
		git(t, repo, "mv", "main_test.go", "main_test.md")
		git(t, repo, "commit", "-q", "-m", "rename")
		if err := ShipCheck(context.Background(), repo, gated, nil); err == nil || !strings.Contains(err.Error(), "main_test.go") {
			t.Fatalf("want error naming main_test.go, got %v", err)
		}
	})
	t.Run("protected markdown", func(t *testing.T) {
		repo := newRepo(t)
		gated := git(t, repo, "rev-parse", "HEAD")
		commit(repo, "CLAUDE.md", "docs/notes.md")
		if err := ShipCheck(context.Background(), repo, gated, []string{"docs/notes.md"}); err == nil ||
			!strings.Contains(err.Error(), "CLAUDE.md") || !strings.Contains(err.Error(), "docs/notes.md") {
			t.Fatalf("want error naming CLAUDE.md and docs/notes.md, got %v", err)
		}
	})
}
