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
	"github.com/yeremi777/loopgate/internal/feed"
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
	ID            string
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
	// Feed gets the Job's live events.
	Feed *feed.Feed
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
	TestGuide   string
	PRURL       string
	CostUSD     float64
	Err         error
}

// maxLogOutput bounds each verify output kept in the progress log, so the
// implement prompt stays small across iterations.
const maxLogOutput = 4000

// maxTestGuide bounds the Test guide kept on a Job and shown in its PR.
const maxTestGuide = 4000

// gateTail is how many lines of a failed gate's output its live view shows.
const gateTail = 20

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
		res, err := r.stage(ctx, "drill", drillPrompt(issue))
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
		res, err := r.stage(ctx, fmt.Sprintf("implement %d", i), implementPrompt(r.work, r.progress.text))
		if err != nil {
			return Outcome{}, err
		}
		switch res.Status {
		case runner.StatusBlocked:
			return Outcome{State: store.Blocked, Question: res.Question, Summary: res.Summary}, nil
		case runner.StatusRevise:
			return Outcome{}, errors.New("implement reported revise, which only rubric may report")
		}

		gate, err := r.gate(ctx, i)
		if err != nil {
			return Outcome{}, err
		}
		// The setup and verify commands run code too, so they face the same check as a stage.
		question, err := r.protectedQuestion(ctx)
		if err != nil {
			return Outcome{}, err
		}
		if question != "" {
			return Outcome{State: store.Blocked, Question: question, Summary: res.Summary}, nil
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

		review, err := r.stage(ctx, fmt.Sprintf("rubric %d", i), rubricPrompt(j.Base))
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
	land, err := r.stage(ctx, "land", landPrompt(r.work))
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
	out.TestGuide = cut(strings.TrimSpace(land.Try), maxTestGuide)
	pr.Body = report(j, out, r.drill, r.cost) + "\n\n---\n\n" + pr.Body
	start := time.Now()
	url, err := r.Publisher.Publish(ctx, j.Worktree, j.Branch, j.DefaultBranch, pr)
	row := StageRow{Stage: "publish", Start: start, End: time.Now(), Status: "done"}
	if err != nil {
		row.Status, err = "failed", fmt.Errorf("publish: %w", err)
	}
	if err := errors.Join(err, appendStage(j.Dir, row)); err != nil {
		return Outcome{}, err
	}
	j.Feed.Bound("publish", "draft PR "+url)
	out.PRURL = url
	return out, nil
}

// stage runs one stage between its start and end events, and adds its row.
func (r *jobRun) stage(ctx context.Context, name, prompt string) (runner.Result, error) {
	row := StageRow{Stage: name, Start: time.Now()}
	r.job.Feed.Bound(name, "start")
	res, err := r.runStage(ctx, name, prompt)
	row.Status, row.CostUSD = string(res.Status), res.CostUSD
	switch {
	case err != nil:
		row.Status = "failed"
		r.job.Feed.Bound(name, "failed: "+err.Error())
	case res.Status == runner.StatusBlocked:
		r.job.Feed.Bound(name, fmt.Sprintf("blocked $%.2f: %s", res.CostUSD, res.Question))
	default:
		r.job.Feed.Bound(name, fmt.Sprintf("%s $%.2f", res.Status, res.CostUSD))
	}
	row.End = time.Now()
	return res, errors.Join(err, appendStage(r.job.Dir, row))
}

// runStage runs one stage, then blocks the Job when the stage left a
// protected path changed, before any later stage can load it.
func (r *jobRun) runStage(ctx context.Context, name, prompt string) (runner.Result, error) {
	res, err := r.Stages.Run(ctx, runner.Stage{
		Dir:       r.job.Worktree,
		Prompt:    prompt,
		Timeout:   remaining(ctx),
		Network:   r.job.Loop.Network,
		DenyWrite: r.guard,
		Log:       func(s feed.Step) { r.job.Feed.Step(name, s) },
	})
	r.cost += res.CostUSD
	if err != nil {
		return res, err
	}
	question, err := r.protectedQuestion(ctx)
	if err != nil {
		return runner.Result{}, err
	}
	if question != "" {
		return runner.Result{Status: runner.StatusBlocked, Question: question, Summary: res.Summary, CostUSD: res.CostUSD}, nil
	}
	return res, nil
}

// gate runs the Verify gate of iteration i, reporting each command as it
// starts, its result, and on failure the tail of its output, and adds its row.
func (r *jobRun) gate(ctx context.Context, i int) (verify.Outcome, error) {
	name := fmt.Sprintf("verify %d", i)
	row := StageRow{Stage: name, Start: time.Now()}
	gate, err := verify.Gate(ctx, r.Shell, r.job.Worktree, r.job.Loop, remaining(ctx), r.guard, func(step string) { r.job.Feed.Bound(name, step) })
	row.Commands = gate.Commands
	switch {
	case err != nil:
		row.Status = "failed"
		r.job.Feed.Bound(name, "failed: "+err.Error())
	case gate.Passed:
		row.Status = "pass"
		r.job.Feed.Bound(name, "pass: verify command exited 0")
	default:
		lines := strings.Split(strings.TrimSpace(gate.Output), "\n")
		row.Status, row.Output = "fail", lines[max(0, len(lines)-gateTail):]
		r.job.Feed.Bound(name, "fail: "+strings.TrimSuffix(lines[0], ":"))
		for _, line := range lines[max(1, len(lines)-gateTail):] {
			r.job.Feed.Line(name, "│ "+line)
		}
	}
	row.End = time.Now()
	return gate, errors.Join(err, appendStage(r.job.Dir, row))
}

// protectedQuestion asks whether the protected paths changed since the Job's
// base should change, and is empty when none did.
func (r *jobRun) protectedQuestion(ctx context.Context) (string, error) {
	hits, err := verify.Touched(ctx, r.job.Worktree, r.job.Base, r.job.Loop.Protected)
	if err != nil || hits == nil {
		return "", err
	}
	return "The change touches protected paths: " + strings.Join(hits, ", ") + ". Should these change?", nil
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

// cut shortens s to max characters, ending it in "…".
func cut(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
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
