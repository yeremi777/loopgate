package job

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
	"github.com/yeremi777/loopgate/internal/runner"
	"github.com/yeremi777/loopgate/internal/store"
	"github.com/yeremi777/loopgate/internal/verify"
)

func TestHappyPathEndsReadyToShipAtGatedCommit(t *testing.T) {
	j := newJob(t, 3)
	f := &fakeStages{t: t, steps: []step{
		commits(j, "ok.txt"),
		reports(runner.StatusDone, "no act-on findings"),
	}}
	out := Pipeline{Stages: f, Shell: shell()}.Run(context.Background(), j)
	if out.State != store.Done || out.Err != nil {
		t.Fatalf("got %+v, want done", out)
	}
	if want := git(t, j.Worktree, "rev-parse", "HEAD"); out.Gated != want {
		t.Fatalf("gated %q, want head %q", out.Gated, want)
	}
	f.wantKinds("/implement", "/rubric")
	first := f.stages[0]
	if !strings.HasPrefix(first.Prompt, "/implement docs/specs/x.md") || first.Dir != j.Worktree ||
		!reflect.DeepEqual(first.Network, []string{"proxy.golang.org"}) {
		t.Fatalf("implement stage %+v", first)
	}
	if !strings.Contains(f.stages[1].Prompt, "origin/main") {
		t.Fatalf("rubric prompt does not name the diff base: %q", f.stages[1].Prompt)
	}
}

func TestFailedGateFeedsItsOutputToTheNextImplementRun(t *testing.T) {
	j := newJob(t, 3)
	f := &fakeStages{t: t, steps: []step{
		reports(runner.StatusDone, "done, trust me"),
		commits(j, "ok.txt"),
		reports(runner.StatusDone, "clean"),
	}}
	out := Pipeline{Stages: f, Shell: shell()}.Run(context.Background(), j)
	if out.State != store.Done {
		t.Fatalf("got %+v, want done", out)
	}
	f.wantKinds("/implement", "/implement", "/rubric")
	if !strings.Contains(f.stages[1].Prompt, "missing ok.txt") {
		t.Fatalf("second implement prompt lacks gate output: %q", f.stages[1].Prompt)
	}
	if strings.Contains(f.stages[0].Prompt, "missing ok.txt") {
		t.Fatal("first implement prompt already has gate output")
	}
	progress, err := os.ReadFile(filepath.Join(j.Dir, "progress.md"))
	if err != nil || !strings.Contains(string(progress), "missing ok.txt") {
		t.Fatalf("progress.md %q, %v", progress, err)
	}
}

func TestUncommittedWorkFailsTheGate(t *testing.T) {
	j := newJob(t, 2)
	f := &fakeStages{t: t, steps: []step{
		func(runner.Stage) (runner.Result, error) {
			writeFile(t, filepath.Join(j.Worktree, "ok.txt"), "ok")
			return runner.Result{Status: runner.StatusDone, Summary: "wrote it"}, nil
		},
		func(runner.Stage) (runner.Result, error) {
			git(t, j.Worktree, "add", "ok.txt")
			git(t, j.Worktree, "commit", "-q", "-m", "ok")
			return runner.Result{Status: runner.StatusDone, Summary: "committed"}, nil
		},
		reports(runner.StatusDone, "clean"),
	}}
	out := Pipeline{Stages: f, Shell: shell()}.Run(context.Background(), j)
	if out.State != store.Done {
		t.Fatalf("got %+v, want done", out)
	}
	if !strings.Contains(f.stages[1].Prompt, "uncommitted") {
		t.Fatalf("second prompt lacks the uncommitted report: %q", f.stages[1].Prompt)
	}
}

func TestExhaustedAfterMaxIterations(t *testing.T) {
	j := newJob(t, 2)
	f := &fakeStages{t: t, steps: []step{
		reports(runner.StatusDone, "try 1"),
		reports(runner.StatusDone, "try 2"),
	}}
	out := Pipeline{Stages: f, Shell: shell()}.Run(context.Background(), j)
	if out.State != store.Exhausted {
		t.Fatalf("got %+v, want exhausted", out)
	}
	f.wantKinds("/implement", "/implement")
}

func TestExhaustedOnTimeout(t *testing.T) {
	j := newJob(t, 5)
	j.Loop.Bounds.Timeout = 300 * time.Millisecond
	f := &fakeStages{t: t, steps: []step{
		func(s runner.Stage) (runner.Result, error) {
			if s.Timeout > 300*time.Millisecond || s.Timeout <= 0 {
				t.Errorf("stage timeout %v exceeds the job's remaining time", s.Timeout)
			}
			time.Sleep(s.Timeout)
			return runner.Result{}, context.DeadlineExceeded
		},
	}}
	out := Pipeline{Stages: f, Shell: shell()}.Run(context.Background(), j)
	if out.State != store.Exhausted {
		t.Fatalf("got %+v, want exhausted", out)
	}
}

func TestBlockedStageEndsTheJob(t *testing.T) {
	j := newJob(t, 3)
	f := &fakeStages{t: t, steps: []step{
		func(runner.Stage) (runner.Result, error) {
			return runner.Result{Status: runner.StatusBlocked, Question: "which database?", Summary: "stopped"}, nil
		},
	}}
	out := Pipeline{Stages: f, Shell: shell()}.Run(context.Background(), j)
	if out.State != store.Blocked || out.Question != "which database?" {
		t.Fatalf("got %+v, want blocked with question", out)
	}
	f.wantKinds("/implement")
}

func TestProtectedPathBlocksBeforeRubric(t *testing.T) {
	j := newJob(t, 3)
	f := &fakeStages{t: t, steps: []step{commits(j, "ok.txt", ".github/workflows/ci.yml")}}
	out := Pipeline{Stages: f, Shell: shell()}.Run(context.Background(), j)
	if out.State != store.Blocked || !strings.Contains(out.Question, ".github/workflows/ci.yml") {
		t.Fatalf("got %+v, want blocked naming the protected path", out)
	}
	f.wantKinds("/implement")
}

func TestRubricRevisionGoesBackToImplement(t *testing.T) {
	j := newJob(t, 2)
	f := &fakeStages{t: t, steps: []step{
		commits(j, "ok.txt"),
		reports(runner.StatusRevise, "nil map write at store.go:40"),
		commits(j, "fix.txt"),
		reports(runner.StatusDone, "clean"),
	}}
	out := Pipeline{Stages: f, Shell: shell()}.Run(context.Background(), j)
	if out.State != store.Done {
		t.Fatalf("got %+v, want done", out)
	}
	f.wantKinds("/implement", "/rubric", "/implement", "/rubric")
	if !strings.Contains(f.stages[2].Prompt, "nil map write at store.go:40") {
		t.Fatalf("second implement prompt lacks rubric findings: %q", f.stages[2].Prompt)
	}
}

func TestRubricRevisionCountsTowardIterations(t *testing.T) {
	j := newJob(t, 1)
	f := &fakeStages{t: t, steps: []step{
		commits(j, "ok.txt"),
		reports(runner.StatusRevise, "nil map write"),
	}}
	out := Pipeline{Stages: f, Shell: shell()}.Run(context.Background(), j)
	if out.State != store.Exhausted {
		t.Fatalf("got %+v, want exhausted", out)
	}
}

func TestRubricThatEditsTheTreeFailsTheJob(t *testing.T) {
	j := newJob(t, 3)
	f := &fakeStages{t: t, steps: []step{
		commits(j, "ok.txt"),
		func(runner.Stage) (runner.Result, error) {
			writeFile(t, filepath.Join(j.Worktree, "ok.txt"), "edited by reviewer")
			return runner.Result{Status: runner.StatusDone, Summary: "fixed it myself"}, nil
		},
	}}
	out := Pipeline{Stages: f, Shell: shell()}.Run(context.Background(), j)
	if out.State != store.Failed || out.Err == nil {
		t.Fatalf("got %+v, want failed", out)
	}
}

func TestOutcomeReportsChangedTests(t *testing.T) {
	j := newJob(t, 3)
	f := &fakeStages{t: t, steps: []step{
		commits(j, "ok.txt", "a_test.go"),
		reports(runner.StatusDone, "clean"),
	}}
	out := Pipeline{Stages: f, Shell: shell()}.Run(context.Background(), j)
	if want := []verify.Change{{Path: "a_test.go", Kind: verify.Modified}}; !reflect.DeepEqual(out.TestChanges, want) {
		t.Fatalf("test changes %+v, want %+v", out.TestChanges, want)
	}
}

func TestStageErrorFailsTheJob(t *testing.T) {
	j := newJob(t, 3)
	f := &fakeStages{t: t, steps: []step{
		func(runner.Stage) (runner.Result, error) { return runner.Result{}, errors.New("claude crashed") },
	}}
	out := Pipeline{Stages: f, Shell: shell()}.Run(context.Background(), j)
	if out.State != store.Failed || out.Err == nil {
		t.Fatalf("got %+v, want failed", out)
	}
}

type step func(runner.Stage) (runner.Result, error)

type fakeStages struct {
	t      *testing.T
	steps  []step
	stages []runner.Stage
}

func (f *fakeStages) Run(_ context.Context, s runner.Stage) (runner.Result, error) {
	f.stages = append(f.stages, s)
	if len(f.stages) > len(f.steps) {
		f.t.Fatalf("unexpected stage %d: %q", len(f.stages), s.Prompt)
	}
	return f.steps[len(f.stages)-1](s)
}

func (f *fakeStages) wantKinds(kinds ...string) {
	f.t.Helper()
	var got []string
	for _, s := range f.stages {
		got = append(got, strings.Fields(s.Prompt)[0])
	}
	if !reflect.DeepEqual(got, kinds) {
		f.t.Fatalf("stages %q, want %q", got, kinds)
	}
}

func reports(status runner.Status, summary string) step {
	return func(runner.Stage) (runner.Result, error) {
		return runner.Result{Status: status, Summary: summary}, nil
	}
}

func commits(j Job, files ...string) step {
	return func(runner.Stage) (runner.Result, error) {
		for _, f := range files {
			path := filepath.Join(j.Worktree, f)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return runner.Result{}, err
			}
			if err := os.WriteFile(path, []byte("work"), 0o644); err != nil {
				return runner.Result{}, err
			}
		}
		for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "work"}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = j.Worktree
			if out, err := cmd.CombinedOutput(); err != nil {
				return runner.Result{}, errors.New(string(out))
			}
		}
		return runner.Result{Status: runner.StatusDone, Summary: "committed"}, nil
	}
}

// newJob returns a Job on branch loop/x-1 off origin/main whose verify command
// passes only once ok.txt is committed.
func newJob(t *testing.T, maxIterations int) Job {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	wt := filepath.Join(root, "wt")
	git(t, root, "clone", "-q", origin, wt)
	git(t, wt, "checkout", "-q", "-b", "main")
	writeFile(t, filepath.Join(wt, "a_test.go"), "base")
	git(t, wt, "add", "a_test.go")
	git(t, wt, "commit", "-q", "-m", "base")
	git(t, wt, "push", "-q", "origin", "main")
	git(t, wt, "checkout", "-q", "-b", "loop/x-1")
	dir := filepath.Join(root, "job")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return Job{
		ID: 1,
		Loop: config.Loop{
			Name:     "x",
			Verify:   `test -f ok.txt || { echo "missing ok.txt"; exit 1; }`,
			Bounds:   config.Bounds{MaxIterations: maxIterations, Timeout: time.Minute},
			Tests:    []string{"**/*_test.go"},
			Network:  []string{"proxy.golang.org"},
			Checkout: wt,
		},
		Work:          Spec{Path: "docs/specs/x.md"},
		Dir:           dir,
		Worktree:      wt,
		DefaultBranch: "main",
	}
}

func shell() runner.Runner { return runner.Runner{Environ: os.Environ()} }

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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
