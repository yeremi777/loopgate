package runner

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

type ShellResult struct {
	ExitCode int
	Output   string
}

// Shell runs script with sh in dir under the same env and process-group rules as a stage.
// A non-zero exit is a result, not an error.
func (r Runner) Shell(ctx context.Context, dir, script string, timeout time.Duration) (ShellResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := r.command(ctx, dir, "/bin/sh", "-c", script)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := runGroup(ctx, cmd)
	var exit *exec.ExitError
	if errors.As(err, &exit) && ctx.Err() == nil {
		return ShellResult{ExitCode: exit.ExitCode(), Output: out.String()}, nil
	}
	if err != nil {
		return ShellResult{}, err
	}
	return ShellResult{Output: out.String()}, nil
}
