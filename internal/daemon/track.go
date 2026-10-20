package daemon

import (
	"github.com/yeremi777/loopgate/internal/job"
	"github.com/yeremi777/loopgate/internal/store"
)

// Track is what a Job's tracking page shows: the Job, its Loop's bound on
// implement runs, 0 once the Loop is removed, and its stage rows.
type Track struct {
	Job           store.Job      `json:"job"`
	MaxIterations int            `json:"max_iterations"`
	Stages        []job.StageRow `json:"stages"`
}

// Track is the tracking page's view of Job id.
func (d *Daemon) Track(id string) (Track, error) {
	j, err := d.st.Job(id)
	if err != nil {
		return Track{}, err
	}
	stages, err := job.ReadStages(d.dirOf(j))
	if err != nil {
		return Track{}, err
	}
	d.mu.Lock()
	loop, ok := d.loops[j.Loop]
	d.mu.Unlock()
	tr := Track{Job: j, Stages: stages}
	if ok {
		tr.MaxIterations = loop.Bounds.MaxIterations
	}
	return tr, nil
}
