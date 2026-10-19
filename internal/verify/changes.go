package verify

import (
	"context"
	"fmt"
	"path"
	"slices"
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

// Changes lists the branch's changes since base, the commit the Job started
// from. A rename shows as a deletion and an addition. Paths are read with -z,
// so a quoted or non-ASCII name is matched as it is.
func Changes(ctx context.Context, dir, base string) ([]Change, error) {
	out, err := runner.Git(ctx, 30*time.Second, dir, "diff", "-z", "--name-status", "--no-renames", base+"...HEAD")
	if err != nil {
		return nil, err
	}
	fields := splitNUL(out)
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("git diff output has an odd field count")
	}
	var changes []Change
	for i := 0; i < len(fields); i += 2 {
		kind := Modified
		switch fields[i] {
		case "A":
			kind = Added
		case "D":
			kind = Deleted
		}
		changes = append(changes, Change{Path: fields[i+1], Kind: kind})
	}
	return changes, nil
}

// Touched returns the protected paths that differ from base, in commits or in
// the working tree, untracked and ignored files included: a stage that leaves
// .claude/settings.local.json behind would steer the stages after it.
func Touched(ctx context.Context, dir, base string, globs []string) ([]string, error) {
	committed, err := Changes(ctx, dir, base)
	if err != nil {
		return nil, err
	}
	status, err := runner.Git(ctx, 30*time.Second, dir, "status", "-z", "--porcelain=v1", "--no-renames", "--ignored", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var all []Change
	for _, c := range committed {
		seen[c.Path] = true
		all = append(all, c)
	}
	for _, entry := range splitNUL(status) {
		if len(entry) > 3 && !seen[entry[3:]] {
			seen[entry[3:]] = true
			all = append(all, Change{Path: entry[3:], Kind: Modified})
		}
	}
	hits := Protected(all, globs)
	slices.Sort(hits)
	return hits, nil
}

func splitNUL(s string) []string {
	var out []string
	for _, f := range strings.Split(s, "\x00") {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
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
