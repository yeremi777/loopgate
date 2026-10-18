package daemon

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestTakeoverRefusesDoneJob(t *testing.T) {
	r := newRunEnv(t)
	r.release()
	started, _ := r.d.Run("deps", "")
	r.waitDone(t, started.ID)
	if _, err := r.d.Takeover(started.ID); !errors.Is(err, ErrNotTakeable) {
		t.Fatalf("want ErrNotTakeable, got %v", err)
	}
}

func TestHandbackRunsTheLoopAgainOnTheHumansCommits(t *testing.T) {
	r := newRunEnv(t)
	started, _ := r.d.Run("deps", "")
	if _, err := r.d.Takeover(started.ID); err != nil {
		t.Fatal(err)
	}
	writeFile(filepath.Join(started.Worktree, "human.txt"), "by hand")
	gitRun(t, started.Worktree, "add", "human.txt")
	gitRun(t, started.Worktree, "commit", "-q", "-m", "human work")

	if _, err := r.d.Handback(started.ID + 100); !errors.Is(err, store.ErrNotFound) {
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
