package daemon

import (
	"slices"
	"testing"

	"github.com/yeremi777/loopgate/internal/store"
)

func TestTrackShowsTheJobItsLoopsBoundAndItsStageRows(t *testing.T) {
	r := newRunEnv(t)
	r.release()
	started, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	r.waitDone(t, started.ID)

	tr, err := r.d.Track(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Job.ID != started.ID || tr.Job.State != store.Done || tr.Job.Started.IsZero() || tr.Job.Ended.Before(tr.Job.Started) {
		t.Fatalf("job %+v; want done with its start and end", tr.Job)
	}
	if tr.MaxIterations != 3 {
		t.Fatalf("max iterations %d, want the loop's 3", tr.MaxIterations)
	}
	var stages []string
	for _, row := range tr.Stages {
		stages = append(stages, row.Stage+" "+row.Status)
	}
	if want := []string{"implement 1 done", "verify 1 pass", "rubric 1 done", "land done", "publish done"}; !slices.Equal(stages, want) {
		t.Fatalf("stages %q, want %q", stages, want)
	}

	if err := r.d.Remove("deps"); err != nil {
		t.Fatal(err)
	}
	if tr, err := r.d.Track(started.ID); err != nil || tr.MaxIterations != 0 || len(tr.Stages) != len(stages) {
		t.Fatalf("after the loop's removal: %+v, %v; want no bound and the same stages", tr, err)
	}
}
