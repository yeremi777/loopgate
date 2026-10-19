package runner

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const doneJSON = `{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.25,"structured_output":{"status":"done","summary":"fixed it"}}`

func TestRunPassesSafetyFlagsAndPromptInWorktree(t *testing.T) {
	fake := newFake(t, `printf '%s\n' "$@" > "$REC/args"; pwd -P > "$REC/cwd"; cat > "$REC/stdin"; echo '`+doneJSON+`'`)
	dir := realDir(t)

	r := fake.runner()
	r.DenyRead = []string{"/home/me/.ssh"}
	res, err := r.Run(context.Background(), Stage{Dir: dir, Prompt: "/implement docs/specs/x.md", Timeout: time.Minute, Network: []string{"proxy.golang.org"}, DenyWrite: []string{"/repo/.git/config", "/repo/.git/hooks"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Result{Status: StatusDone, Summary: "fixed it", CostUSD: 0.25}); res != want {
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
	for _, flag := range []string{"-p", "--strict-mcp-config"} {
		if !slices.Contains(args, flag) {
			t.Fatalf("want %s in %q", flag, args)
		}
	}
	if got := flagValue(args, "--setting-sources"); got != "user" {
		t.Fatalf("--setting-sources %q, want user so worktree settings and hooks never load", got)
	}
	if got := flagValue(args, "--add-dir"); got != fake.runner().Cache {
		t.Fatalf("--add-dir %q, want the cache dir %q", got, fake.runner().Cache)
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
			Enabled                  bool  `json:"enabled"`
			AllowUnsandboxedCommands *bool `json:"allowUnsandboxedCommands"`
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
	if err := json.Unmarshal([]byte(flagValue(args, "--settings")), &settings); err != nil {
		t.Fatalf("settings: %v", err)
	}
	if !settings.Sandbox.Enabled {
		t.Fatal("sandbox not enabled")
	}
	if u := settings.Sandbox.AllowUnsandboxedCommands; u == nil || *u {
		t.Fatal("stages may ask to run commands outside the sandbox")
	}
	if got := settings.Sandbox.Network.AllowedDomains; !slices.Equal(got, []string{"proxy.golang.org"}) {
		t.Fatalf("allowed domains %q", got)
	}
	if got := settings.Sandbox.Filesystem.DenyWrite; !slices.Equal(got, []string{"/repo/.git/config", "/repo/.git/hooks"}) {
		t.Fatalf("deny write %q", got)
	}
	if got := settings.Sandbox.Filesystem.DenyRead; !slices.Equal(got, []string{"/home/me/.ssh"}) {
		t.Fatalf("deny read %q", got)
	}
	for _, rule := range []string{"Bash(gh:*)", "Bash(git push:*)", "Bash(git merge:*)",
		"Read(//home/me/.ssh/**)", "Edit(//repo/.git/config)", "Write(//repo/.git/hooks/**)"} {
		if !slices.Contains(settings.Permissions.Deny, rule) {
			t.Fatalf("deny rules %q lack %q", settings.Permissions.Deny, rule)
		}
	}
}

func TestRunReportsBlockedQuestion(t *testing.T) {
	fake := newFake(t, `cat >/dev/null; echo '{"is_error":false,"structured_output":{"status":"blocked","question":"which db?","summary":"stopped"}}'`)
	res, err := fake.runner().Run(context.Background(), Stage{Dir: t.TempDir(), Prompt: "p", Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Result{Status: StatusBlocked, Question: "which db?", Summary: "stopped"}); res != want {
		t.Fatalf("got %+v, want %+v", res, want)
	}
}

func TestRunReportsRevision(t *testing.T) {
	fake := newFake(t, `cat >/dev/null; echo '{"is_error":false,"structured_output":{"status":"revise","summary":"nil map write in store.go:40"}}'`)
	res, err := fake.runner().Run(context.Background(), Stage{Dir: t.TempDir(), Prompt: "p", Timeout: time.Minute})
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
			if res, err := fake.runner().Run(context.Background(), Stage{Dir: t.TempDir(), Prompt: "p", Timeout: time.Minute}); err == nil {
				t.Fatalf("want error, got %+v", res)
			}
		})
	}
}

func TestRunKeepsOnlyAllowlistedEnv(t *testing.T) {
	fake := newFake(t, `cat >/dev/null; env > "$REC/env"; echo '`+doneJSON+`'`)
	r := fake.runner()
	r.GoProxy = "file:///home/me/go/pkg/mod/cache/download,https://proxy.golang.org"
	r.Environ = append(r.Environ,
		"HOME=/home/me",
		"SSH_AUTH_SOCK=/tmp/agent",
		"TMPDIR=/tmp/me/",
		"GOCACHE=/home/me/Library/Caches/go-build",
		"AWS_SECRET_ACCESS_KEY=secret",
		"GITHUB_TOKEN=secret",
		"ANTHROPIC_API_KEY=secret",
		"DATABASE_URL=postgres://secret",
	)
	if _, err := r.Run(context.Background(), Stage{Dir: t.TempDir(), Prompt: "p", Timeout: time.Minute}); err != nil {
		t.Fatal(err)
	}
	env := strings.Split(fake.read("env"), "\n")
	for _, want := range []string{"GOMODCACHE=" + r.Cache + "/gomod", "GOPROXY=file:///home/me/go/pkg/mod/cache/download,https://proxy.golang.org"} {
		if !slices.Contains(env, want) {
			t.Fatalf("child env lacks %s", want)
		}
	}
	for _, want := range []string{
		"HOME=/home/me", "SSH_AUTH_SOCK=/tmp/agent", "TMPDIR=/tmp/me/", "GOCACHE=" + r.Cache + "/go-build",
		"npm_config_cache=" + r.Cache + "/npm", "npm_config_store_dir=" + r.Cache + "/pnpm-store",
		"UV_CACHE_DIR=" + r.Cache + "/uv", "PIP_CACHE_DIR=" + r.Cache + "/pip",
	} {
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
	fake := newFake(t, `echo $$ > "$REC/child"; cat >/dev/null; sleep 300 & echo $! > "$REC/grandchild"; wait`)
	start := time.Now()
	_, err := fake.runner().Run(context.Background(), Stage{Dir: t.TempDir(), Prompt: "p", Timeout: 3 * time.Second})
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
		_, err := fake.runner().Run(ctx, Stage{Dir: t.TempDir(), Prompt: "p", Timeout: time.Minute})
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
	if _, err := fake.runner().Run(context.Background(), Stage{Dir: t.TempDir(), Prompt: "p", Timeout: time.Minute}); err != nil {
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
	return Runner{Claude: f.claude, Environ: []string{"PATH=" + os.Getenv("PATH")}, Cache: filepath.Join(filepath.Dir(f.rec), "cache")}
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

func TestShellReportsExitCodeAndOutput(t *testing.T) {
	r := Runner{Environ: []string{"PATH=" + os.Getenv("PATH"), "GITHUB_TOKEN=secret"}, Cache: t.TempDir()}
	dir := realDir(t)
	res, err := r.Shell(context.Background(), dir, `pwd -P; env | grep -c secret; echo boom >&2; exit 3`, time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := (ShellResult{ExitCode: 3, Output: dir + "\n0\nboom\n"}); res != want {
		t.Fatalf("got %+v, want %+v", res, want)
	}
}

func TestShellTimeoutKillsWholeProcessGroup(t *testing.T) {
	r := Runner{Environ: []string{"PATH=" + os.Getenv("PATH")}, Cache: t.TempDir()}
	dir := t.TempDir()
	_, err := r.Shell(context.Background(), dir, `sleep 300 & echo $! > grandchild; wait`, 3*time.Second, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
	fake{t: t, rec: dir}.assertDead("grandchild")
}

func TestShellSandboxConfinesTheVerifyCommand(t *testing.T) {
	dir, cache, secret := realDir(t), realDir(t), realDir(t)
	if err := os.WriteFile(filepath.Join(secret, "key"), []byte("hush"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(realDir(t), "outside")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() { _ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })) }()

	r := Runner{Environ: []string{"PATH=" + os.Getenv("PATH"), "SSH_AUTH_SOCK=/tmp/agent", "TMPDIR=" + realDir(t)}, Cache: cache, DenyRead: []string{secret}}
	script := strings.Join([]string{
		"echo in > inside && echo inside-ok",
		"echo c > " + cache + "/c && echo cache-ok",
		"echo out > " + outside + " && echo OUTSIDE-WRITTEN",
		"cat " + secret + "/key && echo SECRET-READ",
		"curl -s -o /dev/null -w 'local-%{http_code}\\n' http://" + ln.Addr().String() + "/",
		"curl -s -m 5 -o /dev/null -w 'remote-%{http_code}\\n' https://example.com/",
		"env | grep -c SSH_AUTH_SOCK",
		"echo $GOCACHE $GOMODCACHE",
	}, "; ")
	res, err := r.Shell(context.Background(), dir, script, time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"inside-ok", "cache-ok", "local-204", "remote-000", "\n0\n", cache + "/go-build " + cache + "/gomod"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("output lacks %q:\n%s", want, res.Output)
		}
	}
	for _, bad := range []string{"OUTSIDE-WRITTEN", "SECRET-READ", "hush"} {
		if strings.Contains(res.Output, bad) {
			t.Fatalf("sandbox let through %q:\n%s", bad, res.Output)
		}
	}
}

func TestSetupReachesTheNetworkOnlyThroughTheProxy(t *testing.T) {
	dir, cache := realDir(t), realDir(t)
	outside := filepath.Join(realDir(t), "outside")
	guard := filepath.Join(dir, ".git")
	r := Runner{Environ: []string{"PATH=" + os.Getenv("PATH"), "SSH_AUTH_SOCK=/tmp/agent", "TMPDIR=" + realDir(t)}, Cache: cache}
	script := strings.Join([]string{
		"echo in > inside && echo inside-ok",
		"echo out > " + outside + " && echo OUTSIDE-WRITTEN",
		"echo evil > .git && echo GUARD-WRITTEN",
		"env | grep -c SSH_AUTH_SOCK",
		`[ -n "$HTTPS_PROXY" ] && [ "$HTTPS_PROXY" = "$HTTP_PROXY" ] && [ "$HTTPS_PROXY" = "$https_proxy" ] && [ "$HTTPS_PROXY" = "$http_proxy" ] && echo proxy-set`,
		"curl -s -m 5 -o /dev/null -w 'proxied-%{http_connect}\\n' https://example.com/",
		"curl -s -m 5 --noproxy '*' -o /dev/null -w 'bypass-%{http_code}\\n' https://example.com/",
		"exit 4",
	}, "; ")
	for _, network := range [][]string{nil, {"registry.npmjs.org"}} {
		res, err := r.Setup(context.Background(), dir, script, time.Minute, []string{guard}, network)
		if err != nil {
			t.Fatal(err)
		}
		if res.ExitCode != 4 {
			t.Fatalf("network %q: exit %d, want 4:\n%s", network, res.ExitCode, res.Output)
		}
		for _, want := range []string{"inside-ok", "\n0\n", "proxy-set", "proxied-403", "bypass-000"} {
			if !strings.Contains(res.Output, want) {
				t.Fatalf("network %q: output lacks %q:\n%s", network, want, res.Output)
			}
		}
		for _, bad := range []string{"OUTSIDE-WRITTEN", "GUARD-WRITTEN"} {
			if strings.Contains(res.Output, bad) {
				t.Fatalf("network %q: sandbox let through %q:\n%s", network, bad, res.Output)
			}
		}
		if !strings.HasSuffix(res.Output, "loopgate refused hosts outside the loop's network list: example.com\n") {
			t.Fatalf("network %q: output does not end naming the refused host:\n%s", network, res.Output)
		}
	}
}

func TestShellPassesWhenABackgroundProcessOutlivesASuccessfulCommand(t *testing.T) {
	r := Runner{Environ: []string{"PATH=" + os.Getenv("PATH")}, Cache: t.TempDir()}
	res, err := r.Shell(context.Background(), t.TempDir(), "sleep 30 & echo done", time.Minute, nil)
	if err != nil || res.ExitCode != 0 || res.Output != "done\n" {
		t.Fatalf("got %+v, %v; want a pass", res, err)
	}
}

func TestCredentialDirs(t *testing.T) {
	got := CredentialDirs("/Users/me")
	for _, want := range []string{"/Users/me/.ssh", "/Users/me/.aws", "/Users/me/.config/gh", "/Users/me/Library/Keychains"} {
		if !slices.Contains(got, want) {
			t.Fatalf("%q lacks %s", got, want)
		}
	}
}

func TestShellCannotRewriteTheWorktreesGitPointer(t *testing.T) {
	dir := realDir(t)
	pointer := filepath.Join(dir, ".git")
	if err := os.WriteFile(pointer, []byte("gitdir: /repo/.git/worktrees/wt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Runner{Environ: []string{"PATH=" + os.Getenv("PATH")}, Cache: t.TempDir()}
	res, err := r.Shell(context.Background(), dir, "echo 'gitdir: /tmp/evil' > .git; echo other > ok.txt && echo inside-ok", time.Minute, []string{pointer})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(pointer); string(got) != "gitdir: /repo/.git/worktrees/wt\n" {
		t.Fatalf("verify rewrote the .git pointer to %q", got)
	}
	if !strings.Contains(res.Output, "inside-ok") {
		t.Fatalf("other worktree writes blocked too:\n%s", res.Output)
	}
}
