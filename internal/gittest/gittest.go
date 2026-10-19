// Package gittest builds git repositories for tests.
package gittest

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Checkout clones a one-commit origin whose remote URL reads as url, so
// fetches reach the local origin while the URL names a GitHub repo.
func Checkout(t *testing.T, url string) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	Run(t, root, "init", "-q", "--bare", "-b", "main", origin)
	seed := filepath.Join(root, "seed")
	Run(t, root, "clone", "-q", origin, seed)
	Run(t, seed, "commit", "-q", "--allow-empty", "-m", "base")
	Run(t, seed, "push", "-q", "origin", "HEAD:main")
	checkout := filepath.Join(root, "checkout")
	Run(t, root, "clone", "-q", origin, checkout)
	Run(t, checkout, "remote", "set-url", "origin", url)
	Run(t, checkout, "config", "url."+origin+".insteadOf", url)
	return checkout
}

// Run runs git in dir as a fixed test identity and fails t on error.
func Run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
