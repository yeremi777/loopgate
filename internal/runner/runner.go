// Package runner runs one Claude Code stage in a Job worktree.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type Status string

const (
	StatusDone    Status = "done"
	StatusBlocked Status = "blocked"
	StatusRevise  Status = "revise"
)

// Result is the stage's structured report.
type Result struct {
	Status   Status
	Question string
	Summary  string
}

// Runner starts claude with Environ filtered to the allowlist.
type Runner struct {
	Claude  string
	Environ []string
}

type Stage struct {
	Dir           string
	Prompt        string
	Timeout       time.Duration
	DefaultBranch string
	Network       []string
}

const resultSchema = `{"type":"object","properties":{` +
	`"status":{"type":"string","enum":["done","blocked","revise"]},` +
	`"question":{"type":"string"},` +
	`"summary":{"type":"string"}},` +
	`"required":["status","summary"]}`

// envAllowlist holds what claude, git and ssh need; everything else stays in loopd.
var envAllowlist = []string{
	"HOME", "USER", "LOGNAME", "PATH", "SHELL", "TMPDIR",
	"LANG", "LC_ALL", "LC_CTYPE", "TERM", "SSH_AUTH_SOCK",
}

// Run runs one stage and returns its report. The stage's whole process group
// dies when the stage ends, times out, or ctx is cancelled.
func (r Runner) Run(ctx context.Context, s Stage) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	settings, err := settingsJSON(s.DefaultBranch, s.Network)
	if err != nil {
		return Result{}, err
	}
	cmd := r.command(ctx, s.Dir, r.Claude,
		"-p",
		"--output-format", "json",
		"--permission-mode", "auto",
		"--permission-prompts", "none",
		"--json-schema", resultSchema,
		"--settings", settings,
	)
	cmd.Stdin = strings.NewReader(s.Prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := runGroup(ctx, cmd); err != nil {
		return Result{}, fmt.Errorf("claude: %w: %s", err, tail(stderr.String()))
	}
	return parseResult(stdout.Bytes())
}

func parseResult(data []byte) (Result, error) {
	var out struct {
		IsError    bool   `json:"is_error"`
		Subtype    string `json:"subtype"`
		Structured *struct {
			Status   Status `json:"status"`
			Question string `json:"question"`
			Summary  string `json:"summary"`
		} `json:"structured_output"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return Result{}, fmt.Errorf("claude output: %w: %s", err, tail(string(data)))
	}
	if out.IsError {
		return Result{}, fmt.Errorf("claude reported error: %s", out.Subtype)
	}
	if out.Structured == nil {
		return Result{}, errors.New("claude output has no structured_output")
	}
	res := Result{Status: out.Structured.Status, Question: out.Structured.Question, Summary: out.Structured.Summary}
	switch {
	case res.Status != StatusDone && res.Status != StatusBlocked && res.Status != StatusRevise:
		return Result{}, fmt.Errorf("claude reported unknown status %q", res.Status)
	case res.Status == StatusBlocked && res.Question == "":
		return Result{}, errors.New("claude reported blocked without a question")
	}
	return res, nil
}

// settingsJSON enables the sandbox, opens only the network domains given, and denies the git and gh commands that
// would merge, force-push, push to the default branch, or mark a PR ready.
// Deny rules match command prefixes only; loopd checks the PR after land.
func settingsJSON(defaultBranch string, network []string) (string, error) {
	if defaultBranch == "" {
		return "", errors.New("stage needs the default branch")
	}
	var settings struct {
		Sandbox struct {
			Enabled                  bool `json:"enabled"`
			AutoAllowBashIfSandboxed bool `json:"autoAllowBashIfSandboxed"`
			Network                  struct {
				AllowedDomains []string `json:"allowedDomains"`
			} `json:"network"`
		} `json:"sandbox"`
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	settings.Sandbox.Enabled = true
	settings.Sandbox.AutoAllowBashIfSandboxed = true
	settings.Sandbox.Network.AllowedDomains = append([]string{}, network...)
	settings.Permissions.Deny = []string{
		"Bash(gh pr merge:*)",
		"Bash(gh pr ready:*)",
		"Bash(git merge:*)",
		"Bash(git push --force:*)",
		"Bash(git push --force-with-lease:*)",
		"Bash(git push -f:*)",
		"Bash(git push origin " + defaultBranch + ":*)",
		"Bash(git push origin HEAD:" + defaultBranch + ":*)",
	}
	data, err := json.Marshal(settings)
	return string(data), err
}

// command builds a child with the allowlisted env that runGroup runs in its own process group.
func (r Runner) command(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = filterEnv(r.Environ)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

// runGroup runs cmd and then kills its whole process group, so no grandchild
// outlives it. A stop by ctx is reported as ctx's error.
func runGroup(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	err := cmd.Wait()
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("stopped: %w", ctxErr)
	}
	return err
}

func filterEnv(environ []string) []string {
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		for _, allowed := range envAllowlist {
			if name == allowed {
				out = append(out, kv)
			}
		}
	}
	return out
}

func tail(s string) string {
	const max = 2000
	s = strings.TrimSpace(s)
	if len(s) > max {
		return "..." + s[len(s)-max:]
	}
	return s
}
