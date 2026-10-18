package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const githubLoop = `
name: fix-issues
checkout: /src/app
trigger:
  github: { repo: me/app, label: agent, every: 10m }
verify: "go test ./..."
bounds: { max_iterations: 5, timeout: 20m }
protected: [".github/**"]
tests: ["**/*_test.go"]
`

func TestParseGitHubLoop(t *testing.T) {
	got, err := Parse([]byte(githubLoop))
	if err != nil {
		t.Fatal(err)
	}
	want := Loop{
		Name:      "fix-issues",
		Checkout:  "/src/app",
		Trigger:   GitHub{Repo: "me/app", Label: "agent", Every: 10 * time.Minute},
		Verify:    "go test ./...",
		Bounds:    Bounds{MaxIterations: 5, Timeout: 20 * time.Minute},
		Protected: []string{".github/**"},
		Tests:     []string{"**/*_test.go"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParseTimerLoop(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse([]byte(`
name: deps
checkout: ~/src/app
trigger:
  timer: { cron: "0 9 * * 1-5" }
task: bump go dependencies
verify: go test ./...
bounds: { max_iterations: 3, timeout: 1h }
`))
	if err != nil {
		t.Fatal(err)
	}
	want := Loop{
		Name:     "deps",
		Checkout: filepath.Join(home, "src/app"),
		Trigger:  Timer{Cron: "0 9 * * 1-5", Task: "bump go dependencies"},
		Verify:   "go test ./...",
		Bounds:   Bounds{MaxIterations: 3, Timeout: time.Hour},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParseRejectsInvalidFields(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(string) string
		field string
	}{
		{"missing name", drop("name: fix-issues"), "name"},
		{"path in name", swap("name: fix-issues", "name: ../fix"), "name"},
		{"uppercase name", swap("name: fix-issues", "name: Fix"), "name"},
		{"missing checkout", drop("checkout: /src/app"), "checkout"},
		{"relative checkout", swap("checkout: /src/app", "checkout: src/app"), "checkout"},
		{"missing verify", drop(`verify: "go test ./..."`), "verify"},
		{"missing max_iterations", swap("max_iterations: 5, ", ""), "bounds.max_iterations"},
		{"zero max_iterations", swap("max_iterations: 5", "max_iterations: 0"), "bounds.max_iterations"},
		{"missing timeout", swap(", timeout: 20m", ""), "bounds.timeout"},
		{"negative timeout", swap("timeout: 20m", "timeout: -1m"), "bounds.timeout"},
		{"unknown field", add("skills: [drill, implement]"), "skills"},
		{"unknown nested field", swap("timeout: 20m", "timeout: 20m, until: x"), "until"},
		{"no trigger", swap("  github: { repo: me/app, label: agent, every: 10m }", "  {}"), "trigger"},
		{"two triggers", add("  timer: { every: 1h }"), "trigger"},
		{"github without repo", swap("repo: me/app, ", ""), "trigger.github.repo"},
		{"github without label", swap("label: agent, ", ""), "trigger.github.label"},
		{"github without every", swap(", every: 10m", ""), "trigger.github.every"},
		{"github with task", add("task: do it"), "task"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertFieldError(t, c.edit(githubLoop), c.field)
		})
	}
}

func TestParseRejectsInvalidTimer(t *testing.T) {
	base := `
name: deps
checkout: /src/app
trigger:
  timer: { %s }
%s
verify: go test ./...
bounds: { max_iterations: 3, timeout: 1h }
`
	cases := []struct {
		name, schedule, work, field string
	}{
		{"no schedule", "", "task: x", "trigger.timer"},
		{"every and cron", "every: 1h, cron: '0 9 * * *'", "task: x", "trigger.timer"},
		{"no work", "every: 1h", "", "task"},
		{"task and spec", "every: 1h", "task: x\nspec: docs/specs/x.md", "task"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := strings.Replace(strings.Replace(base, "%s", c.schedule, 1), "%s", c.work, 1)
			assertFieldError(t, src, c.field)
		})
	}
}

func assertFieldError(t *testing.T, src, field string) {
	t.Helper()
	_, err := Parse([]byte(src))
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("want FieldError for %q, got %v", field, err)
	}
	if fe.Field != field {
		t.Fatalf("want field %q, got %q (%v)", field, fe.Field, err)
	}
}

func drop(line string) func(string) string { return swap(line+"\n", "") }

func add(line string) func(string) string {
	return func(s string) string {
		return strings.Replace(s, "verify:", line+"\nverify:", 1)
	}
}

func swap(old, new string) func(string) string {
	return func(s string) string {
		if !strings.Contains(s, old) {
			panic("fixture lacks " + old)
		}
		return strings.Replace(s, old, new, 1)
	}
}
