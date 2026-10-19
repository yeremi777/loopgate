package runner

import (
	"context"
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
// origin's default branch, commits the Spec onto it when the branch lacks it,
// and returns the default branch.
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

	if _, err := Git(ctx, 2*time.Minute, w.Checkout, "fetch", "--quiet", "origin"); err != nil {
		return "", err
	}
	ref, err := Git(ctx, 30*time.Second, w.Checkout, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", fmt.Errorf("find origin's default branch (run `git remote set-head origin --auto` in %s): %w", w.Checkout, err)
	}
	// A worktree whose directory was deleted, as when ~/.loopd is wiped and job
	// ids start again, stays registered and blocks its path until pruned.
	if _, err := Git(ctx, 30*time.Second, w.Checkout, "worktree", "prune"); err != nil {
		return "", err
	}
	if _, err := Git(ctx, time.Minute, w.Checkout, "worktree", "add", "--quiet", "-b", w.Branch, w.Path, ref); err != nil {
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
		changed, err := Git(ctx, 30*time.Second, w.Path, "status", "--porcelain", "--", w.Spec)
		if err != nil {
			return "", err
		}
		if changed != "" {
			if _, err := Git(ctx, 30*time.Second, w.Path, "add", "--", w.Spec); err != nil {
				return "", err
			}
			if _, err := Git(ctx, 30*time.Second, w.Path, "commit", "--quiet", "-m", "add spec "+w.Spec, "--", w.Spec); err != nil {
				return "", err
			}
		}
	}
	return strings.TrimPrefix(ref, "origin/"), nil
}

// Git runs git in dir for loopd itself, outside any stage, and returns its
// trimmed output. Hooks and fsmonitor are off, so nothing a stage planted in
// the repository runs with loopd's access.
func Git(ctx context.Context, timeout time.Duration, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), ctxErr)
	}
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
