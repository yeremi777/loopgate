package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestWorktreeComesFromOriginDefaultBranchAndCarriesSpec(t *testing.T) {
	checkout, seed := cloneWithOrigin(t)
	commitFile(t, seed, "README", "v2")
	gitRun(t, seed, "push", "-q", "origin", "trunk")
	writeFile(t, filepath.Join(checkout, "README"), "dirty")
	writeFile(t, filepath.Join(checkout, "notes.txt"), "untracked")
	writeFile(t, filepath.Join(checkout, "docs/specs/x.md"), "spec")

	wt := filepath.Join(t.TempDir(), "wt")
	created, err := CreateWorktree(context.Background(), Worktree{Checkout: checkout, Path: wt, Branch: "loop/fix-42", Spec: "docs/specs/x.md"})
	if err != nil {
		t.Fatal(err)
	}
	if created.DefaultBranch != "trunk" || created.Base != gitOut(t, checkout, "rev-parse", "origin/trunk") {
		t.Fatalf("created %+v, want trunk at origin's head", created)
	}
	if got := readFile(t, filepath.Join(wt, "README")); got != "v2" {
		t.Fatalf("worktree README %q, want origin's v2", got)
	}
	if _, err := os.Stat(filepath.Join(wt, "notes.txt")); !os.IsNotExist(err) {
		t.Fatal("untracked file of the checkout leaked into the worktree")
	}
	if got := readFile(t, filepath.Join(wt, "docs/specs/x.md")); got != "spec" {
		t.Fatalf("spec in worktree %q", got)
	}
	if got := gitOut(t, wt, "branch", "--show-current"); got != "loop/fix-42" {
		t.Fatalf("worktree branch %q", got)
	}
	if got := readFile(t, filepath.Join(checkout, "README")); got != "dirty" {
		t.Fatalf("checkout README changed to %q", got)
	}
	if got := gitOut(t, wt, "status", "--porcelain"); got != "" {
		t.Fatalf("worktree not clean after creation: %q", got)
	}
	if got := gitOut(t, wt, "log", "-1", "--format=%s"); got != "add spec docs/specs/x.md" {
		t.Fatalf("last commit %q, want the spec commit", got)
	}
}

func TestWorktreeAddsNoCommitForSpecAlreadyOnDefaultBranch(t *testing.T) {
	checkout, seed := cloneWithOrigin(t)
	commitFile(t, seed, "docs/specs/x.md", "spec")
	gitRun(t, seed, "push", "-q", "origin", "trunk")
	gitRun(t, checkout, "pull", "-q")
	wt := filepath.Join(t.TempDir(), "wt")
	if _, err := CreateWorktree(context.Background(), Worktree{Checkout: checkout, Path: wt, Branch: "loop/fix-1", Spec: "docs/specs/x.md"}); err != nil {
		t.Fatal(err)
	}
	if got := gitOut(t, wt, "log", "-1", "--format=%s"); got != "spec" {
		t.Fatalf("last commit %q, want origin's head", got)
	}
}

func TestWorktreeWithoutSpec(t *testing.T) {
	checkout, _ := cloneWithOrigin(t)
	wt := filepath.Join(t.TempDir(), "wt")
	if _, err := CreateWorktree(context.Background(), Worktree{Checkout: checkout, Path: wt, Branch: "loop/deps-1"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(wt, "README")); got != "v1" {
		t.Fatalf("README %q", got)
	}
}

func TestWorktreeRefusesSpecOutsideCheckout(t *testing.T) {
	checkout, _ := cloneWithOrigin(t)
	for _, spec := range []string{"../secret.md", "/etc/passwd", "docs/specs/missing.md"} {
		wt := filepath.Join(t.TempDir(), "wt")
		if _, err := CreateWorktree(context.Background(), Worktree{Checkout: checkout, Path: wt, Branch: "loop/x-1", Spec: spec}); err == nil {
			t.Fatalf("spec %q accepted", spec)
		}
	}
}

func TestWorktreeNeedsOriginHead(t *testing.T) {
	checkout, _ := cloneWithOrigin(t)
	gitRun(t, checkout, "remote", "set-head", "origin", "--delete")
	_, err := CreateWorktree(context.Background(), Worktree{Checkout: checkout, Path: filepath.Join(t.TempDir(), "wt"), Branch: "loop/x-1"})
	if err == nil || !strings.Contains(err.Error(), "git remote set-head origin --auto") {
		t.Fatalf("want error naming git remote set-head origin --auto, got %v", err)
	}
}

// cloneWithOrigin returns a checkout and a second clone that can push new
// commits to their shared bare origin, whose default branch is trunk.
func cloneWithOrigin(t *testing.T) (checkout, seed string) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	gitRun(t, root, "init", "-q", "--bare", "-b", "trunk", origin)
	seed = filepath.Join(root, "seed")
	gitRun(t, root, "clone", "-q", origin, seed)
	gitRun(t, seed, "checkout", "-q", "-b", "trunk")
	commitFile(t, seed, "README", "v1")
	gitRun(t, seed, "push", "-q", "origin", "trunk")
	checkout = filepath.Join(root, "checkout")
	gitRun(t, root, "clone", "-q", origin, checkout)
	return checkout, seed
}

func commitFile(t *testing.T, repo, name, content string) {
	t.Helper()
	writeFile(t, filepath.Join(repo, name), content)
	gitRun(t, repo, "add", name)
	gitRun(t, repo, "commit", "-q", "-m", content)
}

func gitRun(t *testing.T, dir string, args ...string) { t.Helper(); gitOut(t, dir, args...) }

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestWorktreeReusesThePathOfAWorktreeWhoseDirectoryIsGone(t *testing.T) {
	checkout, _ := cloneWithOrigin(t)
	wt := filepath.Join(realDir(t), "jobs", "1", "worktree")
	if _, err := CreateWorktree(context.Background(), Worktree{Checkout: checkout, Path: wt, Branch: "loop/x-1"}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(wt)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(wt), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateWorktree(context.Background(), Worktree{Checkout: checkout, Path: wt, Branch: "loop/x-2"}); err != nil {
		t.Fatalf("new job at the path of a deleted one: %v", err)
	}
}

func TestGitIgnoresHooksAndFsmonitorPlantedInTheRepo(t *testing.T) {
	checkout, _ := cloneWithOrigin(t)
	marker := filepath.Join(t.TempDir(), "ran")
	hook := "#!/bin/sh\ntouch " + marker + "\n"
	writeFile(t, filepath.Join(checkout, ".git/hooks/post-checkout"), hook)
	if err := os.Chmod(filepath.Join(checkout, ".git/hooks/post-checkout"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, checkout, "config", "core.fsmonitor", "touch "+marker+" #")
	if _, err := Git(context.Background(), time.Minute, checkout, "status", "--porcelain"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateWorktree(context.Background(), Worktree{Checkout: checkout, Path: filepath.Join(t.TempDir(), "wt"), Branch: "loop/x-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("loopd's git ran a hook or fsmonitor planted in the repo")
	}
}

func TestGitTimeoutKeepsItsCause(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Git(ctx, time.Minute, t.TempDir(), "status"); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled in %v", err)
	}
}

func TestGitGuardNamesTheRepositoryFilesAStageMustNotWrite(t *testing.T) {
	checkout, _ := cloneWithOrigin(t)
	wt := filepath.Join(realDir(t), "wt")
	if _, err := CreateWorktree(context.Background(), Worktree{Checkout: checkout, Path: wt, Branch: "loop/x-1"}); err != nil {
		t.Fatal(err)
	}
	got, err := GitGuard(wt)
	if err != nil {
		t.Fatal(err)
	}
	common, _ := filepath.EvalSymlinks(filepath.Join(checkout, ".git"))
	for _, want := range []string{filepath.Join(common, "config"), filepath.Join(common, "hooks"), filepath.Join(common, "info"), filepath.Join(wt, ".git")} {
		if !slices.Contains(got, want) {
			t.Fatalf("guard %q lacks %s", got, want)
		}
	}
}
