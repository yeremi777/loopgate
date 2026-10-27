package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeremi777/loopgate/internal/gittest"
	"github.com/yeremi777/loopgate/internal/runner"
	"github.com/yeremi777/loopgate/internal/store"
)

func TestRemoveJobRefusesAQueuedRunningOrManualJobNamingItsState(t *testing.T) {
	r := newRunEnv(t)
	queued, err := r.st.CreateJob("deps")
	if err != nil {
		t.Fatal(err)
	}
	running, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	for id, state := range map[string]string{queued.ID: "queued", running.ID: "running"} {
		if _, err := r.d.RemoveJob(id); !errors.Is(err, store.ErrNotEnded) || !strings.Contains(err.Error(), "is "+state) {
			t.Fatalf("remove of a %s job: %v", state, err)
		}
	}
	if _, err := r.d.Takeover(running.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.RemoveJob(running.ID); !errors.Is(err, store.ErrNotEnded) || !strings.Contains(err.Error(), "is manual") {
		t.Fatalf("remove of a manual job: %v", err)
	}
	if _, err := os.Stat(running.Worktree); err != nil {
		t.Fatalf("a refused remove touched the worktree: %v", err)
	}
	if got, err := r.st.Job(running.ID); err != nil || got.State != store.Manual {
		t.Fatalf("a refused remove changed the job: %+v, %v", got, err)
	}
	if _, err := r.d.RemoveJob("999999999999"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("remove of an unknown job: %v", err)
	}
}

func TestRemoveJobOfADoneJobRemovesItsWorktreeDirectoryRowAndBranch(t *testing.T) {
	r := newRunEnv(t)
	r.release()
	started, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	r.waitDone(t, started.ID)

	removed, err := r.d.RemoveJob(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Job.ID != started.ID || removed.Job.PRURL != "https://github.com/me/app/pull/1" || removed.Kept != "" {
		t.Fatalf("removal %+v", removed)
	}
	assertRemoved(t, r, started)
	if branches := runGit(t, r.checkout, "branch", "--list", started.Branch); branches != "" {
		t.Fatalf("branch %s stays: %q", started.Branch, branches)
	}
}

func TestRemoveJobKeepsABranchWithCommitsTheJobDidNotEndAt(t *testing.T) {
	r := newRunEnv(t)
	r.stages.blockFirst = true
	r.release()
	started, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	r.waitDone(t, started.ID)
	gittest.Run(t, started.Worktree, "commit", "-q", "--allow-empty", "-m", "by hand after the job")
	head := runGit(t, started.Worktree, "rev-parse", "HEAD")

	removed, err := r.d.RemoveJob(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := "kept branch " + started.Branch + ": it has commits the job did not end at"; removed.Kept != want || removed.Job.PRURL != "" {
		t.Fatalf("removal %+v, want kept %q", removed, want)
	}
	assertRemoved(t, r, started)
	if at := runGit(t, r.checkout, "rev-parse", "refs/heads/"+started.Branch); at != head {
		t.Fatalf("kept branch points at %s, want %s", at, head)
	}
}

func TestSiblingWorktreeFolderStaysWhileAnotherWorktreeIsInItAndGoesWhenEmpty(t *testing.T) {
	r := newRunEnv(t)
	r.release()
	var jobs [2]store.Job
	for i := range jobs {
		started, err := r.d.Run("deps", "")
		if err != nil {
			t.Fatal(err)
		}
		jobs[i] = r.waitDone(t, started.ID)
		if got := readFile(t, filepath.Join(r.d.jobDir(started.ID), "stages.jsonl")); got == "" {
			t.Fatalf("stage rows of job %s are empty or missing in its job directory", started.ID)
		}
	}
	folder := r.checkout + "-worktrees"
	if _, err := r.d.RemoveJob(jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jobs[1].Worktree); err != nil {
		t.Fatalf("the other job's worktree after rm: %v", err)
	}
	if _, err := r.d.RemoveJob(jobs[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(folder); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s stays with no worktree in it: %v", folder, err)
	}
}

func TestRemoveJobKeepsTheBranchOfAJobWithNoStoredEndCommit(t *testing.T) {
	r := newRunEnv(t)
	old, err := r.st.CreateJob("deps")
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(r.d.home, "jobs", "1", "worktree")
	if _, err := runner.CreateWorktree(t.Context(), runner.Worktree{Checkout: r.checkout, Path: wt, Branch: "loop/deps-20260101-000000-1"}); err != nil {
		t.Fatal(err)
	}
	if err := r.st.StartJob(old.ID, store.Start{Work: "task x", Branch: "loop/deps-20260101-000000-1", Worktree: wt}); err != nil {
		t.Fatal(err)
	}
	if err := r.st.FinishJob(old.ID, store.Finish{State: store.Done, PRURL: "https://github.com/me/app/pull/3"}); err != nil {
		t.Fatal(err)
	}

	removed, err := r.d.RemoveJob(old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := "kept branch loop/deps-20260101-000000-1: it has commits the job did not end at"; removed.Kept != want || removed.Job.PRURL != "https://github.com/me/app/pull/3" {
		t.Fatalf("removal %+v, want kept %q", removed, want)
	}
	assertRemoved(t, r, store.Job{ID: old.ID, Worktree: wt})
	if branches := runGit(t, r.checkout, "branch", "--list", "loop/deps-20260101-000000-1"); branches == "" {
		t.Fatal("the branch of a job with no end commit was deleted")
	}
}

func TestRemoveJobWhoseDirectoryWasDeletedByHandStillPrunesItsWorktree(t *testing.T) {
	r := newRunEnv(t)
	r.release()
	started, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	r.waitDone(t, started.ID)
	if err := os.RemoveAll(filepath.Dir(started.Worktree)); err != nil {
		t.Fatal(err)
	}

	if _, err := r.d.RemoveJob(started.ID); err != nil {
		t.Fatal(err)
	}
	assertRemoved(t, r, started)
	if branches := runGit(t, r.checkout, "branch", "--list", started.Branch); branches != "" {
		t.Fatalf("branch %s stays: %q", started.Branch, branches)
	}
}

func TestRemoveJobOfARemovedLoopFindsTheCheckoutFromTheWorktree(t *testing.T) {
	r := newRunEnv(t)
	r.release()
	started, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	r.waitDone(t, started.ID)
	if err := r.d.Remove("deps"); err != nil {
		t.Fatal(err)
	}

	if _, err := r.d.RemoveJob(started.ID); err != nil {
		t.Fatal(err)
	}
	assertRemoved(t, r, started)
	if branches := runGit(t, r.checkout, "branch", "--list", started.Branch); branches != "" {
		t.Fatalf("branch %s stays: %q", started.Branch, branches)
	}
}

// assertRemoved fails t unless j's row, directory and worktree registration are gone.
func assertRemoved(t *testing.T, r *runEnv, j store.Job) {
	t.Helper()
	if _, err := r.st.Job(j.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("row of job %s stays: %v", j.ID, err)
	}
	for _, path := range []string{j.Worktree, r.d.dirOf(j)} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s of job %s stays: %v", path, j.ID, err)
		}
	}
	if n := worktrees(t, r.checkout); n != 1 {
		t.Fatalf("%d worktrees registered, want only the checkout", n)
	}
}
