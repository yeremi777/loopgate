package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestStateSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	st := open(t, path)
	if err := st.SetPaused("fix", true); err != nil {
		t.Fatal(err)
	}
	job, err := st.CreateJob("fix")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetJobState(job.ID, Blocked); err != nil {
		t.Fatal(err)
	}
	if fresh, err := st.ClaimIssue("fix", 42); err != nil || !fresh {
		t.Fatalf("first claim: fresh=%v err=%v", fresh, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st = open(t, path)
	if paused, err := st.Paused("fix"); err != nil || !paused {
		t.Fatalf("paused=%v err=%v, want true", paused, err)
	}
	got, err := st.Job(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Job{ID: job.ID, Loop: "fix", State: Blocked}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if fresh, err := st.ClaimIssue("fix", 42); err != nil || fresh {
		t.Fatalf("claim after reopen: fresh=%v err=%v, want already claimed", fresh, err)
	}
}

func TestNewJobIsQueuedAndIssuesAreClaimedPerLoop(t *testing.T) {
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	job, err := st.CreateJob("fix")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != Queued || job.Loop != "fix" {
		t.Fatalf("got %+v, want queued job of fix", job)
	}
	if fresh, err := st.ClaimIssue("other", 42); err != nil || !fresh {
		t.Fatal("issue 42 of another loop is still new")
	}
	if fresh, err := st.ClaimIssue("fix", 42); err != nil || !fresh {
		t.Fatal("issue 42 of fix is new")
	}
	if paused, err := st.Paused("unknown"); err != nil || paused {
		t.Fatalf("unknown loop: paused=%v err=%v", paused, err)
	}
}

func TestUnknownJob(t *testing.T) {
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if _, err := st.Job(99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := st.SetJobState(99, Running); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func open(t *testing.T, path string) *Store {
	t.Helper()
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestJobDetailsSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	st := open(t, path)
	job, err := st.CreateJob("fix")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.StartJob(job.ID, "spec docs/specs/x.md", "loop/fix-1", "/jobs/1/worktree"); err != nil {
		t.Fatal(err)
	}
	end := Finish{State: Done, Summary: "added Double", PRURL: "https://github.com/me/app/pull/7", CostUSD: 1.25}
	if err := st.FinishJob(job.ID, end); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := open(t, path).Job(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := Job{
		ID: job.ID, Loop: "fix", State: Done, Work: "spec docs/specs/x.md",
		Branch: "loop/fix-1", Worktree: "/jobs/1/worktree",
		Summary: "added Double", PRURL: "https://github.com/me/app/pull/7", CostUSD: 1.25,
	}
	if got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestInterruptFailsJobsLeftRunning(t *testing.T) {
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	running, _ := st.CreateJob("fix")
	if err := st.StartJob(running.ID, "task x", "loop/fix-1", "/wt"); err != nil {
		t.Fatal(err)
	}
	queued, _ := st.CreateJob("fix")
	blocked, _ := st.CreateJob("fix")
	if err := st.FinishJob(blocked.ID, Finish{State: Blocked, Question: "which db?"}); err != nil {
		t.Fatal(err)
	}

	if err := st.InterruptRunning("loopd restarted"); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]JobState{running.ID: Failed, queued.ID: Failed, blocked.ID: Blocked} {
		job, err := st.Job(id)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != want {
			t.Fatalf("job %d: state %s, want %s", id, job.State, want)
		}
		if want == Failed && job.Error != "loopd restarted" {
			t.Fatalf("job %d: error %q", id, job.Error)
		}
	}
}

func TestFinishAddsCostAcrossRuns(t *testing.T) {
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	job, _ := st.CreateJob("fix")
	if err := st.FinishJob(job.ID, Finish{State: Manual, CostUSD: 1.5}); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishJob(job.ID, Finish{State: Done, CostUSD: 0.75}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Job(job.ID); got.CostUSD != 2.25 {
		t.Fatalf("cost %v, want 2.25 over both runs", got.CostUSD)
	}
}
