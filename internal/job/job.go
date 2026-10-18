// Package job carries one Job through its stages within its Loop's bounds.
package job

import (
	"context"
	"errors"

	"github.com/yeremi777/loopgate/internal/config"
	"github.com/yeremi777/loopgate/internal/runner"
	"github.com/yeremi777/loopgate/internal/store"
	"github.com/yeremi777/loopgate/internal/verify"
)

// Work is what a Job implements: a Spec or an obvious-plan Task.
type Work interface{ isWork() }

type Spec struct{ Path string }

type Task struct{ Text string }

func (Spec) isWork() {}
func (Task) isWork() {}

type Job struct {
	ID            int64
	Loop          config.Loop
	Work          Work
	Dir           string
	Worktree      string
	DefaultBranch string
}

// Stager runs one Claude Code stage; runner.Runner is the real one.
type Stager interface {
	Run(ctx context.Context, s runner.Stage) (runner.Result, error)
}

type Pipeline struct {
	Stages Stager
	Shell  runner.Runner
}

// Outcome ends a Job. Gated is the commit that passed the Verify gate and rubric.
type Outcome struct {
	State       store.JobState
	Question    string
	Summary     string
	Gated       string
	TestChanges []verify.Change
	Err         error
}

func (p Pipeline) Run(ctx context.Context, j Job) Outcome {
	return Outcome{State: store.Failed, Err: errors.New("not implemented")}
}
