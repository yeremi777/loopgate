package verify

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestChangesListsBranchDiffAgainstDefaultBranch(t *testing.T) {
	branch, seed := newBranch(t)
	write(t, filepath.Join(branch, "a.go"), "changed")
	write(t, filepath.Join(branch, ".github/ci.yml"), "new")
	write(t, filepath.Join(branch, "d.go"), "renamed")
	git(t, branch, "rm", "-q", "b_test.go", "c.go")
	git(t, branch, "add", "-A")
	git(t, branch, "commit", "-q", "-m", "work")

	write(t, filepath.Join(seed, "other.go"), "someone else on main")
	git(t, seed, "add", "other.go")
	git(t, seed, "commit", "-q", "-m", "main moved")
	git(t, seed, "push", "-q", "origin", "main")
	git(t, branch, "fetch", "-q", "origin")

	got, err := Changes(context.Background(), branch, "main")
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{
		{Path: ".github/ci.yml", Kind: Added},
		{Path: "a.go", Kind: Modified},
		{Path: "b_test.go", Kind: Deleted},
		{Path: "c.go", Kind: Deleted},
		{Path: "d.go", Kind: Added},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestProtectedMatchesDefaultsAndLoopGlobs(t *testing.T) {
	changes := []Change{
		{Path: ".github/workflows/ci.yml", Kind: Modified},
		{Path: ".claude/settings.json", Kind: Added},
		{Path: "CLAUDE.md", Kind: Modified},
		{Path: "pkg/AGENTS.md", Kind: Deleted},
		{Path: ".golangci.yml", Kind: Modified},
		{Path: "internal/a.go", Kind: Modified},
		{Path: "docs/CLAUDE.md.bak", Kind: Added},
	}
	got := Protected(changes, []string{".golangci.yml"})
	want := []string{".github/workflows/ci.yml", ".claude/settings.json", "CLAUDE.md", "pkg/AGENTS.md", ".golangci.yml"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := Protected([]Change{{Path: "internal/a.go", Kind: Modified}}, nil); got != nil {
		t.Fatalf("got %q, want none", got)
	}
}

func TestTestChangesReportsChangedAndDeletedTestsOnly(t *testing.T) {
	changes := []Change{
		{Path: "a_test.go", Kind: Modified},
		{Path: "internal/x/b_test.go", Kind: Deleted},
		{Path: "internal/x/new_test.go", Kind: Added},
		{Path: "internal/x/b.go", Kind: Modified},
		{Path: "web/c.test.ts", Kind: Modified},
	}
	got := TestChanges(changes, []string{"**/*_test.go", "web/*.test.ts"})
	want := []Change{
		{Path: "a_test.go", Kind: Modified},
		{Path: "internal/x/b_test.go", Kind: Deleted},
		{Path: "web/c.test.ts", Kind: Modified},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// newBranch returns a clone on a work branch off origin/main, and a second
// clone that can move origin's main.
func newBranch(t *testing.T) (branch, seed string) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	seed = filepath.Join(root, "seed")
	git(t, root, "clone", "-q", origin, seed)
	git(t, seed, "checkout", "-q", "-b", "main")
	for _, f := range []string{"a.go", "b_test.go", "c.go"} {
		write(t, filepath.Join(seed, f), f)
	}
	git(t, seed, "add", "-A")
	git(t, seed, "commit", "-q", "-m", "base")
	git(t, seed, "push", "-q", "origin", "main")
	branch = filepath.Join(root, "branch")
	git(t, root, "clone", "-q", origin, branch)
	git(t, branch, "checkout", "-q", "-b", "loop/x-1")
	return branch, seed
}
