package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yeremi777/loopgate/internal/runner"
	"github.com/yeremi777/loopgate/internal/store"
)

// ErrNotDone refuses try of a Job that did not end done.
var ErrNotDone = errors.New("only a done job can be tried")

// Tried is a done Job readied to run by hand and the paths, relative to its
// worktree, of the files copied into it.
type Tried struct {
	Job    store.Job `json:"job"`
	Copied []string  `json:"copied"`
}

// Try adds a deleted worktree of a done Job again from its branch, then
// copies into it each regular file of its Loop's checkout that the checkout's
// .worktreeinclude matches and git ignores, in the checkout and in the
// worktree. A done Job runs no more stages, so no model reads what Try copies.
func (d *Daemon) Try(id string) (Tried, error) {
	j, err := d.st.Job(id)
	if err != nil {
		return Tried{}, err
	}
	switch {
	case j.State == store.Done:
	case j.State == store.Queued || j.State == store.Running:
		return Tried{}, fmt.Errorf("job %s is %s: %w; wait for it to end, following it with `loopctl logs -f %s`", id, j.State, ErrNotDone, id)
	case j.State == store.Manual:
		return Tried{}, fmt.Errorf("job %s is %s: %w; to try it, hand it back with `loopctl handback %s` and try it once it is done", id, j.State, ErrNotDone, id)
	case j.Worktree == "":
		return Tried{}, fmt.Errorf("job %s is %s: %w; it never got a worktree, so to remove it, run `loopctl rm %s`", id, j.State, ErrNotDone, id)
	default:
		return Tried{}, fmt.Errorf("job %s is %s: %w; to work in it, run `loopctl takeover %s`", id, j.State, ErrNotDone, id)
	}
	d.mu.Lock()
	loop, ok := d.loops[j.Loop]
	d.mu.Unlock()
	if !ok {
		return Tried{}, fmt.Errorf("job %s: loop %s: %w; to try the job, add the loop again with `loopctl add <loop.yaml>`", id, j.Loop, ErrUnknownLoop)
	}
	if _, err := os.Stat(j.Worktree); errors.Is(err, os.ErrNotExist) {
		if err := d.restoreWorktree(j, loop.Checkout); err != nil {
			return Tried{}, err
		}
	} else if err != nil {
		return Tried{}, fmt.Errorf("job %s: %w", id, err)
	}
	files, err := d.included(loop.Checkout)
	if err != nil {
		return Tried{}, err
	}
	ignored, err := runner.Ignored(d.ctx, j.Worktree, files)
	if err != nil {
		return Tried{}, err
	}
	var unignored []string
	for _, f := range files {
		if !slices.Contains(ignored, f) {
			unignored = append(unignored, f)
		}
	}
	if err := d.refuseUnignored(j, loop.Checkout, unignored); err != nil {
		return Tried{}, err
	}
	root, err := os.OpenRoot(j.Worktree)
	if err != nil {
		return Tried{}, err
	}
	defer func() { _ = root.Close() }()
	for _, f := range files {
		if err := copyInto(root, filepath.Join(loop.Checkout, f), f); err != nil {
			return Tried{}, fmt.Errorf("copy %s into job %s's worktree: %w", f, id, err)
		}
	}
	return Tried{Job: j, Copied: files}, nil
}

// restoreWorktree adds j's deleted worktree again from its branch, and fails
// naming `loopctl rm` when the branch is gone too.
func (d *Daemon) restoreWorktree(j store.Job, checkout string) error {
	if _, err := runner.Git(d.ctx, 30*time.Second, checkout, "worktree", "prune"); err != nil {
		return err
	}
	at, err := runner.Git(d.ctx, 30*time.Second, checkout, "for-each-ref", "--format=%(objectname)", "refs/heads/"+j.Branch)
	if err != nil {
		return fmt.Errorf("job %s: look up its branch %s: %w", j.ID, j.Branch, err)
	}
	if at == "" {
		return fmt.Errorf("job %s: its worktree %s is gone and so is its branch %s; to remove the job, run `loopctl rm %s`", j.ID, Tilde(j.Worktree), j.Branch, j.ID)
	}
	if _, err := runner.Git(d.ctx, time.Minute, checkout, "worktree", "add", "--quiet", j.Worktree, j.Branch); err != nil {
		return fmt.Errorf("job %s: add its worktree %s again: %w", j.ID, Tilde(j.Worktree), err)
	}
	return nil
}

// refuseUnignored fails when j's branch tracks or does not ignore any of
// files, naming the remedy for each: a tracked file would be overwritten, and
// an untracked one could be committed.
func (d *Daemon) refuseUnignored(j store.Job, checkout string, files []string) error {
	if files == nil {
		return nil
	}
	listed, err := runner.Git(d.ctx, 30*time.Second, j.Worktree, append([]string{"--literal-pathspecs", "ls-files", "-z", "--"}, files...)...)
	if err != nil {
		return err
	}
	if tracked := strings.FieldsFunc(listed, func(r rune) bool { return r == 0 }); len(tracked) > 0 {
		return fmt.Errorf("job %s: its branch %s tracks %s, so a copy would overwrite committed content, and nothing was copied; to try the job, remove them from %s or from that branch", j.ID, j.Branch, strings.Join(tracked, ", "), Tilde(filepath.Join(checkout, ".worktreeinclude")))
	}
	exclude, err := runner.Git(d.ctx, 30*time.Second, checkout, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	return fmt.Errorf("job %s: its branch %s does not ignore %s, so a copy could be committed, and nothing was copied; to copy these files, add them to %s", j.ID, j.Branch, strings.Join(files, ", "), Tilde(exclude))
}

// included is the regular files of checkout, relative to it, that its
// .worktreeinclude matches and git ignores; none when it has no .worktreeinclude.
func (d *Daemon) included(checkout string) ([]string, error) {
	include := filepath.Join(checkout, ".worktreeinclude")
	if _, err := os.Stat(include); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	listed, err := runner.Git(d.ctx, time.Minute, checkout, "ls-files", "-z", "--others", "--ignored", "--exclude-from="+include)
	if err != nil {
		return nil, err
	}
	var regular []string
	for _, f := range strings.Split(listed, "\x00") {
		if info, err := os.Lstat(filepath.Join(checkout, f)); err == nil && f != "" && info.Mode().IsRegular() {
			regular = append(regular, f)
		}
	}
	return runner.Ignored(d.ctx, checkout, regular)
}

// copyInto writes the file at src to rel under root, with src's mode,
// creating its parent directories and replacing a regular file there.
func copyInto(root *os.Root, src, rel string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := root.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
		return err
	}
	if there, err := root.Lstat(rel); err == nil && !there.Mode().IsRegular() {
		return fmt.Errorf("%s in the worktree is not a regular file", rel)
	}
	return root.WriteFile(rel, data, info.Mode().Perm())
}
