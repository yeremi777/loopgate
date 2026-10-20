package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yeremi777/loopgate/internal/config"
	"github.com/yeremi777/loopgate/internal/feed"
	"github.com/yeremi777/loopgate/internal/job"
	"github.com/yeremi777/loopgate/internal/runner"
	"github.com/yeremi777/loopgate/internal/store"
)

// Deps are the parts of a Daemon that reach outside it.
type Deps struct {
	Pipeline job.Pipeline
	Issues   IssueTracker
	Notify   func(title, message string)
}

// IssueTracker reads a GitHub Loop's issues and answers on them.
type IssueTracker interface {
	List(ctx context.Context, repo, label string) ([]job.Issue, error)
	Comment(ctx context.Context, repo string, number int, body string) error
	Label(ctx context.Context, repo string, number int, label string) error
}

// Requeue lets the next poll of a GitHub Loop run issue again.
func (d *Daemon) Requeue(loop string, issue int) error {
	d.mu.Lock()
	l, ok := d.loops[loop]
	d.mu.Unlock()
	if !ok {
		return fmt.Errorf("%s: %w", loop, ErrUnknownLoop)
	}
	if _, ok := l.Trigger.(config.GitHub); !ok {
		return fmt.Errorf("%s is not a github loop", loop)
	}
	return d.st.UnclaimIssue(loop, issue)
}

var (
	ErrBusy           = errors.New("a job is already running")
	ErrNeedsSpec      = errors.New("a github loop runs by hand only with --spec")
	ErrNotTakeable    = errors.New("this job cannot be taken over")
	ErrNotManual      = errors.New("only a taken-over job can be handed back")
	ErrNothingRunning = errors.New("the loop has no running job")
	ErrStarting       = errors.New("the job is still starting; try again")
	ErrStopped        = errors.New("loopd stopped")
)

// running is the Job in the slot. id, cancel and feed are zero while its
// worktree is set up.
type running struct {
	id        string
	loop      string
	cancel    context.CancelFunc
	feed      *feed.Feed
	done      chan struct{}
	takenOver bool
	stopped   bool
}

// Run starts a Job of the named Loop on spec, or on the Loop's own task or
// spec when spec is empty. The worktree is ready when Run returns; the
// stages run in the background.
func (d *Daemon) Run(name, spec string) (store.Job, error) {
	return d.runWork(name, func(loop config.Loop) (job.Work, error) { return workFor(loop, spec) })
}

func (d *Daemon) runWork(name string, workOf func(config.Loop) (job.Work, error)) (store.Job, error) {
	d.mu.Lock()
	loop, ok := d.loops[name]
	if !ok {
		d.mu.Unlock()
		return store.Job{}, fmt.Errorf("%s: %w", name, ErrUnknownLoop)
	}
	work, err := workOf(loop)
	if err != nil {
		d.mu.Unlock()
		return store.Job{}, err
	}
	cur, err := d.claim(name)
	d.mu.Unlock()
	if err != nil {
		return store.Job{}, err
	}

	j, err := d.start(loop, work)
	if err != nil {
		d.release(cur)
		return store.Job{}, err
	}
	return d.launch(cur, j)
}

// Handback returns a taken-over Job to its Loop, which runs implement again
// in the same worktree, on top of the human's commits.
func (d *Daemon) Handback(id string) (store.Job, error) {
	stored, err := d.st.Job(id)
	if err != nil {
		return store.Job{}, err
	}
	if stored.State != store.Manual {
		return store.Job{}, fmt.Errorf("job %s is %s: %w", id, stored.State, ErrNotManual)
	}
	d.mu.Lock()
	loop, ok := d.loops[stored.Loop]
	if !ok {
		d.mu.Unlock()
		return store.Job{}, fmt.Errorf("%s: %w", stored.Loop, ErrUnknownLoop)
	}
	cur, err := d.claim(loop.Name)
	d.mu.Unlock()
	if err != nil {
		return store.Job{}, err
	}

	j, err := d.resume(loop, stored)
	if err != nil {
		d.release(cur)
		return store.Job{}, err
	}
	return d.launch(cur, j)
}

// Takeover stops a running Job, or takes a Blocked, exhausted or failed one,
// and marks it manual so the human works in its worktree.
func (d *Daemon) Takeover(id string) (store.Job, error) {
	d.mu.Lock()
	cur := d.current
	if cur != nil && cur.id == id {
		cur.takenOver = true
		d.mu.Unlock()
		cur.cancel()
		<-cur.done
		return d.st.Job(id)
	}
	d.mu.Unlock()

	j, err := d.st.Job(id)
	if err != nil {
		return store.Job{}, err
	}
	switch j.State {
	case store.Done:
		return store.Job{}, fmt.Errorf("job %s is done; check out %s instead: %w", id, j.Branch, ErrNotTakeable)
	case store.Queued, store.Running:
		return store.Job{}, fmt.Errorf("job %s: %w", id, ErrStarting)
	case store.Manual:
		return j, nil
	}
	if j.Worktree == "" {
		return store.Job{}, fmt.Errorf("job %s never got a worktree: %w", id, ErrNotTakeable)
	}
	if err := d.st.SetJobState(id, store.Manual); err != nil {
		return store.Job{}, err
	}
	return d.st.Job(id)
}

// Stop ends the named Loop's running Job; it is stored as failed.
func (d *Daemon) Stop(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	cur := d.current
	if cur == nil || cur.loop != name || cur.cancel == nil {
		return fmt.Errorf("%s: %w", name, ErrNothingRunning)
	}
	cur.stopped = true
	cur.cancel()
	return nil
}

// followPoll is how often Follow looks again for a Job that is starting.
const followPoll = 200 * time.Millisecond

// Follow sends the running Job's events to send until its outcome is
// stored, waiting first while the Job starts; for a Job that is not queued
// or running it returns at once. It fails with ErrStopped when loopd stops.
func (d *Daemon) Follow(ctx context.Context, id string, send func(feed.Event) error) error {
	for {
		if d.ctx.Err() != nil {
			return ErrStopped
		}
		var f *feed.Feed
		d.mu.Lock()
		if d.current != nil && d.current.id == id {
			f = d.current.feed
		}
		d.mu.Unlock()
		if f != nil {
			if err := f.Follow(ctx, send); err != nil {
				return err
			}
			if d.ctx.Err() != nil {
				return ErrStopped
			}
			return nil
		}
		j, err := d.st.Job(id)
		if err != nil {
			return err
		}
		if j.State != store.Queued && j.State != store.Running {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(followPoll):
		}
	}
}

// claim takes the running slot for loop. The caller holds d.mu.
func (d *Daemon) claim(loop string) (*running, error) {
	if d.current != nil {
		return nil, ErrBusy
	}
	d.current = &running{loop: loop, done: make(chan struct{})}
	return d.current, nil
}

func (d *Daemon) release(cur *running) {
	d.mu.Lock()
	if d.current == cur {
		d.current = nil
	}
	d.mu.Unlock()
	close(cur.done)
}

func (d *Daemon) launch(cur *running, j job.Job) (store.Job, error) {
	ctx, cancel := context.WithCancel(d.ctx)
	j.Feed = feed.New()
	d.mu.Lock()
	cur.id, cur.cancel, cur.feed = j.ID, cancel, j.Feed
	d.mu.Unlock()
	d.jobs.Add(1)
	go d.finish(ctx, cur, j)
	return d.st.Job(j.ID)
}

func (d *Daemon) start(loop config.Loop, work job.Work) (job.Job, error) {
	created, err := d.st.CreateJob(loop.Name)
	if err != nil {
		return job.Job{}, err
	}
	tag := time.Now().Format("20060102-150405")
	j := job.Job{ID: created.ID, Loop: loop, Work: work, Dir: d.jobDir(created.ID)}
	if issue, ok := work.(job.Issue); ok {
		tag, j.Issue = strconv.Itoa(issue.Number), issue.Number
	}
	j.Branch = "loop/" + loop.Name + "-" + tag + "-" + created.ID
	j.Worktree = filepath.Join(j.Dir, "worktree")
	if err := os.MkdirAll(j.Dir, 0o700); err != nil {
		return job.Job{}, d.fail(created.ID, err)
	}
	wt := runner.Worktree{Checkout: loop.Checkout, Path: j.Worktree, Branch: j.Branch}
	if s, ok := work.(job.Spec); ok {
		wt.Spec = s.Path
	}
	made, err := d.createWorktree(d.ctx, wt)
	if err != nil {
		return job.Job{}, d.fail(created.ID, err)
	}
	j.DefaultBranch, j.Base = made.DefaultBranch, made.Base
	return j, d.st.StartJob(j.ID, store.Start{Work: describe(work), Branch: j.Branch, Worktree: j.Worktree, Base: j.Base, Issue: j.Issue})
}

func (d *Daemon) resume(loop config.Loop, stored store.Job) (job.Job, error) {
	work, err := parseWork(stored.Work)
	if err != nil {
		return job.Job{}, err
	}
	// The issue was drilled before the takeover; resume from what drill left,
	// and keep the untrusted issue title out of the implement prompt.
	if issue, ok := work.(job.Issue); ok {
		work = job.Task{Text: fmt.Sprintf("continue GitHub issue #%d from this branch and its progress log", issue.Number)}
		spec := fmt.Sprintf("docs/specs/issue-%d.md", issue.Number)
		if _, err := os.Stat(filepath.Join(stored.Worktree, spec)); err == nil {
			work = job.Spec{Path: spec}
		}
	}
	ref, err := runner.Git(d.ctx, 30*time.Second, stored.Worktree, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return job.Job{}, err
	}
	j := job.Job{
		ID:            stored.ID,
		Loop:          loop,
		Work:          work,
		Dir:           d.dirOf(stored),
		Worktree:      stored.Worktree,
		Branch:        stored.Branch,
		DefaultBranch: strings.TrimPrefix(ref, "origin/"),
		Base:          stored.Base,
		Issue:         stored.Issue,
	}
	return j, d.st.SetJobState(j.ID, store.Running)
}

// finish stores the outcome and frees the slot under one lock, so no caller
// sees a finished Job still holding the slot.
func (d *Daemon) finish(ctx context.Context, cur *running, j job.Job) {
	defer d.jobs.Done()
	out := d.deps.Pipeline.Run(ctx, j)
	endCommit := d.endCommit(j)

	d.mu.Lock()
	end := store.Finish{State: out.State, Question: out.Question, Summary: out.Summary, PRURL: out.PRURL, CostUSD: out.CostUSD}
	switch {
	case out.State == store.Done:
		// The draft PR exists; a stop or takeover that came late changes nothing.
		cur.takenOver = false
	case cur.takenOver:
		end = store.Finish{State: store.Manual, Summary: out.Summary, CostUSD: out.CostUSD}
	case cur.stopped:
		end = store.Finish{State: store.Failed, Error: "stopped by loopctl stop", CostUSD: out.CostUSD}
	case out.Err != nil:
		end.Error = out.Err.Error()
	}
	end.EndCommit = endCommit
	if err := d.st.FinishJob(j.ID, end); err != nil {
		end.Error = errors.Join(out.Err, err).Error()
	}
	takenOver := cur.takenOver
	d.current = nil
	d.mu.Unlock()
	cur.feed.Close()
	close(cur.done)

	if !takenOver {
		d.deps.Notify(notice(j, end))
		d.answerIssue(j, end)
	}
}

// endCommit is the commit j's branch points at, or empty when git cannot
// say, which keeps the branch from loopctl rm. It runs after loopd began
// stopping too, so it does not use d.ctx.
func (d *Daemon) endCommit(j job.Job) string {
	commit, err := runner.Git(context.Background(), 30*time.Second, j.Worktree, "rev-parse", "--verify", "refs/heads/"+j.Branch)
	if err != nil {
		log.Printf("loopd: job %s: end commit: %v", j.ID, err)
	}
	return commit
}

// answerIssue tells an issue's author how its Job ended: the PR link, or the
// question that blocks it with the needs-drill label.
func (d *Daemon) answerIssue(j job.Job, end store.Finish) {
	gh, isGitHub := j.Loop.Trigger.(config.GitHub)
	if j.Issue == 0 || !isGitHub {
		return
	}
	var err error
	switch end.State {
	case store.Done:
		err = d.deps.Issues.Comment(d.ctx, gh.Repo, j.Issue, "loopgate opened a draft pull request for this issue: "+end.PRURL)
	case store.Blocked:
		err = d.deps.Issues.Comment(d.ctx, gh.Repo, j.Issue, "loopgate stopped before building this and needs a human decision:\n\n"+end.Question)
		if err == nil {
			err = d.deps.Issues.Label(d.ctx, gh.Repo, j.Issue, "needs-drill")
		}
	}
	if err != nil {
		log.Printf("loopd: answer issue #%d: %v", j.Issue, err)
	}
}

// poll starts a Job on the lowest open issue that the Loop has not handled.
func (d *Daemon) poll(name string, gh config.GitHub) {
	if d.isBusy() {
		return
	}
	issues, err := d.deps.Issues.List(d.ctx, gh.Repo, gh.Label)
	if err != nil {
		log.Printf("loopd: poll %s: %v", name, err)
		return
	}
	for _, issue := range issues {
		fresh, err := d.st.ClaimIssue(name, issue.Number)
		if err != nil {
			log.Printf("loopd: poll %s: %v", name, err)
			return
		}
		if !fresh {
			continue
		}
		_, err = d.runWork(name, func(config.Loop) (job.Work, error) { return issue, nil })
		if errors.Is(err, ErrBusy) {
			err = d.st.UnclaimIssue(name, issue.Number)
		}
		if err != nil {
			log.Printf("loopd: issue #%d of %s: %v", issue.Number, name, err)
		}
		return
	}
}

// fail records a Job that could not start and returns its error.
func (d *Daemon) fail(id string, err error) error {
	return errors.Join(err, d.st.FinishJob(id, store.Finish{State: store.Failed, Error: err.Error()}))
}

func (d *Daemon) isBusy() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.current != nil
}

func (d *Daemon) jobDir(id string) string {
	return filepath.Join(d.home, "jobs", id)
}

// dirOf is a stored Job's directory, the parent of its worktree, so a Job
// from before random ids keeps its integer-named one.
func (d *Daemon) dirOf(j store.Job) string {
	if j.Worktree == "" {
		return d.jobDir(j.ID)
	}
	return filepath.Dir(j.Worktree)
}

// Close stops the running Job and waits until its outcome is stored.
func (d *Daemon) Close() {
	d.mu.Lock()
	d.cancel()
	d.mu.Unlock()
	d.jobs.Wait()
}

type JobLog struct {
	Job      store.Job `json:"job"`
	Progress string    `json:"progress"`
}

func (d *Daemon) Job(id string) (JobLog, error) {
	j, err := d.st.Job(id)
	if err != nil {
		return JobLog{}, err
	}
	progress, err := os.ReadFile(filepath.Join(d.dirOf(j), "progress.md"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return JobLog{}, err
	}
	return JobLog{Job: j, Progress: string(progress)}, nil
}

func workFor(loop config.Loop, spec string) (job.Work, error) {
	if spec != "" {
		rel := spec
		if filepath.IsAbs(spec) {
			var err error
			if rel, err = filepath.Rel(loop.Checkout, spec); err != nil {
				return nil, err
			}
		}
		if !filepath.IsLocal(rel) {
			return nil, fmt.Errorf("spec %s is outside the loop's checkout %s", spec, loop.Checkout)
		}
		return job.Spec{Path: rel}, nil
	}
	timer, ok := loop.Trigger.(config.Timer)
	if !ok {
		return nil, ErrNeedsSpec
	}
	if timer.Spec != "" {
		return job.Spec{Path: timer.Spec}, nil
	}
	return job.Task{Text: timer.Task}, nil
}

// describe and parseWork store a Job's Work as one line: "spec <path>",
// "task <text>", or "issue <number> <title>".
func describe(w job.Work) string {
	switch w := w.(type) {
	case job.Spec:
		return "spec " + w.Path
	case job.Task:
		return "task " + w.Text
	case job.Issue:
		return "issue " + strconv.Itoa(w.Number) + " " + w.Title
	}
	return ""
}

func parseWork(s string) (job.Work, error) {
	if rest, ok := strings.CutPrefix(s, "issue "); ok {
		num, title, _ := strings.Cut(rest, " ")
		n, err := strconv.Atoi(num)
		if err != nil {
			return nil, fmt.Errorf("stored work %q: %w", s, err)
		}
		return job.Issue{Number: n, Title: title}, nil
	}
	if path, ok := strings.CutPrefix(s, "spec "); ok {
		return job.Spec{Path: path}, nil
	}
	if text, ok := strings.CutPrefix(s, "task "); ok {
		return job.Task{Text: text}, nil
	}
	return nil, fmt.Errorf("stored work %q is neither a spec nor a task", s)
}

func notice(j job.Job, end store.Finish) (string, string) {
	title := fmt.Sprintf("loopgate: %s job %s %s", j.Loop.Name, j.ID, end.State)
	switch end.State {
	case store.Done:
		return title, "Draft PR ready: " + end.PRURL
	case store.Blocked:
		return title, end.Question
	case store.Failed:
		return title, end.Error
	}
	return title, end.Summary
}
