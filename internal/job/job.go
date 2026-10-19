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
	Branch        string
	DefaultBranch string
	// Base is the commit the worktree was cut from; every branch diff uses it.
	Base string
	// Issue is the GitHub issue number the Job answers, or 0.
	Issue int
}

// Stager runs one Claude Code stage; runner.Runner is the real one.
type Stager interface {
	Run(ctx context.Context, s runner.Stage) (runner.Result, error)
}

// PR is the pull request text that land writes.
type PR struct {
	Title string
	Body  string
}

// Publisher pushes a branch and opens its draft PR, returning the PR's URL.
type Publisher interface {
	Publish(ctx context.Context, dir, branch, base string, pr PR) (string, error)
}

type Pipeline struct {
	Stages    Stager
	Shell     runner.Runner
	Publisher Publisher
}

// Outcome ends a Job. Gated is the commit that passed the Verify gate and rubric.
type Outcome struct {
	State       store.JobState
	Question    string
	Summary     string
	Gated       string
	TestChanges []verify.Change
	PRURL       string
	CostUSD     float64
	Err         error
}

// maxLogOutput bounds each verify output kept in the progress log, so the
// implement prompt stays small across iterations.
const maxLogOutput = 4000

// Run implements, gates, and reviews until rubric passes a gated tree, then
// lands it offline and publishes the draft PR. It stops early when a stage is
// blocked or the Loop's bounds run out.
func (p Pipeline) Run(ctx context.Context, j Job) Outcome {
	ctx, cancel := context.WithTimeout(ctx, j.Loop.Bounds.Timeout)
	defer cancel()
	guard, err := runner.GitGuard(j.Worktree)
	if err != nil {
		return Outcome{State: store.Failed, Err: err}
	}
	r := &jobRun{Pipeline: p, job: j, guard: guard, progress: progressLog{path: filepath.Join(j.Dir, "progress.md")}}
	earlier, err := os.ReadFile(r.progress.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Outcome{State: store.Failed, Err: err}
	}
	r.progress.text = string(earlier)
	out, err := r.iterate(ctx)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		out = Outcome{State: store.Exhausted, Summary: "timeout of " + j.Loop.Bounds.Timeout.String() + " reached", Err: err}
	case err != nil:
		out = Outcome{State: store.Failed, Err: err}
	}
	out.CostUSD = r.cost
	return out
}

// jobRun is one Run in progress: the work it implements, its progress log,
// and the cost of its stages so far. An issue's work is what drill made of it.
type jobRun struct {
	Pipeline
	job      Job
	work     Work
	drill    string
	guard    []string
	progress progressLog
	cost     float64
}

func (r *jobRun) iterate(ctx context.Context) (Outcome, error) {
	j := r.job
	r.work = j.Work
	if issue, ok := j.Work.(Issue); ok {
		res, err := r.stage(ctx, drillPrompt(issue))
		if err != nil {
			return Outcome{}, err
		}
		switch res.Status {
		case runner.StatusBlocked:
			return Outcome{State: store.Blocked, Question: res.Question, Summary: res.Summary}, nil
		case runner.StatusRevise:
			return Outcome{}, errors.New("drill reported revise, which only rubric may report")
		}
		r.drill = res.Summary
		r.work = Task{Text: res.Summary}
		spec := issueSpec(issue)
		if _, err := os.Stat(filepath.Join(j.Worktree, spec)); err == nil {
			r.work = Spec{Path: spec}
		}
	}
	for i := 1; i <= j.Loop.Bounds.MaxIterations; i++ {
		res, err := r.stage(ctx, implementPrompt(r.work, r.progress.text))
		if err != nil {
			return Outcome{}, err
		}
		switch res.Status {
		case runner.StatusBlocked:
			return Outcome{State: store.Blocked, Question: res.Question, Summary: res.Summary}, nil
		case runner.StatusRevise:
			return Outcome{}, errors.New("implement reported revise, which only rubric may report")
		}

		gate, err := verify.Gate(ctx, r.Shell, j.Worktree, j.Loop.Verify, remaining(ctx))
		if err != nil {
			return Outcome{}, err
		}
		if !gate.Passed {
			if err := r.progress.add(i, "verify gate failed", "```\n"+tail(gate.Output)+"\n```"); err != nil {
				return Outcome{}, err
			}
			continue
		}

		changes, err := verify.Changes(ctx, j.Worktree, j.Base)
		if err != nil {
			return Outcome{}, err
		}

		review, err := r.stage(ctx, rubricPrompt(j.Base))
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
			if err := r.progress.add(i, "rubric found defects to fix", review.Summary); err != nil {
				return Outcome{}, err
			}
			continue
		}
		return r.ship(ctx, Outcome{
			State:       store.Done,
			Summary:     res.Summary,
			Gated:       gate.Head,
			TestChanges: verify.TestChanges(changes, j.Loop.Tests),
		})
	}
	return Outcome{State: store.Exhausted, Summary: fmt.Sprintf("%d implement runs did not pass the checks", j.Loop.Bounds.MaxIterations)}, nil
}

// ship lands the gated tree offline, checks that land changed only Markdown,
// and publishes the branch as a draft PR.
func (r *jobRun) ship(ctx context.Context, out Outcome) (Outcome, error) {
	j := r.job
	land, err := r.stage(ctx, landPrompt(r.work))
	if err != nil {
		return Outcome{}, err
	}
	switch land.Status {
	case runner.StatusBlocked:
		return Outcome{State: store.Blocked, Question: land.Question, Summary: land.Summary}, nil
	case runner.StatusRevise:
		return Outcome{}, errors.New("land reported revise, which only rubric may report")
	}
	if err := verify.ShipCheck(ctx, j.Worktree, out.Gated, j.Loop.Protected); err != nil {
		return Outcome{}, fmt.Errorf("land changed the gated work: %w", err)
	}
	pr, err := parsePR(land.Summary)
	if err != nil {
		return Outcome{}, err
	}
	pr.Body = report(j, out, r.drill, r.cost) + "\n\n---\n\n" + pr.Body
	url, err := r.Publisher.Publish(ctx, j.Worktree, j.Branch, j.DefaultBranch, pr)
	if err != nil {
		return Outcome{}, fmt.Errorf("publish: %w", err)
	}
	out.PRURL = url
	return out, nil
}

// stage runs one stage, then blocks the Job when the stage left a protected
// path changed, before any later stage can load it.
func (r *jobRun) stage(ctx context.Context, prompt string) (runner.Result, error) {
	res, err := r.Stages.Run(ctx, runner.Stage{
		Dir:       r.job.Worktree,
		Prompt:    prompt,
		Timeout:   remaining(ctx),
		Network:   r.job.Loop.Network,
		DenyWrite: r.guard,
	})
	r.cost += res.CostUSD
	if err != nil {
		return res, err
	}
	hits, err := verify.Touched(ctx, r.job.Worktree, r.job.Base, r.job.Loop.Protected)
	if err != nil {
		return runner.Result{}, err
	}
	if hits != nil {
		return runner.Result{
			Status:   runner.StatusBlocked,
			Question: "The change touches protected paths: " + strings.Join(hits, ", ") + ". Should these change?",
			Summary:  res.Summary,
			CostUSD:  res.CostUSD,
		}, nil
	}
	return res, nil
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

// Issue is a GitHub issue that a Job drills before it implements.
type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

func (Issue) isWork() {}
