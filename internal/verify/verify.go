// Package verify decides whether a Job's tree may ship.
package verify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yeremi777/loopgate/internal/runner"
)

// Outcome is the Verify gate's verdict on one tree. Head is the commit it judged.
type Outcome struct {
	Passed bool
	Head   string
	Output string
}

// Gate runs command in dir and passes only a clean, committed tree whose
// command exits 0. The command may not write guard, the repository files
// that loopd's own git trusts.
func Gate(ctx context.Context, r runner.Runner, dir, command string, timeout time.Duration, guard []string) (Outcome, error) {
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
	res, err := r.Shell(ctx, dir, command, timeout, guard)
	if err != nil {
		return Outcome{}, fmt.Errorf("verify command: %w", err)
	}
	return Outcome{Passed: res.ExitCode == 0, Head: head, Output: res.Output}, nil
}

// ShipCheck passes only a clean tree whose HEAD descends from gated and
// differs from it in unprotected Markdown files alone.
func ShipCheck(ctx context.Context, dir, gated string, protected []string) error {
	status, err := runner.Git(ctx, 30*time.Second, dir, "status", "--porcelain")
	if err != nil {
		return err
	}
	if status != "" {
		return errors.New("tree has uncommitted changes after the gate:\n" + status)
	}
	if _, err := runner.Git(ctx, 30*time.Second, dir, "merge-base", "--is-ancestor", gated, "HEAD"); err != nil {
		return fmt.Errorf("HEAD no longer contains the gated commit %s", gated)
	}
	changed, err := runner.Git(ctx, 30*time.Second, dir, "diff", "-z", "--name-only", "--no-renames", gated, "HEAD")
	if err != nil {
		return err
	}
	var code []string
	var changes []Change
	for _, f := range strings.Split(changed, "\x00") {
		if f == "" {
			continue
		}
		changes = append(changes, Change{Path: f, Kind: Modified})
		if !strings.HasSuffix(f, ".md") {
			code = append(code, f)
		}
	}
	if code != nil {
		return errors.New("files other than Markdown changed after the gate: " + strings.Join(code, ", "))
	}
	if hits := Protected(changes, protected); hits != nil {
		return errors.New("protected files changed after the gate: " + strings.Join(hits, ", "))
	}
	return nil
}
