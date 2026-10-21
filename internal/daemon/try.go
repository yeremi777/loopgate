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

// Tried is a done Job readied to run by hand, and the paths, relative to its
// worktree, of the files copied into it.
type Tried struct {
	Job    store.Job `json:"job"`
	Copied []string  `json:"copied"`
}

// Try copies into a done Job's own worktree each regular file of its Loop's
// checkout that the checkout's .worktreeinclude matches and git ignores, in
// the checkout and in the worktree. A done Job runs no more stages, so no
// model reads what Try copies.
func (d *Daemon) Try(id string) (Tried, error) {
	j, err := d.st.Job(id)
	if err != nil {
		return Tried{}, err
	}
	switch j.State {
	case store.Done:
	case store.Blocked, store.Failed, store.Exhausted:
		return Tried{}, fmt.Errorf("job %s is %s: %w; to work in it, run `loopctl takeover %s`", id, j.State, ErrNotDone, id)
	default:
		return Tried{}, fmt.Errorf("job %s is %s: %w", id, j.State, ErrNotDone)
	}
	d.mu.Lock()
	loop, ok := d.loops[j.Loop]
	d.mu.Unlock()
	if !ok {
		return Tried{}, fmt.Errorf("job %s: loop %s: %w; to try the job, add the loop again with `loopctl add <loop.yaml>`", id, j.Loop, ErrUnknownLoop)
	}
	if _, err := os.Stat(j.Worktree); errors.Is(err, os.ErrNotExist) {
		return Tried{}, fmt.Errorf("job %s: its worktree %s is gone; to remove the job, run `loopctl rm %s`", id, Tilde(j.Worktree), id)
	}
	files, err := d.included(loop.Checkout)
	if err != nil {
		return Tried{}, err
	}
	ignored, err := runner.Ignored(d.ctx, j.Worktree, files)
	if err != nil {
		return Tried{}, err
	}
	var tracked []string
	for _, f := range files {
		if !slices.Contains(ignored, f) {
			tracked = append(tracked, f)
		}
	}
	if tracked != nil {
		return Tried{}, fmt.Errorf("job %s: its branch %s does not ignore %s, so a copy could be committed, and nothing was copied; to copy these files, ignore them in that branch's .gitignore", id, j.Branch, strings.Join(tracked, ", "))
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
