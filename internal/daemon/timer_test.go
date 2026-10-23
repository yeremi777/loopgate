package daemon

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)

func TestTickFiresTimerLoopOnItsSchedule(t *testing.T) {
	r := newRunEnv(t)
	r.release()
	r.d.Tick(t0)
	r.d.Tick(t0.Add(59 * time.Minute))
	if n := r.jobCount(t); n != 0 {
		t.Fatalf("%d jobs before the first fire is due", n)
	}
	r.d.Tick(t0.Add(time.Hour))
	if n := r.jobCount(t); n != 1 {
		t.Fatalf("%d jobs after the fire came due, want 1", n)
	}
	if j := r.waitDone(t, r.nth(t, 1)); j.Work != "task bump go dependencies" {
		t.Fatalf("fired job %+v", j)
	}
}

func TestTickSkipsFireWhileBusyWithoutCatchUp(t *testing.T) {
	r := newRunEnv(t)
	r.d.Tick(t0)
	r.d.Tick(t0.Add(time.Hour))
	r.d.Tick(t0.Add(2 * time.Hour))
	if n := r.jobCount(t); n != 1 {
		t.Fatalf("%d jobs while the first still runs, want 1", n)
	}
	r.release()
	r.waitDone(t, r.nth(t, 1))
	r.d.Tick(t0.Add(2*time.Hour + time.Minute))
	if n := r.jobCount(t); n != 1 {
		t.Fatalf("skipped fire was caught up: %d jobs", n)
	}
	r.d.Tick(t0.Add(3*time.Hour + time.Minute))
	if n := r.jobCount(t); n != 2 {
		t.Fatalf("%d jobs at the next fire, want 2", n)
	}
	r.waitDone(t, r.nth(t, 2))
}

func TestTickSkipsPausedAndRemovedLoops(t *testing.T) {
	r := newRunEnv(t)
	r.release()
	r.d.Tick(t0)
	if err := r.d.SetPaused("deps", true); err != nil {
		t.Fatal(err)
	}
	r.d.Tick(t0.Add(time.Hour))
	if err := r.d.SetPaused("deps", false); err != nil {
		t.Fatal(err)
	}
	r.d.Tick(t0.Add(time.Hour + time.Minute))
	if n := r.jobCount(t); n != 0 {
		t.Fatalf("paused fire ran or was caught up: %d jobs", n)
	}
	if err := r.d.Remove("deps"); err != nil {
		t.Fatal(err)
	}
	r.d.Tick(t0.Add(5 * time.Hour))
	if n := r.jobCount(t); n != 0 {
		t.Fatalf("removed loop fired: %d jobs", n)
	}
}

// jobCount counts stored Jobs, of which these tests end at most five.
func (r *runEnv) jobCount(t *testing.T) int {
	t.Helper()
	jobs, err := r.st.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	return len(jobs)
}

// nth is the id of the nth Job created, counting from 1.
func (r *runEnv) nth(t *testing.T, n int) string {
	t.Helper()
	jobs, err := r.st.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) < n {
		t.Fatalf("%d jobs, want at least %d", len(jobs), n)
	}
	return jobs[len(jobs)-n].ID
}
