package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/yeremi777/loopgate/internal/config"
	"github.com/yeremi777/loopgate/internal/job"
	"github.com/yeremi777/loopgate/internal/runner"
	"github.com/yeremi777/loopgate/internal/store"
)

// Deps are the parts of a Daemon that reach outside it.
type Deps struct {
	Pipeline job.Pipeline
	Notify   func(title, message string)
}

var (
	ErrBusy      = errors.New("a job is already running")
	ErrNeedsSpec = errors.New("a github loop runs by hand only with --spec")
)

// Run starts a Job of the named Loop on spec, or on the Loop's own task or
// spec when spec is empty. The worktree is ready when Run returns; the
// stages run in the background.
func (d *Daemon) Run(name, spec string) (store.Job, error) {
	d.mu.Lock()
	loop, ok := d.loops[name]
	switch {
	case !ok:
		d.mu.Unlock()
		return store.Job{}, fmt.Errorf("%s: %w", name, ErrUnknownLoop)
	case d.busy:
		d.mu.Unlock()
		return store.Job{}, ErrBusy
	}
	work, err := workFor(loop, spec)
	if err != nil {
		d.mu.Unlock()
		return store.Job{}, err
	}
	d.busy = true
	d.mu.Unlock()

	started, j, err := d.start(loop, work)
	if err != nil {
		d.release()
		return store.Job{}, err
	}
	d.jobs.Add(1)
	go d.finish(j)
	return started, nil
}

func (d *Daemon) start(loop config.Loop, work job.Work) (store.Job, job.Job, error) {
	created, err := d.st.CreateJob(loop.Name)
	if err != nil {
		return store.Job{}, job.Job{}, err
	}
	dir := filepath.Join(d.home, "jobs", strconv.FormatInt(created.ID, 10))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return store.Job{}, job.Job{}, d.fail(created.ID, err)
	}
	j := job.Job{
		ID:       created.ID,
		Loop:     loop,
		Work:     work,
		Dir:      dir,
		Worktree: filepath.Join(dir, "worktree"),
		Branch:   "loop/" + loop.Name + "-" + time.Now().Format("20060102-150405") + "-" + strconv.FormatInt(created.ID, 10),
	}
	wt := runner.Worktree{Checkout: loop.Checkout, Path: j.Worktree, Branch: j.Branch}
	if s, ok := work.(job.Spec); ok {
		wt.Spec = s.Path
	}
	if j.DefaultBranch, err = runner.CreateWorktree(d.ctx, wt); err != nil {
		return store.Job{}, job.Job{}, d.fail(created.ID, err)
	}
	if err := d.st.StartJob(j.ID, describe(work), j.Branch, j.Worktree); err != nil {
		return store.Job{}, job.Job{}, err
	}
	started, err := d.st.Job(j.ID)
	return started, j, err
}

func (d *Daemon) finish(j job.Job) {
	defer d.jobs.Done()
	defer d.release()
	out := d.deps.Pipeline.Run(d.ctx, j)
	end := store.Finish{State: out.State, Question: out.Question, Summary: out.Summary, PRURL: out.PRURL, CostUSD: out.CostUSD}
	if out.Err != nil {
		end.Error = out.Err.Error()
	}
	if err := d.st.FinishJob(j.ID, end); err != nil {
		end.Error = errors.Join(out.Err, err).Error()
	}
	d.deps.Notify(notice(j, end))
}

// fail records a Job that could not start and returns its error.
func (d *Daemon) fail(id int64, err error) error {
	return errors.Join(err, d.st.FinishJob(id, store.Finish{State: store.Failed, Error: err.Error()}))
}

func (d *Daemon) release() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.busy = false
}

func (d *Daemon) isBusy() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.busy
}

// Close stops the running Job and waits until its outcome is stored.
func (d *Daemon) Close() {
	d.cancel()
	d.jobs.Wait()
}

type JobLog struct {
	Job      store.Job `json:"job"`
	Progress string    `json:"progress"`
}

func (d *Daemon) Job(id int64) (JobLog, error) {
	j, err := d.st.Job(id)
	if err != nil {
		return JobLog{}, err
	}
	progress, err := os.ReadFile(filepath.Join(d.home, "jobs", strconv.FormatInt(id, 10), "progress.md"))
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

func describe(w job.Work) string {
	switch w := w.(type) {
	case job.Spec:
		return "spec " + w.Path
	case job.Task:
		return "task " + w.Text
	}
	return ""
}

func notice(j job.Job, end store.Finish) (string, string) {
	title := fmt.Sprintf("loopgate: %s job %d %s", j.Loop.Name, j.ID, end.State)
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
