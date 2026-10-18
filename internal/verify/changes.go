package verify

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/yeremi777/loopgate/internal/runner"
)

type ChangeKind string

const (
	Added    ChangeKind = "added"
	Modified ChangeKind = "modified"
	Deleted  ChangeKind = "deleted"
)

// Change is one path that the branch changed since it left the default branch.
type Change struct {
	Path string
	Kind ChangeKind
}

// defaultProtected guards the files that steer CI and Claude Code itself.
var defaultProtected = []string{".github/**", ".claude/**", "**/CLAUDE.md", "**/AGENTS.md"}

// Changes lists the branch's changes since its merge-base with origin's default branch.
// A rename shows as a deletion and an addition.
func Changes(ctx context.Context, dir, defaultBranch string) ([]Change, error) {
	out, err := runner.Git(ctx, 30*time.Second, dir, "diff", "--name-status", "--no-renames", "origin/"+defaultBranch+"...HEAD")
	if err != nil {
		return nil, err
	}
	var changes []Change
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		status, file, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("git diff line %q", line)
		}
		kind := Modified
		switch status {
		case "A":
			kind = Added
		case "D":
			kind = Deleted
		}
		changes = append(changes, Change{Path: file, Kind: kind})
	}
	return changes, nil
}

// Protected returns the changed paths that match the default protected globs or globs.
func Protected(changes []Change, globs []string) []string {
	all := append(append([]string{}, defaultProtected...), globs...)
	var hits []string
	for _, c := range changes {
		if matchAny(all, c.Path) {
			hits = append(hits, c.Path)
		}
	}
	return hits
}

// TestChanges returns the modified or deleted paths that match globs.
func TestChanges(changes []Change, globs []string) []Change {
	var hits []Change
	for _, c := range changes {
		if c.Kind != Added && matchAny(globs, c.Path) {
			hits = append(hits, c)
		}
	}
	return hits
}

func matchAny(globs []string, file string) bool {
	for _, g := range globs {
		if match(strings.Split(g, "/"), strings.Split(file, "/")) {
			return true
		}
	}
	return false
}

// match is path.Match per segment, where a ** segment matches zero or more segments.
func match(glob, file []string) bool {
	if len(glob) == 0 {
		return len(file) == 0
	}
	if glob[0] == "**" {
		for i := 0; i <= len(file); i++ {
			if match(glob[1:], file[i:]) {
				return true
			}
		}
		return false
	}
	if len(file) == 0 {
		return false
	}
	ok, err := path.Match(glob[0], file[0])
	return err == nil && ok && match(glob[1:], file[1:])
}
