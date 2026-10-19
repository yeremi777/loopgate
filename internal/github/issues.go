package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yeremi777/loopgate/internal/job"
)

// Issues reads and answers a repo's issues with gh.
type Issues struct{ GH string }

// List returns the open issues that carry label, lowest number first.
func (i Issues) List(ctx context.Context, repo, label string) ([]job.Issue, error) {
	out, err := i.gh(ctx, "", "issue", "list", "-R", repo, "--label", label, "--state", "open", "--limit", "100", "--json", "number,title,body")
	if err != nil {
		return nil, err
	}
	var issues []job.Issue
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("gh issue list output: %w", err)
	}
	slices.SortFunc(issues, func(a, b job.Issue) int { return a.Number - b.Number })
	return issues, nil
}

func (i Issues) Comment(ctx context.Context, repo string, number int, body string) error {
	_, err := i.gh(ctx, body, "issue", "comment", strconv.Itoa(number), "-R", repo, "--body-file", "-")
	return err
}

// Label adds label to the issue, creating the label first when the repo lacks it.
func (i Issues) Label(ctx context.Context, repo string, number int, label string) error {
	if _, err := i.gh(ctx, "", "label", "create", label, "-R", repo, "--force", "--description", "loopgate needs a human decision before it can build this"); err != nil {
		return err
	}
	_, err := i.gh(ctx, "", "issue", "edit", strconv.Itoa(number), "-R", repo, "--add-label", label)
	return err
}

func (i Issues) gh(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, i.GH, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh %s %s: %w: %s", args[0], args[1], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
