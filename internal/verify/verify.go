// Package verify decides whether a Job's tree may ship.
package verify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yeremi777/loopgate/internal/config"
	"github.com/yeremi777/loopgate/internal/runner"
)

// Outcome is the Verify gate's verdict on one tree. Head is the commit it judged.
type Outcome struct {
	Passed bool
	Head   string
	Output string
}

// Gate runs the Loop's setup command, when set, then its verify command in
// dir, and passes only a clean, committed tree that setup leaves unchanged and
// whose commands both exit 0. Neither command may write guard, the repository
// files that loopd's own git trusts. started gets each command as it starts.
func Gate(ctx context.Context, r runner.Runner, dir string, loop config.Loop, timeout time.Duration, guard []string, started func(step string)) (Outcome, error) {
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
	var refused string
	if loop.Setup != "" {
		started("setup: " + loop.Setup)
		setup, hosts, err := r.Setup(ctx, dir, loop.Setup, timeout, guard, loop.Network)
		if hosts != nil {
			refused = "loopgate refused hosts outside the loop's network list: " + strings.Join(hosts, ", ")
		}
		if err != nil {
			return Outcome{}, fmt.Errorf("setup command: %w%s", err, inline(refused))
		}
		if setup.ExitCode != 0 {
			return Outcome{Output: fmt.Sprintf("setup command exited %d:\n%s%s", setup.ExitCode, setup.Output, block(refused))}, nil
		}
		status, err := runner.Git(ctx, 30*time.Second, dir, "status", "--porcelain")
		if err != nil {
			return Outcome{}, err
		}
		if status != "" {
			return Outcome{Output: "setup command changed files that are tracked or not ignored:\n" + status + "\nsetup command output:\n" + setup.Output + block(refused)}, nil
		}
	}
	started("verify: " + loop.Verify)
	res, err := r.Shell(ctx, dir, loop.Verify, timeout, guard)
	if err != nil {
		return Outcome{}, fmt.Errorf("verify command: %w%s", err, inline(refused))
	}
	if res.ExitCode != 0 {
		return Outcome{Head: head, Output: fmt.Sprintf("verify command exited %d:\n%s%s", res.ExitCode, res.Output, block(refused))}, nil
	}
	return Outcome{Passed: true, Head: head, Output: res.Output}, nil
}

// inline and block place a note at the end of an error line or of command output.
func inline(note string) string {
	if note == "" {
		return ""
	}
	return "; " + note
}

func block(note string) string {
	if note == "" {
		return ""
	}
	return "\n" + note + "\n"
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
