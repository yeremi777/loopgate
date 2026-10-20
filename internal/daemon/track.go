package daemon

import (
	"errors"

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
func (d *Daemon) Track(id string) (Track, error) { return Track{}, errors.New("not implemented") }
