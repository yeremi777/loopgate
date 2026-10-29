package daemon

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
	"github.com/yeremi777/loopgate/internal/verify"
)

// trial runs the Loop's Verify gate, bounded by its timeout, on a fresh
// detached worktree of origin's default branch, and removes the worktree.
// It ends early when ctx ends or loopd closes.
func (d *Daemon) trial(ctx context.Context, loop config.Loop) (err error) {
	ctx, cancel := context.WithTimeout(ctx, loop.Bounds.Timeout)
	defer cancel()
	defer context.AfterFunc(d.ctx, cancel)()
	trials := filepath.Join(d.home, "trials")
	if err := os.MkdirAll(trials, 0o700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(trials, loop.Name+"-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	wt := filepath.Join(dir, "worktree")
	defer func() {
		err = errors.Join(err, runner.RemoveWorktree(context.WithoutCancel(ctx), loop.Checkout, wt))
	}()
	made, err := d.createWorktree(ctx, runner.Worktree{Checkout: loop.Checkout, Path: wt})
	if err != nil {
		return fmt.Errorf("trial gate: %w", err)
	}
	gate, err := verify.Gate(ctx, d.deps.Pipeline.Shell, wt, dir, loop, loop.Bounds.Timeout, func(string) {})
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("trial gate on origin/%s ran past the loop's timeout of %s; loop %s not added: %w", made.DefaultBranch, loop.Bounds.Timeout, loop.Name, err)
	case ctx.Err() != nil:
		return fmt.Errorf("trial gate on origin/%s stopped; loop %s not added", made.DefaultBranch, loop.Name)
	case err != nil:
		return fmt.Errorf("trial gate on origin/%s: %w", made.DefaultBranch, err)
	case !gate.Passed:
		return fmt.Errorf("trial gate failed on origin/%s; loop %s not added:\n%s", made.DefaultBranch, loop.Name, gate.Output)
	}
	return nil
}

// checkCheckout refuses a checkout that no Job could start from: one that is
// not a clone's top level, has no origin HEAD to cut worktrees from, has no
// writable worktree folder, or whose origin is not the GitHub repo the Loop polls.
func checkCheckout(ctx context.Context, loop config.Loop) error {
	top, err := runner.Git(ctx, 30*time.Second, loop.Checkout, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("checkout %s is not a git work tree: %w", loop.Checkout, err)
	}
	if real, err := filepath.EvalSymlinks(loop.Checkout); err != nil || real != top {
		return fmt.Errorf("checkout %s is not the top level of a git work tree; that is %s", loop.Checkout, top)
	}
	if _, err := runner.Git(ctx, 30*time.Second, loop.Checkout, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err != nil {
		return fmt.Errorf("checkout %s has no origin HEAD; run `git -C %s remote set-head origin --auto`", loop.Checkout, loop.Checkout)
	}
	if err := checkWorktreeFolder(loop.Checkout); err != nil {
		return err
	}
	gh, ok := loop.Trigger.(config.GitHub)
	if !ok {
		return nil
	}
	url, err := runner.Git(ctx, 30*time.Second, loop.Checkout, "config", "--get", "remote.origin.url")
	if err != nil {
		return fmt.Errorf("checkout %s has no origin remote: %w", loop.Checkout, err)
	}
	if repo := repoOf(url); !strings.EqualFold(repo, gh.Repo) {
		return fmt.Errorf("checkout %s is a clone of %s, not of the loop's repo %s", loop.Checkout, repo, gh.Repo)
	}
	return nil
}

// checkWorktreeFolder refuses a checkout whose worktree folder, or its parent
// while the folder does not exist, cannot take a new entry; it leaves nothing behind.
func checkWorktreeFolder(checkout string) error {
	folder := checkout + "-worktrees"
	dir := folder
	if _, err := os.Stat(folder); errors.Is(err, os.ErrNotExist) {
		dir = filepath.Dir(checkout)
	}
	probe, err := os.MkdirTemp(dir, ".loopgate-")
	if err != nil {
		return fmt.Errorf("checkout %s has no writable worktree folder %s: %w; make %s writable", Tilde(checkout), Tilde(folder), err, Tilde(dir))
	}
	return os.Remove(probe)
}

// repoOf reads owner/repo from the last two path segments of a remote URL,
// in its https, ssh:// and scp-like git@host:owner/repo forms.
func repoOf(url string) string {
	path := strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git")
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == ':' })
	if len(parts) < 2 {
		return url
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
}
