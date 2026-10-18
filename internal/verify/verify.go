// Package verify decides whether a Job's tree may ship.
package verify

import (
	"context"
	"errors"
	"time"

	"github.com/yeremi777/loopgate/internal/runner"
)

// Outcome is the Verify gate's verdict on one tree. Head is the commit it judged.
type Outcome struct {
	Passed bool
	Head   string
	Output string
}

// Gate runs command in dir and passes only a clean, committed tree whose command exits 0.
func Gate(ctx context.Context, r runner.Runner, dir, command string, timeout time.Duration) (Outcome, error) {
	return Outcome{}, errors.New("not implemented")
}

// Change is one path that the branch changed since it left the default branch.
type Change struct {
	Path    string
	Deleted bool
}

func Changes(ctx context.Context, dir, defaultBranch string) ([]Change, error) {
	return nil, errors.New("not implemented")
}

// Protected returns the changed paths that match the default protected globs or globs.
func Protected(changes []Change, globs []string) []string { return nil }

// TestChanges returns the changes to paths that match globs.
func TestChanges(changes []Change, globs []string) []Change { return nil }
