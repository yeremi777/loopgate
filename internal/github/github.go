// Package github is loopd's only path to GitHub, through the user's gh login.
package github

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/yeremi777/loopgate/internal/job"
	"github.com/yeremi777/loopgate/internal/runner"
)

// Publisher pushes a Job branch and opens its draft PR with gh.
type Publisher struct{ GH string }

// Publish pushes HEAD to branch without force, so a remote branch that HEAD
// does not extend is refused, then opens a draft PR into base.
func (p Publisher) Publish(ctx context.Context, dir, branch, base string, pr job.PR) (string, error) {
	if _, err := runner.Git(ctx, 2*time.Minute, dir, "push", "--quiet", "origin", "HEAD:refs/heads/"+branch); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.GH, "pr", "create", "--draft", "--base", base, "--head", branch, "--title", pr.Title, "--body-file", "-")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(pr.Body)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh pr create: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	return lines[len(lines)-1], nil
}
