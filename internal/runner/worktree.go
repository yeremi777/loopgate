package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Worktree describes a Job worktree cut from the default branch of Checkout's origin.
// Spec, when set, is a path relative to Checkout that is copied into the worktree.
type Worktree struct {
	Checkout string
	Path     string
	Branch   string
	Spec     string
}

// CreateWorktree fetches origin, adds the worktree on a new branch from
// origin's default branch, copies the Spec in, and returns the default branch.
func CreateWorktree(ctx context.Context, w Worktree) (string, error) {
	var spec []byte
	if w.Spec != "" {
		if !filepath.IsLocal(w.Spec) {
			return "", fmt.Errorf("spec %q must be a path inside the checkout", w.Spec)
		}
		data, err := os.ReadFile(filepath.Join(w.Checkout, w.Spec))
		if err != nil {
			return "", fmt.Errorf("read spec: %w", err)
		}
		spec = data
	}

	if _, err := git(ctx, 2*time.Minute, w.Checkout, "fetch", "--quiet", "origin"); err != nil {
		return "", err
	}
	ref, err := git(ctx, 30*time.Second, w.Checkout, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", fmt.Errorf("find origin's default branch (run `git remote set-head origin --auto` in %s): %w", w.Checkout, err)
	}
	if _, err := git(ctx, time.Minute, w.Checkout, "worktree", "add", "--quiet", "-b", w.Branch, w.Path, ref); err != nil {
		return "", err
	}

	if spec != nil {
		dst := filepath.Join(w.Path, w.Spec)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, spec, 0o644); err != nil {
			return "", err
		}
	}
	return strings.TrimPrefix(ref, "origin/"), nil
}

func git(ctx context.Context, timeout time.Duration, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", errors.New("git " + strings.Join(args, " ") + ": " + err.Error() + ": " + strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
