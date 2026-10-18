package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const doneJSON = `{"type":"result","subtype":"success","is_error":false,"structured_output":{"status":"done","summary":"fixed it"}}`

func TestRunPassesSafetyFlagsAndPromptInWorktree(t *testing.T) {
	fake := newFake(t, `printf '%s\n' "$@" > "$REC/args"; pwd -P > "$REC/cwd"; cat > "$REC/stdin"; echo '`+doneJSON+`'`)
	dir := realDir(t)

	res, err := fake.runner().Run(context.Background(), Stage{Dir: dir, Prompt: "/implement docs/specs/x.md", Timeout: time.Minute, DefaultBranch: "trunk", Network: []string{"proxy.golang.org"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Result{Status: StatusDone, Summary: "fixed it"}); res != want {
		t.Fatalf("got %+v, want %+v", res, want)
	}
	if got := fake.read("cwd"); got != dir+"\n" {
		t.Fatalf("cwd %q, want %q", got, dir)
	}
	if got := fake.read("stdin"); got != "/implement docs/specs/x.md" {
		t.Fatalf("prompt on stdin %q", got)
	}

	args := strings.Split(strings.TrimSuffix(fake.read("args"), "\n"), "\n")
	for _, pair := range [][2]string{
		{"--permission-mode", "auto"},
		{"--permission-prompts", "none"},
		{"--output-format", "json"},
	} {
		if flagValue(args, pair[0]) != pair[1] {
			t.Fatalf("want %s %s in %q", pair[0], pair[1], args)
		}
	}
	if !slices.Contains(args, "-p") {
		t.Fatalf("want -p in %q", args)
	}

	var schema struct {
		Properties struct {
			Status struct {
				Enum []string `json:"enum"`
			} `json:"status"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(flagValue(args, "--json-schema")), &schema); err != nil {
		t.Fatalf("json schema: %v", err)
	}
	if !slices.Equal(schema.Properties.Status.Enum, []string{"done", "blocked", "revise"}) {
		t.Fatalf("schema status enum %q", schema.Properties.Status.Enum)
	}

	var settings struct {
		Sandbox struct {
			Enabled bool `json:"enabled"`
			Network struct {
				AllowedDomains []string `json:"allowedDomains"`
			} `json:"network"`
		} `json:"sandbox"`
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(flagValue(args, "--settings")), &settings); err != nil {
		t.Fatalf("settings: %v", err)
	}
	if !settings.Sandbox.Enabled {
		t.Fatal("sandbox not enabled")
	}
	if got := settings.Sandbox.Network.AllowedDomains; !slices.Equal(got, []string{"proxy.golang.org"}) {
		t.Fatalf("allowed domains %q", got)
	}
	for _, rule := range []string{
		"Bash(gh pr merge:*)",
		"Bash(gh pr ready:*)",
		"Bash(git merge:*)",
		"Bash(git push --force:*)",
		"Bash(git push -f:*)",
		"Bash(git push origin trunk:*)",
		"Bash(git push origin HEAD:trunk:*)",
	} {
		if !slices.Contains(settings.Permissions.Deny, rule) {
			t.Fatalf("deny rules %q lack %q", settings.Permissions.Deny, rule)
		}
	}
}

func TestRunReportsBlockedQuestion(t *testing.T) {
	fake := newFake(t, `cat >/dev/null; echo '{"is_error":false,"structured_output":{"status":"blocked","question":"which db?","summary":"stopped"}}'`)
	res, err := fake.runner().Run(context.Background(), Stage{Dir: t.TempDir(), Prompt: "p", Timeout: time.Minute, DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Result{Status: StatusBlocked, Question: "which db?", Summary: "stopped"}); res != want {
		t.Fatalf("got %+v, want %+v", res, want)
	}
}

func TestRunReportsRevision(t *testing.T) {
	fake := newFake(t, `cat >/dev/null; echo '{"is_error":false,"structured_output":{"status":"revise","summary":"nil map write in store.go:40"}}'`)
	res, err := fake.runner().Run(context.Background(), Stage{Dir: t.TempDir(), Prompt: "p", Timeout: time.Minute, DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Result{Status: StatusRevise, Summary: "nil map write in store.go:40"}); res != want {
		t.Fatalf("got %+v, want %+v", res, want)
	}
}

func TestRunRejectsBadResults(t *testing.T) {
	cases := map[string]string{
		"is_error":          `echo '{"is_error":true,"subtype":"error_max_turns","structured_output":{"status":"done","summary":"x"}}'`,
		"no structured":     `echo '{"is_error":false,"result":"done"}'`,
		"unknown status":    `echo '{"is_error":false,"structured_output":{"status":"maybe","summary":"x"}}'`,
		"not json":          `echo 'oops'`,
		"non-zero exit":     `echo '` + doneJSON + `'; exit 3`,
		"blocked, no quest": `echo '{"is_error":false,"structured_output":{"status":"blocked","summary":"x"}}'`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			fake := newFake(t, "cat >/dev/null; "+body)
			if res, err := fake.runner().Run(context.Background(), Stage{Dir: t.TempDir(), Prompt: "p", Timeout: time.Minute, DefaultBranch: "main"}); err == nil {
				t.Fatalf("want error, got %+v", res)
			}
		})
	}
}

func TestRunKeepsOnlyAllowlistedEnv(t *testing.T) {
	fake := newFake(t, `cat >/dev/null; env > "$REC/env"; echo '`+doneJSON+`'`)
	r := fake.runner()
	r.Environ = append(r.Environ,
		"HOME=/home/me",
		"SSH_AUTH_SOCK=/tmp/agent",
		"AWS_SECRET_ACCESS_KEY=secret",
		"GITHUB_TOKEN=secret",
		"ANTHROPIC_API_KEY=secret",
		"DATABASE_URL=postgres://secret",
	)
	if _, err := r.Run(context.Background(), Stage{Dir: t.TempDir(), Prompt: "p", Timeout: time.Minute, DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	env := strings.Split(fake.read("env"), "\n")
	for _, want := range []string{"HOME=/home/me", "SSH_AUTH_SOCK=/tmp/agent"} {
		if !slices.Contains(env, want) {
			t.Fatalf("child env lacks %s: %q", want, env)
		}
	}
	for _, line := range env {
		if strings.Contains(line, "secret") {
			t.Fatalf("child env leaks %q", line)
		}
	}
}

func TestTimeoutKillsWholeProcessGroup(t *testing.T) {
	fake := newFake(t, `cat >/dev/null; sleep 300 & echo $! > "$REC/grandchild"; echo $$ > "$REC/child"; wait`)
	start := time.Now()
	_, err := fake.runner().Run(context.Background(), Stage{Dir: t.TempDir(), Prompt: "p", Timeout: 500 * time.Millisecond, DefaultBranch: "main"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("Run did not return promptly after timeout")
	}
	fake.assertDead("child")
	fake.assertDead("grandchild")
}

func TestCancelKillsWholeProcessGroup(t *testing.T) {
	fake := newFake(t, `cat >/dev/null; sleep 300 & echo $! > "$REC/grandchild"; wait`)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := fake.runner().Run(ctx, Stage{Dir: t.TempDir(), Prompt: "p", Timeout: time.Minute, DefaultBranch: "main"})
		done <- err
	}()
	fake.waitFor("grandchild")
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("want Canceled, got %v", err)
	}
	fake.assertDead("grandchild")
}

func TestLeftoverProcessesDieAfterStage(t *testing.T) {
	fake := newFake(t, `cat >/dev/null; sleep 300 >/dev/null 2>&1 & echo $! > "$REC/grandchild"; echo '`+doneJSON+`'`)
	if _, err := fake.runner().Run(context.Background(), Stage{Dir: t.TempDir(), Prompt: "p", Timeout: time.Minute, DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	fake.assertDead("grandchild")
}

type fake struct {
	t      *testing.T
	claude string
	rec    string
}

// newFake writes a claude stand-in that runs body with REC set to a record directory.
func newFake(t *testing.T, body string) fake {
	t.Helper()
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	if err := os.Mkdir(rec, 0o700); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nREC=" + rec + "\n" + body + "\n"
	if err := os.WriteFile(claude, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return fake{t: t, claude: claude, rec: rec}
}

func (f fake) runner() Runner {
	return Runner{Claude: f.claude, Environ: []string{"PATH=" + os.Getenv("PATH")}}
}

func (f fake) read(name string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.rec, name))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}

func (f fake) waitFor(name string) {
	f.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if data, err := os.ReadFile(filepath.Join(f.rec, name)); err == nil && len(data) > 0 {
			return
		}
	}
	f.t.Fatalf("%s never recorded", name)
}

func (f fake) assertDead(name string) {
	f.t.Helper()
	f.waitFor(name)
	pid, err := strconv.Atoi(strings.TrimSpace(f.read(name)))
	if err != nil {
		f.t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	f.t.Fatalf("%s (pid %d) still alive", name, pid)
}

func flagValue(args []string, name string) string {
	i := slices.Index(args, name)
	if i < 0 || i+1 >= len(args) {
		return ""
	}
	return args[i+1]
}

// realDir resolves the macOS /var -> /private/var link so pwd -P matches.
func realDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
