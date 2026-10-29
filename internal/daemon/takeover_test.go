package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeremi777/loopgate/internal/gittest"
	"github.com/yeremi777/loopgate/internal/job"
	"github.com/yeremi777/loopgate/internal/runner"
	"github.com/yeremi777/loopgate/internal/store"
)

func TestTakeoverOfRunningJobStopsItAndFreesTheSlot(t *testing.T) {
	r := newRunEnv(t)
	started, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	taken, err := r.d.Takeover(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if taken.State != store.Manual || taken.Worktree != started.Worktree || taken.Branch != started.Branch {
		t.Fatalf("taken %+v", taken)
	}
	if r.d.isBusy() {
		t.Fatal("a manual job still holds the slot")
	}
	if got, _ := r.st.Job(started.ID); got.State != store.Manual {
		t.Fatalf("stored state %s, want manual", got.State)
	}
	if n := r.notes(); len(n) != 0 {
		t.Fatalf("takeover notified: %q", n)
	}
}

func TestTakeoverOfBlockedJob(t *testing.T) {
	r := newRunEnv(t)
	r.stages.blockFirst = true
	r.release()
	started, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.waitDone(t, started.ID); got.State != store.Blocked {
		t.Fatalf("state %s, want blocked", got.State)
	}
	taken, err := r.d.Takeover(started.ID)
	if err != nil || taken.State != store.Manual {
		t.Fatalf("taken %+v, %v", taken, err)
	}
}

func TestTakeoverOfAJobWithoutAWorktreeNamesRm(t *testing.T) {
	r := newRunEnv(t)
	j, err := r.st.CreateJob("deps")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.st.SetJobState(j.ID, store.Failed); err != nil {
		t.Fatal(err)
	}
	want := "job " + j.ID + " never got a worktree: this job cannot be taken over; to remove it, run `loopctl rm " + j.ID + "`"
	if _, err := r.d.Takeover(j.ID); !errors.Is(err, ErrNotTakeable) || err.Error() != want {
		t.Fatalf("got %v, want ErrNotTakeable as %q", err, want)
	}
}

func TestTakeoverDoneNamesTry(t *testing.T) {
	r := newRunEnv(t)
	r.release()
	started, _ := r.d.Run("deps", "")
	r.waitDone(t, started.ID)
	want := "job " + started.ID + " is done: this job cannot be taken over; to run it by hand, run `loopctl try " + started.ID + "`"
	if _, err := r.d.Takeover(started.ID); !errors.Is(err, ErrNotTakeable) || err.Error() != want {
		t.Fatalf("got %v, want ErrNotTakeable as %q", err, want)
	}
}

func TestHandbackRunsTheLoopAgainOnTheHumansCommits(t *testing.T) {
	r := newRunEnv(t)
	started, _ := r.d.Run("deps", "")
	if _, err := r.d.Takeover(started.ID); err != nil {
		t.Fatal(err)
	}
	writeFile(filepath.Join(started.Worktree, "human.txt"), "by hand")
	gittest.Run(t, started.Worktree, "add", "human.txt")
	gittest.Run(t, started.Worktree, "commit", "-q", "-m", "human work")

	if _, err := r.d.Handback("999999999999"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("handback of unknown job: %v", err)
	}
	r.release()
	back, err := r.d.Handback(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if back.State != store.Running {
		t.Fatalf("handed back %+v", back)
	}
	if got := r.waitDone(t, started.ID); got.State != store.Done {
		t.Fatalf("after handback %+v", got)
	}
	log := runGit(t, started.Worktree, "log", "--format=%s")
	if !strings.Contains(log, "human work") || !strings.Contains(log, "ok") {
		t.Fatalf("branch lost the human's or the loop's commit:\n%s", log)
	}
	if _, err := r.d.Handback(started.ID); !errors.Is(err, ErrNotManual) {
		t.Fatalf("handback of a done job: %v", err)
	}
}

func TestHandbackClearsTheEarlierOutcome(t *testing.T) {
	r := newRunEnv(t)
	started, _ := r.d.Run("deps", "")
	if _, err := r.d.Takeover(started.ID); err != nil {
		t.Fatal(err)
	}
	earlier := store.Finish{State: store.Manual, Question: "which db?", Summary: "stopped", Error: "stage failed", EndCommit: "abc", TestGuide: "run it", CostUSD: 1.5}
	if err := r.st.FinishJob(started.ID, earlier); err != nil {
		t.Fatal(err)
	}
	cost := r.waitDone(t, started.ID).CostUSD
	back, err := r.d.Handback(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if back.Question != "" || back.Summary != "" || back.Error != "" || back.EndCommit != "" || back.TestGuide != "" || !back.Ended.IsZero() {
		t.Fatalf("handed back with the earlier outcome: %+v", back)
	}
	if back.CostUSD != cost {
		t.Fatalf("cost %v, want %v kept", back.CostUSD, cost)
	}
	r.release()
	r.waitDone(t, started.ID)
}

func TestHandbackWaitsForTheSlot(t *testing.T) {
	r := newRunEnv(t)
	first, _ := r.d.Run("deps", "")
	if _, err := r.d.Takeover(first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.Run("deps", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.Handback(first.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("want ErrBusy, got %v", err)
	}
}

func TestStopEndsTheLoopsRunningJob(t *testing.T) {
	r := newRunEnv(t)
	if err := r.d.Stop("deps"); !errors.Is(err, ErrNothingRunning) {
		t.Fatalf("stop with nothing running: %v", err)
	}
	started, _ := r.d.Run("deps", "")
	if err := r.d.Stop("issues"); !errors.Is(err, ErrNothingRunning) {
		t.Fatalf("stop of another loop: %v", err)
	}
	if err := r.d.Stop("deps"); err != nil {
		t.Fatal(err)
	}
	got := r.waitDone(t, started.ID)
	if got.State != store.Failed || !strings.Contains(got.Error, "stopped") {
		t.Fatalf("stopped job %+v", got)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runner.Git(t.Context(), time.Minute, dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestHandbackOfAnIssueJobStillAnswersTheIssue(t *testing.T) {
	r := newRunEnv(t)
	r.stages.blockFirst = true
	r.issues.open = []job.Issue{{Number: 6, Title: "six"}}
	r.release()
	r.d.Tick(t0)
	r.d.Tick(t0.Add(10 * time.Minute))
	if j := r.waitDone(t, r.nth(t, 1)); j.State != store.Blocked || j.Issue != 6 {
		t.Fatalf("job %+v", j)
	}
	if _, err := r.d.Takeover(r.nth(t, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.Handback(r.nth(t, 1)); err != nil {
		t.Fatal(err)
	}
	if j := r.waitDone(t, r.nth(t, 1)); j.State != store.Done {
		t.Fatalf("after handback %+v", j)
	}
	said := r.issues.said(6)
	if len(said) != 2 || !strings.Contains(said[1], "https://github.com/me/app/pull/1") {
		t.Fatalf("issue 6 comments %q", said)
	}
}

func TestOldJobDirKeepsItsIntegerNameForLogsTakeoverAndHandback(t *testing.T) {
	r := newRunEnv(t)
	r.stages.failFirst = true
	dir := filepath.Join(r.d.home, "jobs", "1")
	wt := filepath.Join(dir, "worktree")
	made, err := runner.CreateWorktree(t.Context(), runner.Worktree{Checkout: r.checkout, Path: wt, Branch: "loop/deps-20260101-000000-1"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(filepath.Join(dir, "progress.md"), "## iteration 1\nfrom before the upgrade\n")
	old, err := r.st.CreateJob("deps")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.st.StartJob(old.ID, store.Start{Work: "task bump go dependencies", Branch: "loop/deps-20260101-000000-1", Worktree: wt, Base: made.Base}); err != nil {
		t.Fatal(err)
	}
	if err := r.st.FinishJob(old.ID, store.Finish{State: store.Blocked, Question: "which db?"}); err != nil {
		t.Fatal(err)
	}

	log, err := r.d.Job(old.ID)
	if err != nil || !strings.Contains(log.Progress, "from before the upgrade") {
		t.Fatalf("log of an old job: %+v, %v", log, err)
	}
	if _, err := r.d.Takeover(old.ID); err != nil {
		t.Fatal(err)
	}
	r.release()
	if _, err := r.d.Handback(old.ID); err != nil {
		t.Fatal(err)
	}
	if got := r.waitDone(t, old.ID); got.State != store.Done {
		t.Fatalf("old job after handback %+v", got)
	}
	if progress := readFile(t, filepath.Join(dir, "progress.md")); !strings.Contains(progress, "from before the upgrade") || !strings.Contains(progress, "missing ok.txt") {
		t.Fatalf("progress of the old job:\n%s", progress)
	}
	if _, err := os.Stat(r.d.jobDir(old.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("handback made a second directory for the old job: %v", err)
	}
}

func TestEndCommitIsWhereTheBranchPointsWhenTheJobEnds(t *testing.T) {
	r := newRunEnv(t)
	r.release()
	shipped, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	done := r.waitDone(t, shipped.ID)
	if head := runGit(t, r.checkout, "rev-parse", "refs/heads/"+done.Branch); done.State != store.Done || done.EndCommit != head || head == done.Base {
		t.Fatalf("done job %+v; want end commit %s past the base", done, head)
	}

	r.stages.blockFirst = true
	r.stages.prompts = nil
	blocked, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	got := r.waitDone(t, blocked.ID)
	if got.State != store.Blocked || got.EndCommit != got.Base {
		t.Fatalf("blocked job %+v; want end commit at its base", got)
	}
}

func TestTakeoverRefusesAJobWithoutAWorktree(t *testing.T) {
	r := newRunEnv(t)
	created, err := r.st.CreateJob("deps")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.st.FinishJob(created.ID, store.Finish{State: store.Failed, Error: "fetch failed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.Takeover(created.ID); !errors.Is(err, ErrNotTakeable) {
		t.Fatalf("want ErrNotTakeable, got %v", err)
	}
}

func TestLateStopKeepsAPublishedJobDone(t *testing.T) {
	r := newRunEnv(t)
	pub := &gatedPublisher{published: make(chan struct{}), resume: make(chan struct{})}
	r.d.deps.Pipeline.Publisher = pub
	r.release()
	started, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	<-pub.published
	if err := r.d.Stop("deps"); err != nil {
		t.Fatal(err)
	}
	close(pub.resume)
	if j := r.waitDone(t, started.ID); j.State != store.Done || j.PRURL != "https://github.com/me/app/pull/1" {
		t.Fatalf("published job %+v", j)
	}
}

// gatedPublisher opens the PR, then holds Publish until resume closes.
type gatedPublisher struct{ published, resume chan struct{} }

func (g *gatedPublisher) Publish(context.Context, string, string, string, job.PR) (string, error) {
	close(g.published)
	<-g.resume
	return "https://github.com/me/app/pull/1", nil
}
