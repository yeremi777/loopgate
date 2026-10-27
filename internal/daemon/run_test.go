package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeremi777/loopgate/internal/feed"
	"github.com/yeremi777/loopgate/internal/gittest"
	"github.com/yeremi777/loopgate/internal/job"
	"github.com/yeremi777/loopgate/internal/runner"
	"github.com/yeremi777/loopgate/internal/store"
)

func TestRunCarriesSpecJobToDraftPRAndNotifies(t *testing.T) {
	r := newRunEnv(t)
	writeFile(filepath.Join(r.checkout, "docs/specs/x.md"), "the spec")

	started, err := r.d.Run("deps", filepath.Join(r.checkout, "docs/specs/x.md"))
	if err != nil {
		t.Fatal(err)
	}
	if started.State != store.Running || !regexp.MustCompile(`^loop/deps-\d{8}-\d{6}-`+started.ID+`$`).MatchString(started.Branch) {
		t.Fatalf("started %+v", started)
	}
	if want := filepath.Join(r.checkout+"-worktrees", started.ID); started.Worktree != want {
		t.Fatalf("worktree %s, want %s", started.Worktree, want)
	}
	if got := readFile(t, filepath.Join(started.Worktree, "docs/specs/x.md")); got != "the spec" {
		t.Fatalf("spec in worktree %q", got)
	}
	r.release()
	got := r.waitDone(t, started.ID)
	if got.State != store.Done || got.PRURL != "https://github.com/me/app/pull/1" || got.Work != "spec docs/specs/x.md" {
		t.Fatalf("finished %+v", got)
	}
	if got.CostUSD <= 0 {
		t.Fatalf("cost %v not recorded", got.CostUSD)
	}
	if r.stages.prompt(0) != "/implement docs/specs/x.md" {
		t.Fatalf("first stage %q", r.stages.prompt(0))
	}
	if n := r.notes(); len(n) != 1 || !strings.Contains(n[0], "https://github.com/me/app/pull/1") {
		t.Fatalf("notifications %q", n)
	}
}

func TestRunStoresTheTestGuideOfADoneJob(t *testing.T) {
	r := newRunEnv(t)
	r.stages.try = "go run ./cmd/app, then GET /ok"
	r.release()
	started, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.waitDone(t, started.ID); got.State != store.Done || got.TestGuide != "go run ./cmd/app, then GET /ok" {
		t.Fatalf("finished %+v, want done with the test guide", got)
	}
}

func TestRunUsesTimerTaskWithoutSpec(t *testing.T) {
	r := newRunEnv(t)
	started, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	r.release()
	r.waitDone(t, started.ID)
	if got := r.stages.prompt(0); got != "/implement bump go dependencies" {
		t.Fatalf("first stage %q", got)
	}
}

func TestRunRefusesSecondJobWhileOneRuns(t *testing.T) {
	r := newRunEnv(t)
	first, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.Run("deps", ""); !errors.Is(err, ErrBusy) {
		t.Fatalf("want ErrBusy, got %v", err)
	}
	r.release()
	r.waitDone(t, first.ID)
	second, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatalf("run after the first job ended: %v", err)
	}
	r.waitDone(t, second.ID)
}

func TestRunRefusesBadWork(t *testing.T) {
	r := newRunEnv(t)
	if _, err := r.d.Run("issues", ""); !errors.Is(err, ErrNeedsSpec) {
		t.Fatalf("github loop without spec: want ErrNeedsSpec, got %v", err)
	}
	if _, err := r.d.Run("deps", "/etc/passwd"); err == nil {
		t.Fatal("spec outside the checkout accepted")
	}
	if _, err := r.d.Run("nope", ""); !errors.Is(err, ErrUnknownLoop) {
		t.Fatalf("want ErrUnknownLoop, got %v", err)
	}
	if _, err := r.d.Run("deps", ""); err != nil {
		t.Fatalf("refused runs held the slot: %v", err)
	}
}

func TestJobLogShowsProgress(t *testing.T) {
	r := newRunEnv(t)
	r.stages.failFirst = true
	started, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	r.release()
	r.waitDone(t, started.ID)
	log, err := r.d.Job(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if log.Job.State != store.Done || !strings.Contains(log.Progress, "missing ok.txt") {
		t.Fatalf("log %+v", log)
	}
}

type runEnv struct {
	d        *Daemon
	st       *store.Store
	checkout string
	stages   *scriptedStages
	issues   *fakeIssues
	mu       sync.Mutex
	notified []string
}

func newRunEnv(t *testing.T) *runEnv {
	t.Helper()
	checkout := gittest.Checkout(t, "https://github.com/me/app.git")
	home := filepath.Join(t.TempDir(), "home")
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	r := &runEnv{st: st, checkout: checkout, stages: &scriptedStages{gate: make(chan struct{})},
		issues: &fakeIssues{comments: map[int][]string{}, labels: map[int]string{}}}
	// The deps verify command fails on main until a Job adds ok.txt, so these
	// Loops are loaded as files, which loopd reads at start without a trial gate.
	for name, loop := range map[string]string{
		"deps":   "name: deps\ncheckout: " + checkout + "\ntrigger:\n  timer: { every: 1h }\ntask: bump go dependencies\nverify: test -f ok.txt || { echo missing ok.txt; exit 1; }\nbounds: { max_iterations: 3, timeout: 1m }\n",
		"issues": "name: issues\ncheckout: " + checkout + "\ntrigger:\n  github: { repo: me/app, label: agent, every: 10m }\nverify: true\nbounds: { max_iterations: 3, timeout: 1m }\n",
	} {
		writeFile(filepath.Join(home, "loops", name+".yaml"), loop)
	}
	shell := runner.Runner{Environ: os.Environ(), Cache: os.TempDir()}
	d, err := New(home, st, Deps{
		Pipeline: job.Pipeline{Stages: r.stages, Shell: shell, Publisher: fakePublisher{}},
		Issues:   r.issues,
		Notify: func(title, message string) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.notified = append(r.notified, title+": "+message)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	r.d = d
	return r
}

func (r *runEnv) release() { close(r.stages.gate) }

func (r *runEnv) notes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.notified...)
}

func (r *runEnv) waitDone(t *testing.T, id string) store.Job {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		j, err := r.st.Job(id)
		if err != nil {
			t.Fatal(err)
		}
		if j.State != store.Running && j.State != store.Queued && !r.d.isBusy() {
			return j
		}
	}
	t.Fatalf("job %s never finished", id)
	return store.Job{}
}

// scriptedStages stands in for claude: implement commits ok.txt, rubric
// passes, land returns PR text and try as its test guide. The first stage
// waits for gate to close.
type scriptedStages struct {
	gate       chan struct{}
	failFirst  bool
	blockFirst bool
	try        string
	mu         sync.Mutex
	prompts    []string
}

func (s *scriptedStages) Run(ctx context.Context, st runner.Stage) (runner.Result, error) {
	select {
	case <-s.gate:
	case <-ctx.Done():
		return runner.Result{}, ctx.Err()
	}
	s.mu.Lock()
	s.prompts = append(s.prompts, st.Prompt)
	n := len(s.prompts)
	s.mu.Unlock()
	done := runner.Result{Status: runner.StatusDone, Summary: "ok", CostUSD: 0.1}
	switch {
	case strings.HasPrefix(st.Prompt, "/drill"):
		if s.blockFirst && n == 1 {
			return runner.Result{Status: runner.StatusBlocked, Question: "which db?", Summary: "stopped"}, nil
		}
		done.Summary = "add ok.txt"
	case strings.HasPrefix(st.Prompt, "/implement"):
		if s.blockFirst && n == 1 {
			return runner.Result{Status: runner.StatusBlocked, Question: "which db?", Summary: "stopped"}, nil
		}
		if s.failFirst && n == 1 {
			return done, nil
		}
		st.Log(feed.Step{Tool: "Edit", Text: "ok.txt"})
		writeFile(filepath.Join(st.Dir, "ok.txt"), "ok")
		if _, err := runner.Git(ctx, time.Minute, st.Dir, "add", "ok.txt"); err != nil {
			return runner.Result{}, err
		}
		if _, err := runner.Git(ctx, time.Minute, st.Dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "ok"); err != nil {
			return runner.Result{}, err
		}
	case strings.HasPrefix(st.Prompt, "/land"):
		done.Summary, done.Try = "Add ok\n\nbody", s.try
	}
	return done, nil
}

func (s *scriptedStages) prompt(i int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.SplitN(s.prompts[i], "\n", 2)[0]
}

type fakePublisher struct{}

func (fakePublisher) Publish(context.Context, string, string, string, job.PR) (string, error) {
	return "https://github.com/me/app/pull/1", nil
}

func writeFile(path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		panic(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
