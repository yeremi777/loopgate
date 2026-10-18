// Package verify decides whether a Job's tree may ship.
package verify

import (
	"context"
	"fmt"
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
	status, err := runner.Git(ctx, 30*time.Second, dir, "status", "--porcelain")
	if err != nil {
		return Outcome{}, err
	}
	if status != "" {
		return Outcome{Output: "uncommitted changes; commit every change before the gate:\n" + status}, nil
	}
	head, err := runner.Git(ctx, 30*time.Second, dir, "rev-parse", "HEAD")
	if err != nil {
		return Outcome{}, err
	}
	res, err := r.Shell(ctx, dir, command, timeout)
	if err != nil {
		return Outcome{}, fmt.Errorf("verify command: %w", err)
	}
	return Outcome{Passed: res.ExitCode == 0, Head: head, Output: res.Output}, nil
}
