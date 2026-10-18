// Package runner runs one Claude Code stage in a Job worktree.
package runner

import (
	"context"
	"errors"
	"time"
)

type Status string

const (
	StatusDone    Status = "done"
	StatusBlocked Status = "blocked"
)

// Result is the stage's structured report.
type Result struct {
	Status   Status
	Question string
	Summary  string
}

// Runner starts claude with Environ filtered to the allowlist.
type Runner struct {
	Claude  string
	Environ []string
}

type Stage struct {
	Dir     string
	Prompt  string
	Timeout time.Duration
}

func (r Runner) Run(ctx context.Context, s Stage) (Result, error) {
	return Result{}, errors.New("not implemented")
}
