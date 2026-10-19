package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/yeremi777/loopgate/internal/store"
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
	if j := r.waitDone(t, 1); j.Work != "task bump go dependencies" {
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
	r.waitDone(t, 1)
	r.d.Tick(t0.Add(2*time.Hour + time.Minute))
	if n := r.jobCount(t); n != 1 {
		t.Fatalf("skipped fire was caught up: %d jobs", n)
	}
	r.d.Tick(t0.Add(3*time.Hour + time.Minute))
	if n := r.jobCount(t); n != 2 {
		t.Fatalf("%d jobs at the next fire, want 2", n)
	}
	r.waitDone(t, 2)
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

// jobCount counts stored Jobs; ids start at 1 and have no gaps.
func (r *runEnv) jobCount(t *testing.T) int {
	t.Helper()
	n := 0
	for {
		if _, err := r.st.Job(int64(n + 1)); errors.Is(err, store.ErrNotFound) {
			return n
		} else if err != nil {
			t.Fatal(err)
		}
		n++
	}
}
