package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/yeremi777/loopgate/internal/config"
	"github.com/yeremi777/loopgate/internal/runner"
)

// checkCheckout refuses a checkout that no Job could start from: one that is
// not a clone's top level, has no origin HEAD to cut worktrees from, or whose
// origin is not the GitHub repo the Loop polls.
func checkCheckout(ctx context.Context, loop config.Loop) error {
	top, err := runner.Git(ctx, 30*time.Second, loop.Checkout, "rev-parse", "--show-toplevel")
	if err != nil || !samePath(top, loop.Checkout) {
		return fmt.Errorf("checkout %s is not the top level of a git work tree", loop.Checkout)
	}
	if _, err := runner.Git(ctx, 30*time.Second, loop.Checkout, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err != nil {
		return fmt.Errorf("checkout %s has no origin HEAD; run `git -C %s remote set-head origin --auto`", loop.Checkout, loop.Checkout)
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

func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
