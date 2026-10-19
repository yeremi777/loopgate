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

// Created is a new worktree's default branch and the commit it was cut
// from; that commit is the base every branch diff of the Job uses.
type Created struct {
	DefaultBranch string
	Base          string
}

// CreateWorktree fetches origin, adds the worktree on a new branch from
// origin's default branch, and commits the Spec onto it when the branch lacks it.
func CreateWorktree(ctx context.Context, w Worktree) (Created, error) {
	var spec []byte
	if w.Spec != "" {
		if !filepath.IsLocal(w.Spec) {
			return Created{}, fmt.Errorf("spec %q must be a path inside the checkout", w.Spec)
		}
		data, err := os.ReadFile(filepath.Join(w.Checkout, w.Spec))
		if err != nil {
			return Created{}, fmt.Errorf("read spec: %w", err)
		}
		spec = data
	}

	if _, err := Git(ctx, 2*time.Minute, w.Checkout, "fetch", "--quiet", "origin"); err != nil {
		return Created{}, err
	}
	ref, err := Git(ctx, 30*time.Second, w.Checkout, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return Created{}, fmt.Errorf("find origin's default branch (run `git remote set-head origin --auto` in %s): %w", w.Checkout, err)
	}
	base, err := Git(ctx, 30*time.Second, w.Checkout, "rev-parse", ref)
	if err != nil {
		return Created{}, err
	}
	// A worktree whose directory was deleted, as when ~/.loopd is wiped and job
	// ids start again, stays registered and blocks its path until pruned.
	if _, err := Git(ctx, 30*time.Second, w.Checkout, "worktree", "prune"); err != nil {
		return Created{}, err
	}
	if _, err := Git(ctx, time.Minute, w.Checkout, "worktree", "add", "--quiet", "-b", w.Branch, w.Path, base); err != nil {
		return Created{}, err
	}

	if spec != nil {
		if err := commitSpec(ctx, w, spec); err != nil {
			return Created{}, err
		}
	}
	return Created{DefaultBranch: strings.TrimPrefix(ref, "origin/"), Base: base}, nil
}

func commitSpec(ctx context.Context, w Worktree, spec []byte) error {
	dst := filepath.Join(w.Path, w.Spec)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, spec, 0o644); err != nil {
		return err
	}
	changed, err := Git(ctx, 30*time.Second, w.Path, "status", "--porcelain", "--", w.Spec)
	if err != nil || changed == "" {
		return err
	}
	if _, err := Git(ctx, 30*time.Second, w.Path, "add", "--", w.Spec); err != nil {
		return err
	}
	_, err = Git(ctx, 30*time.Second, w.Path, "commit", "--quiet", "-m", "add spec "+w.Spec, "--", w.Spec)
	return err
}

// GitGuard lists the repository files that a stage in worktree must never
// write: config, hooks and info in the shared git directory, the worktree's
// own config and links, and its .git pointer. A stage still writes objects,
// refs and the index, which commits need.
func GitGuard(worktree string) ([]string, error) {
	dotgit := filepath.Join(worktree, ".git")
	if info, err := os.Stat(dotgit); err == nil && info.IsDir() {
		return []string{filepath.Join(dotgit, "config"), filepath.Join(dotgit, "hooks"), filepath.Join(dotgit, "info")}, nil
	}
	data, err := os.ReadFile(dotgit)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dotgit, err)
	}
	admin, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !ok {
		return nil, fmt.Errorf("%s is not a worktree pointer", dotgit)
	}
	rel, err := os.ReadFile(filepath.Join(admin, "commondir"))
	if err != nil {
		return nil, err
	}
	common := filepath.Clean(filepath.Join(admin, strings.TrimSpace(string(rel))))
	if real, err := filepath.EvalSymlinks(common); err == nil {
		common = real
	}
	return []string{
		filepath.Join(common, "config"),
		filepath.Join(common, "hooks"),
		filepath.Join(common, "info"),
		filepath.Join(admin, "config.worktree"),
		filepath.Join(admin, "commondir"),
		filepath.Join(admin, "gitdir"),
		dotgit,
	}, nil
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
