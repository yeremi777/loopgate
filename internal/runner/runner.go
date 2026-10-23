// Package runner runs one Claude Code stage in a Job worktree.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/yeremi777/loopgate/internal/feed"
)

type Status string

const (
	StatusDone    Status = "done"
	StatusBlocked Status = "blocked"
	StatusRevise  Status = "revise"
)

// Result is the stage's structured report and what the stage cost. Try is
// the land stage's Test guide, empty when the stage wrote none.
type Result struct {
	Status   Status
	Question string
	Summary  string
	Try      string
	CostUSD  float64
}

// Runner starts claude with Environ filtered to the allowlist. Cache is the
// one directory outside the worktree that a stage may write: build caches.
type Runner struct {
	Claude   string
	Environ  []string
	Cache    string
	DenyRead []string
	GoProxy  string
}

// CredentialDirs are the places under home where logins and keys live; no
// stage or verify command may read them.
func CredentialDirs(home string) []string {
	var dirs []string
	for _, rel := range []string{".ssh", ".aws", ".config/gh", ".gnupg", ".netrc", ".npmrc", ".docker", ".kube", "Library/Keychains"} {
		dirs = append(dirs, filepath.Join(home, rel))
	}
	return dirs
}

type Stage struct {
	Dir       string
	Prompt    string
	Timeout   time.Duration
	Network   []string
	DenyWrite []string
	// Log, when set, gets each tool call and assistant text message as the
	// stage makes them.
	Log func(feed.Step)
}

const resultSchema = `{"type":"object","properties":{` +
	`"status":{"type":"string","enum":["done","blocked","revise"]},` +
	`"question":{"type":"string"},` +
	`"summary":{"type":"string"},` +
	`"try":{"type":"string","description":"Land only: the test guide in Markdown, at most 40 lines; empty when the branch adds nothing a human can run."}},` +
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

	if r.Cache == "" {
		return Result{}, errors.New("runner needs a cache directory")
	}
	settings, err := settingsJSON(s.Network, s.DenyWrite, r.DenyRead)
	if err != nil {
		return Result{}, err
	}
	cmd := r.command(ctx, s.Dir, r.Claude,
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "auto",
		"--permission-prompts", "none",
		"--json-schema", resultSchema,
		"--settings", settings,
		"--add-dir", r.Cache,
		"--setting-sources", "user",
		"--strict-mcp-config",
	)
	cmd.Env = r.childEnv()
	cmd.Stdin = strings.NewReader(s.Prompt)
	stream := &streamJSON{log: s.Log}
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = stream, &stderr
	if err := runGroup(ctx, cmd); err != nil {
		return Result{}, fmt.Errorf("claude: %w: %s", err, tail(stderr.String()))
	}
	stream.line(stream.buf)
	if stream.result == nil {
		return Result{}, fmt.Errorf("claude output has no result line: %s", tail(string(stream.last)))
	}
	return parseResult(stream.result)
}

// streamJSON reads claude's stream-json output one line at a time: it logs
// each tool call and text message, and keeps the final result line.
type streamJSON struct {
	log    func(feed.Step)
	buf    []byte
	last   []byte
	result []byte
}

func (s *streamJSON) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		s.line(s.buf[:i])
		s.buf = append(s.buf[:0], s.buf[i+1:]...)
	}
}

func (s *streamJSON) line(data []byte) {
	if len(bytes.TrimSpace(data)) == 0 {
		return
	}
	s.last = append(s.last[:0], data...)
	var ev struct {
		Type    string  `json:"type"`
		Parent  *string `json:"parent_tool_use_id"`
		Message struct {
			Content []struct {
				Type  string         `json:"type"`
				Text  string         `json:"text"`
				Name  string         `json:"name"`
				Input map[string]any `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return
	}
	switch ev.Type {
	case "result":
		s.result = bytes.Clone(data)
	case "assistant":
		if s.log == nil {
			return
		}
		sub := ev.Parent != nil
		for _, c := range ev.Message.Content {
			switch {
			case c.Type == "text" && firstLine(c.Text) != "":
				s.log(feed.Step{Text: firstLine(c.Text), Sub: sub})
			case c.Type == "tool_use" && c.Name != "StructuredOutput":
				s.log(feed.Step{Tool: c.Name, Text: toolArg(c.Input), Sub: sub})
			}
		}
	}
}

// toolArg is the input that says most about a tool call: the file, command,
// pattern, path, URL, skill or description it names.
func toolArg(input map[string]any) string {
	for _, key := range []string{"file_path", "command", "pattern", "path", "url", "skill", "description"} {
		if v, ok := input[key].(string); ok && firstLine(v) != "" {
			return firstLine(v)
		}
	}
	return ""
}

func firstLine(s string) string {
	for line := range strings.Lines(s) {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func parseResult(data []byte) (Result, error) {
	var out struct {
		IsError    bool    `json:"is_error"`
		Subtype    string  `json:"subtype"`
		CostUSD    float64 `json:"total_cost_usd"`
		Structured *struct {
			Status   Status `json:"status"`
			Question string `json:"question"`
			Summary  string `json:"summary"`
			Try      string `json:"try"`
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
	res := Result{Status: out.Structured.Status, Question: out.Structured.Question, Summary: out.Structured.Summary, Try: out.Structured.Try, CostUSD: out.CostUSD}
	switch {
	case res.Status != StatusDone && res.Status != StatusBlocked && res.Status != StatusRevise:
		return Result{}, fmt.Errorf("claude reported unknown status %q", res.Status)
	case res.Status == StatusBlocked && res.Question == "":
		return Result{}, errors.New("claude reported blocked without a question")
	}
	return res, nil
}

// settingsJSON enables the sandbox with no way to run a command outside it,
// opens only the network domains given, and denies gh, push and merge:
// loopd alone ships work. The flags beside it keep the worktree's own
// .claude settings and every MCP server out of the stage.
func settingsJSON(network, denyWrite, denyRead []string) (string, error) {
	var settings struct {
		Sandbox struct {
			Enabled                  bool `json:"enabled"`
			AutoAllowBashIfSandboxed bool `json:"autoAllowBashIfSandboxed"`
			AllowUnsandboxedCommands bool `json:"allowUnsandboxedCommands"`
			Network                  struct {
				AllowedDomains []string `json:"allowedDomains"`
			} `json:"network"`
			Filesystem struct {
				DenyWrite []string `json:"denyWrite"`
				DenyRead  []string `json:"denyRead"`
			} `json:"filesystem"`
		} `json:"sandbox"`
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	settings.Sandbox.Enabled = true
	settings.Sandbox.AutoAllowBashIfSandboxed = true
	settings.Sandbox.Network.AllowedDomains = append([]string{}, network...)
	settings.Sandbox.Filesystem.DenyWrite = append([]string{}, denyWrite...)
	settings.Sandbox.Filesystem.DenyRead = append([]string{}, denyRead...)
	// The sandbox confines Bash; these rules hold the same lines for the Read,
	// Edit and Write tools, which run outside it.
	settings.Permissions.Deny = []string{"Bash(gh:*)", "Bash(git push:*)", "Bash(git merge:*)"}
	for _, p := range denyRead {
		settings.Permissions.Deny = append(settings.Permissions.Deny, "Read(/"+p+")", "Read(/"+p+"/**)")
	}
	for _, p := range denyWrite {
		for _, tool := range []string{"Edit", "Write"} {
			settings.Permissions.Deny = append(settings.Permissions.Deny, tool+"(/"+p+")", tool+"(/"+p+"/**)")
		}
	}
	data, err := json.Marshal(settings)
	return string(data), err
}

// childEnv is the allowlisted env plus the build and package caches under
// Cache, the one place outside the worktree that stages and the verify
// command may write.
func (r Runner) childEnv() []string {
	env := append(filterEnv(r.Environ),
		"GOCACHE="+filepath.Join(r.Cache, "go-build"),
		"GOMODCACHE="+filepath.Join(r.Cache, "gomod"),
		"npm_config_cache="+filepath.Join(r.Cache, "npm"),
		"npm_config_store_dir="+filepath.Join(r.Cache, "pnpm-store"),
		"npm_config_cache_dir="+filepath.Join(r.Cache, "pnpm"),
		"UV_CACHE_DIR="+filepath.Join(r.Cache, "uv"),
		"PIP_CACHE_DIR="+filepath.Join(r.Cache, "pip"))
	if r.GoProxy != "" {
		env = append(env, "GOPROXY="+r.GoProxy)
	}
	return env
}

// tempDir is the TMPDIR of env, where the verify command may write.
func tempDir(env []string) string {
	for _, kv := range env {
		if dir, ok := strings.CutPrefix(kv, "TMPDIR="); ok && dir != "" {
			return dir
		}
	}
	return "/tmp"
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
	// The command exited 0 but a leftover process held its output open; the
	// group kill above ended that process, and the result stands.
	if errors.Is(err, exec.ErrWaitDelay) {
		return nil
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
