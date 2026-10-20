package daemon

import (
	"fmt"
	"os"
	"time"

	"github.com/yeremi777/loopgate/internal/runner"
	"github.com/yeremi777/loopgate/internal/store"
)

// Removal is a removed Job as it was, and the line that says why its
// branch was kept, when it was.
type Removal struct {
	Job  store.Job `json:"job"`
	Kept string    `json:"kept,omitempty"`
}

// RemoveJob removes a Job that ended: its worktree from the checkout's
// worktrees, its directory, its row, and its local branch while the branch
// still points at the commit the Job ended at. It refuses a queued, running
// or manual Job.
func (d *Daemon) RemoveJob(id string) (Removal, error) {
	j, err := d.st.DeleteEnded(id)
	if err != nil {
		return Removal{}, err
	}
	removed := Removal{Job: j}
	checkout := d.checkoutOf(j)
	if checkout != "" && j.Worktree != "" {
		if err := runner.RemoveWorktree(d.ctx, checkout, j.Worktree); err != nil {
			return removed, fmt.Errorf("job %s is no longer listed, but its worktree %s stays: %w", id, j.Worktree, err)
		}
	}
	if err := os.RemoveAll(d.dirOf(j)); err != nil {
		return removed, fmt.Errorf("job %s is no longer listed, but its directory stays: %w", id, err)
	}
	if j.Branch == "" {
		return removed, nil
	}
	if checkout == "" {
		removed.Kept = fmt.Sprintf("kept branch %s: loop %s is removed and the job's worktree is gone, so its checkout is unknown", j.Branch, j.Loop)
		return removed, nil
	}
	at, err := runner.Git(d.ctx, 30*time.Second, checkout, "for-each-ref", "--format=%(objectname)", "refs/heads/"+j.Branch)
	if err != nil {
		return removed, fmt.Errorf("job %s is no longer listed, but its branch %s stays: %w", id, j.Branch, err)
	}
	if at == "" {
		return removed, nil
	}
	if j.EndCommit == "" || at != j.EndCommit {
		removed.Kept = "kept branch " + j.Branch + ": it has commits the job did not end at"
		return removed, nil
	}
	if _, err := runner.Git(d.ctx, 30*time.Second, checkout, "branch", "-D", j.Branch); err != nil {
		return removed, fmt.Errorf("job %s is no longer listed, but its branch %s stays: %w", id, j.Branch, err)
	}
	return removed, nil
}

// checkoutOf is the repository that holds j's worktree and branch: its
// Loop's checkout, or for a removed Loop the git directory that j's
// worktree points at; it is empty when neither is left.
func (d *Daemon) checkoutOf(j store.Job) string {
	d.mu.Lock()
	loop, ok := d.loops[j.Loop]
	d.mu.Unlock()
	if ok {
		return loop.Checkout
	}
	if j.Worktree == "" {
		return ""
	}
	common, err := runner.Git(d.ctx, 30*time.Second, j.Worktree, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return ""
	}
	return common
}
