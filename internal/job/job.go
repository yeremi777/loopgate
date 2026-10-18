// Package job carries one Job through its stages within its Loop's bounds.
package job

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// maxLogOutput bounds each verify output kept in the progress log, so the
// implement prompt stays small across iterations.
const maxLogOutput = 4000

// Run implements, gates, and reviews until rubric passes a gated tree, a stage
// is blocked, or the Loop's bounds run out.
func (p Pipeline) Run(ctx context.Context, j Job) Outcome {
	ctx, cancel := context.WithTimeout(ctx, j.Loop.Bounds.Timeout)
	defer cancel()
	out, err := p.iterate(ctx, j)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return Outcome{State: store.Exhausted, Summary: "timeout of " + j.Loop.Bounds.Timeout.String() + " reached", Err: err}
	case err != nil:
		return Outcome{State: store.Failed, Err: err}
	}
	return out
}

func (p Pipeline) iterate(ctx context.Context, j Job) (Outcome, error) {
	progress := progressLog{path: filepath.Join(j.Dir, "progress.md")}
	for i := 1; i <= j.Loop.Bounds.MaxIterations; i++ {
		res, err := p.stage(ctx, j, implementPrompt(j.Work, progress.text))
		if err != nil {
			return Outcome{}, err
		}
		switch res.Status {
		case runner.StatusBlocked:
			return Outcome{State: store.Blocked, Question: res.Question, Summary: res.Summary}, nil
		case runner.StatusRevise:
			return Outcome{}, errors.New("implement reported revise, which only rubric may report")
		}

		gate, err := verify.Gate(ctx, p.Shell, j.Worktree, j.Loop.Verify, remaining(ctx))
		if err != nil {
			return Outcome{}, err
		}
		if !gate.Passed {
			if err := progress.add(i, "verify gate failed", "```\n"+tail(gate.Output)+"\n```"); err != nil {
				return Outcome{}, err
			}
			continue
		}

		changes, err := verify.Changes(ctx, j.Worktree, j.DefaultBranch)
		if err != nil {
			return Outcome{}, err
		}
		if hits := verify.Protected(changes, j.Loop.Protected); hits != nil {
			return Outcome{
				State:    store.Blocked,
				Question: "The change touches protected paths: " + strings.Join(hits, ", ") + ". Should these change?",
				Summary:  res.Summary,
			}, nil
		}

		review, err := p.stage(ctx, j, rubricPrompt(j.DefaultBranch))
		if err != nil {
			return Outcome{}, err
		}
		if err := unchanged(ctx, j.Worktree, gate.Head); err != nil {
			return Outcome{}, err
		}
		switch review.Status {
		case runner.StatusBlocked:
			return Outcome{State: store.Blocked, Question: review.Question, Summary: review.Summary}, nil
		case runner.StatusRevise:
			if err := progress.add(i, "rubric found defects to fix", review.Summary); err != nil {
				return Outcome{}, err
			}
			continue
		}
		return Outcome{
			State:       store.Done,
			Summary:     res.Summary,
			Gated:       gate.Head,
			TestChanges: verify.TestChanges(changes, j.Loop.Tests),
		}, nil
	}
	return Outcome{State: store.Exhausted, Summary: fmt.Sprintf("%d implement runs did not pass the checks", j.Loop.Bounds.MaxIterations)}, nil
}

func (p Pipeline) stage(ctx context.Context, j Job, prompt string) (runner.Result, error) {
	return p.Stages.Run(ctx, runner.Stage{
		Dir:     j.Worktree,
		Prompt:  prompt,
		Timeout: remaining(ctx),
		Network: j.Loop.Network,
	})
}

// unchanged fails when a reviewer moved HEAD or left edits, since reviewers never edit.
func unchanged(ctx context.Context, dir, head string) error {
	status, err := runner.Git(ctx, 30*time.Second, dir, "status", "--porcelain")
	if err != nil {
		return err
	}
	now, err := runner.Git(ctx, 30*time.Second, dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if status != "" || now != head {
		return fmt.Errorf("rubric changed the tree it reviewed (head %s, was %s; status %q)", now, head, status)
	}
	return nil
}

func remaining(ctx context.Context) time.Duration {
	deadline, _ := ctx.Deadline()
	return time.Until(deadline)
}

// progressLog is the record of failed iterations that each new implement run reads.
type progressLog struct {
	path string
	text string
}

func (l *progressLog) add(iteration int, title, body string) error {
	entry := fmt.Sprintf("## Iteration %d: %s\n\n%s\n\n", iteration, title, body)
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(entry); err != nil {
		return errors.Join(err, f.Close())
	}
	l.text += entry
	return f.Close()
}

func tail(s string) string {
	if len(s) > maxLogOutput {
		return "..." + s[len(s)-maxLogOutput:]
	}
	return s
}
