package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yeremi777/loopgate/internal/config"
	"github.com/yeremi777/loopgate/internal/gittest"
	"github.com/yeremi777/loopgate/internal/job"
	"github.com/yeremi777/loopgate/internal/runner"
	"github.com/yeremi777/loopgate/internal/store"
)

func fixLoop(checkout string) string {
	return "name: fix\ncheckout: " + checkout + "\ntrigger:\n  github: { repo: me/app, label: agent, every: 10m }\nverify: \"true\"\nbounds: { max_iterations: 5, timeout: 20m }\n"
}

func TestAddedLoopsSurviveRestart(t *testing.T) {
	home := t.TempDir()
	d := newDaemon(t, home)
	checkout := gittest.Checkout(t, "https://github.com/me/app.git")
	if _, err := d.Add(context.Background(), []byte(fixLoop(checkout))); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Add(context.Background(), []byte(strings.Replace(fixLoop(checkout), "name: fix", "name: docs", 1))); err != nil {
		t.Fatal(err)
	}
	if err := d.SetPaused("docs", true); err != nil {
		t.Fatal(err)
	}

	d = newDaemon(t, home)
	got, err := d.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []LoopStatus{
		{Name: "docs", Paused: true, Trigger: "github me/app", Checkout: checkout},
		{Name: "fix", Paused: false, Trigger: "github me/app", Checkout: checkout},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestAddRejectsInvalidAndDuplicateLoops(t *testing.T) {
	d := newDaemon(t, t.TempDir())
	checkout := gittest.Checkout(t, "https://github.com/me/app.git")
	var fe *config.FieldError
	if _, err := d.Add(context.Background(), []byte("name: fix\n")); !errors.As(err, &fe) {
		t.Fatalf("want FieldError, got %v", err)
	}
	if _, err := d.Add(context.Background(), []byte(fixLoop(checkout))); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Add(context.Background(), []byte(fixLoop(checkout))); !errors.Is(err, ErrLoopExists) {
		t.Fatalf("want ErrLoopExists, got %v", err)
	}
	if got, _ := d.List(); len(got) != 1 {
		t.Fatalf("want one loop, got %+v", got)
	}
}

func TestRemovedLoopIsGoneAfterRestart(t *testing.T) {
	home := t.TempDir()
	d := newDaemon(t, home)
	checkout := gittest.Checkout(t, "https://github.com/me/app.git")
	if _, err := d.Add(context.Background(), []byte(fixLoop(checkout))); err != nil {
		t.Fatal(err)
	}
	if err := d.SetPaused("fix", true); err != nil {
		t.Fatal(err)
	}
	if err := d.Remove("fix"); err != nil {
		t.Fatal(err)
	}
	d = newDaemon(t, home)
	if got, _ := d.List(); len(got) != 0 {
		t.Fatalf("want no loops, got %+v", got)
	}

	if _, err := d.Add(context.Background(), []byte(fixLoop(checkout))); err != nil {
		t.Fatal(err)
	}
	got, _ := d.List()
	if want := []LoopStatus{{Name: "fix", Trigger: "github me/app", Checkout: checkout}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("re-added loop: got %+v, want %+v", got, want)
	}
}

func TestAddRefusesACheckoutThatJobsCannotUse(t *testing.T) {
	d := newDaemon(t, t.TempDir())
	checkout := gittest.Checkout(t, "https://github.com/me/app.git")
	noHead := gittest.Checkout(t, "https://github.com/me/app.git")
	gitRun(t, noHead, "remote", "set-head", "origin", "--delete")
	sub := filepath.Join(checkout, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, loop, want string
	}{
		{"not a clone", fixLoop(t.TempDir()), "is not a git work tree"},
		{"missing", fixLoop(filepath.Join(t.TempDir(), "gone")), "is not a git work tree"},
		{"a subdirectory", fixLoop(sub), "not the top level of a git work tree"},
		{"no origin HEAD", fixLoop(noHead), "git -C " + noHead + " remote set-head origin --auto"},
		{"another repo", strings.Replace(fixLoop(checkout), "me/app", "me/other", 1), "me/other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := d.Add(context.Background(), []byte(tc.loop))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want it to name %q", err, tc.want)
			}
			if got, _ := d.List(); len(got) != 0 {
				t.Fatalf("loop added anyway: %+v", got)
			}
		})
	}
}

func TestAddTakesACheckoutPathInAnotherLetterCase(t *testing.T) {
	checkout := gittest.Checkout(t, "https://github.com/me/app.git")
	upper := strings.ToUpper(checkout)
	if _, err := os.Stat(upper); err != nil {
		t.Skip("case-sensitive file system")
	}
	if _, err := newDaemon(t, t.TempDir()).Add(context.Background(), []byte(fixLoop(upper))); err != nil {
		t.Fatal(err)
	}
}

func TestAddMatchesTheGitHubRepoInEveryRemoteURLForm(t *testing.T) {
	for _, url := range []string{
		"https://github.com/me/app.git",
		"https://github.com/me/app",
		"git@github.com:me/app.git",
		"ssh://git@github.com/Me/App.git",
	} {
		t.Run(url, func(t *testing.T) {
			d := newDaemon(t, t.TempDir())
			if _, err := d.Add(context.Background(), []byte(fixLoop(gittest.Checkout(t, url)))); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAddPassesTheTrialGateOnOriginsDefaultBranchAfterAFetch(t *testing.T) {
	d := newDaemon(t, t.TempDir())
	checkout := gittest.Checkout(t, "https://github.com/me/app.git")
	writeFile(filepath.Join(checkout, "ok.txt"), "ok\n")
	gittest.Run(t, checkout, "add", "ok.txt")
	gittest.Run(t, checkout, "commit", "-q", "-m", "ok")
	gittest.Run(t, checkout, "push", "-q", "origin", "HEAD:main")
	gittest.Run(t, checkout, "reset", "-q", "--hard", "HEAD~1")
	gittest.Run(t, checkout, "update-ref", "refs/remotes/origin/main", "HEAD")

	loop := strings.Replace(fixLoop(checkout), `verify: "true"`, "verify: test -f ok.txt", 1)
	if _, err := d.Add(context.Background(), []byte(loop)); err != nil {
		t.Fatal(err)
	}
	if got := worktrees(t, checkout); got != 1 {
		t.Fatalf("%d worktrees after the trial gate, want only the checkout", got)
	}
}

func TestAddRefusesALoopWhoseTrialGateFails(t *testing.T) {
	d := newDaemon(t, t.TempDir())
	checkout := gittest.Checkout(t, "https://github.com/me/app.git")
	for _, tc := range []struct{ name, verify, timeout, want string }{
		{"red", "echo main is red; exit 1", "1m", "main is red"},
		{"slow", "sleep 30", "1s", "past the loop's timeout of 1s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loop := strings.Replace(fixLoop(checkout), `verify: "true"`, "verify: "+tc.verify, 1)
			loop = strings.Replace(loop, "timeout: 20m", "timeout: "+tc.timeout, 1)
			start := time.Now()
			_, err := d.Add(context.Background(), []byte(loop))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want it to name %q", err, tc.want)
			}
			if took := time.Since(start); took > 15*time.Second {
				t.Fatalf("trial gate took %s", took)
			}
			if got, _ := d.List(); len(got) != 0 {
				t.Fatalf("loop added anyway: %+v", got)
			}
			if got := worktrees(t, checkout); got != 1 {
				t.Fatalf("%d worktrees after the trial gate, want only the checkout", got)
			}
		})
	}
}

func TestAddNamesTheRefusedHostsWhenSetupRunsPastTheTimeout(t *testing.T) {
	d := newDaemon(t, t.TempDir())
	checkout := gittest.Checkout(t, "https://github.com/me/app.git")
	loop := strings.Replace(fixLoop(checkout), "timeout: 20m", "timeout: 3s", 1) +
		"setup: while true; do curl -s https://refused.example/ >/dev/null; sleep 0.2; done\n"
	_, err := d.Add(context.Background(), []byte(loop))
	if err == nil || !strings.Contains(err.Error(), "refused.example") {
		t.Fatalf("err %v, want it to name refused.example", err)
	}
}

func TestAddStopsTheTrialGateWhenTheCallerGoesAway(t *testing.T) {
	d := newDaemon(t, t.TempDir())
	checkout := gittest.Checkout(t, "https://github.com/me/app.git")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := d.Add(ctx, []byte(strings.Replace(fixLoop(checkout), `verify: "true"`, "verify: sleep 30", 1)))
		done <- err
	}()
	waitWorktrees(t, checkout, 2)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("add succeeded after its caller went away")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("trial gate kept running after its caller went away")
	}
	if got, _ := d.List(); len(got) != 0 {
		t.Fatalf("loop added anyway: %+v", got)
	}
	if got := worktrees(t, checkout); got != 1 {
		t.Fatalf("%d worktrees after the trial gate, want only the checkout", got)
	}
}

func TestCloseWaitsForATrialGateAndRemovesItsWorktree(t *testing.T) {
	d := newDaemon(t, t.TempDir())
	checkout := gittest.Checkout(t, "https://github.com/me/app.git")
	done := make(chan error, 1)
	go func() {
		_, err := d.Add(context.Background(), []byte(strings.Replace(fixLoop(checkout), `verify: "true"`, "verify: sleep 30", 1)))
		done <- err
	}()
	waitWorktrees(t, checkout, 2)
	d.Close()
	if got := worktrees(t, checkout); got != 1 {
		t.Fatalf("%d worktrees after Close, want only the checkout", got)
	}
	if err := <-done; err == nil {
		t.Fatal("add succeeded while loopd closed")
	}
}

func waitWorktrees(t *testing.T, checkout string, n int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if worktrees(t, checkout) == n {
			return
		}
	}
	t.Fatalf("never saw %d worktrees", n)
}

func worktrees(t *testing.T, checkout string) int {
	t.Helper()
	out, err := exec.Command("git", "-C", checkout, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(out), "worktree ")
}

func TestUnknownLoop(t *testing.T) {
	d := newDaemon(t, t.TempDir())
	if err := d.Remove("nope"); !errors.Is(err, ErrUnknownLoop) {
		t.Fatalf("remove: want ErrUnknownLoop, got %v", err)
	}
	if err := d.SetPaused("nope", true); !errors.Is(err, ErrUnknownLoop) {
		t.Fatalf("pause: want ErrUnknownLoop, got %v", err)
	}
}

func newDaemon(t *testing.T, home string) *Daemon {
	t.Helper()
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	d, err := New(home, st, Deps{Pipeline: job.Pipeline{Shell: runner.Runner{Environ: os.Environ(), Cache: os.TempDir()}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d
}
