package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yeremi777/loopgate/internal/config"
	"github.com/yeremi777/loopgate/internal/gittest"
	"github.com/yeremi777/loopgate/internal/job"
	"github.com/yeremi777/loopgate/internal/runner"
	"github.com/yeremi777/loopgate/internal/store"
)

func fixLoop(checkout string) string {
	return "name: fix\ncheckout: " + checkout + "\ntrigger:\n  github: { repo: me/app, label: agent, every: 10m }\nverify: \"true\"\nbounds: { max_iterations: 5, timeout: 20m }\n"
}

func TestAddedLoopsSurviveRestart(t *testing.T) {
	home := t.TempDir()
	d := newDaemon(t, home)
	checkout := gittest.Checkout(t, "https://github.com/me/app.git")
	if _, err := d.Add([]byte(fixLoop(checkout))); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Add([]byte(strings.Replace(fixLoop(checkout), "name: fix", "name: docs", 1))); err != nil {
		t.Fatal(err)
	}
	if err := d.SetPaused("docs", true); err != nil {
		t.Fatal(err)
	}

	d = newDaemon(t, home)
	got, err := d.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []LoopStatus{
		{Name: "docs", Paused: true, Trigger: "github me/app", Checkout: checkout},
		{Name: "fix", Paused: false, Trigger: "github me/app", Checkout: checkout},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestAddRejectsInvalidAndDuplicateLoops(t *testing.T) {
	d := newDaemon(t, t.TempDir())
	checkout := gittest.Checkout(t, "https://github.com/me/app.git")
	var fe *config.FieldError
	if _, err := d.Add([]byte("name: fix\n")); !errors.As(err, &fe) {
		t.Fatalf("want FieldError, got %v", err)
	}
	if _, err := d.Add([]byte(fixLoop(checkout))); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Add([]byte(fixLoop(checkout))); !errors.Is(err, ErrLoopExists) {
		t.Fatalf("want ErrLoopExists, got %v", err)
	}
	if got, _ := d.List(); len(got) != 1 {
		t.Fatalf("want one loop, got %+v", got)
	}
}

func TestRemovedLoopIsGoneAfterRestart(t *testing.T) {
	home := t.TempDir()
	d := newDaemon(t, home)
	checkout := gittest.Checkout(t, "https://github.com/me/app.git")
	if _, err := d.Add([]byte(fixLoop(checkout))); err != nil {
		t.Fatal(err)
	}
	if err := d.SetPaused("fix", true); err != nil {
		t.Fatal(err)
	}
	if err := d.Remove("fix"); err != nil {
		t.Fatal(err)
	}
	d = newDaemon(t, home)
	if got, _ := d.List(); len(got) != 0 {
		t.Fatalf("want no loops, got %+v", got)
	}

	if _, err := d.Add([]byte(fixLoop(checkout))); err != nil {
		t.Fatal(err)
	}
	got, _ := d.List()
	if want := []LoopStatus{{Name: "fix", Trigger: "github me/app", Checkout: checkout}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("re-added loop: got %+v, want %+v", got, want)
	}
}

func TestAddRefusesACheckoutThatJobsCannotUse(t *testing.T) {
	d := newDaemon(t, t.TempDir())
	checkout := gittest.Checkout(t, "https://github.com/me/app.git")
	noHead := gittest.Checkout(t, "https://github.com/me/app.git")
	gitRun(t, noHead, "remote", "set-head", "origin", "--delete")
	sub := filepath.Join(checkout, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, loop, want string
	}{
		{"not a clone", fixLoop(t.TempDir()), "not the top level of a git work tree"},
		{"a subdirectory", fixLoop(sub), "not the top level of a git work tree"},
		{"no origin HEAD", fixLoop(noHead), "git -C " + noHead + " remote set-head origin --auto"},
		{"another repo", strings.Replace(fixLoop(checkout), "me/app", "me/other", 1), "me/other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := d.Add([]byte(tc.loop))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want it to name %q", err, tc.want)
			}
			if got, _ := d.List(); len(got) != 0 {
				t.Fatalf("loop added anyway: %+v", got)
			}
		})
	}
}

func TestAddMatchesTheGitHubRepoInEveryRemoteURLForm(t *testing.T) {
	for _, url := range []string{
		"https://github.com/me/app.git",
		"https://github.com/me/app",
		"git@github.com:me/app.git",
		"ssh://git@github.com/Me/App.git",
	} {
		t.Run(url, func(t *testing.T) {
			d := newDaemon(t, t.TempDir())
			if _, err := d.Add([]byte(fixLoop(gittest.Checkout(t, url)))); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUnknownLoop(t *testing.T) {
	d := newDaemon(t, t.TempDir())
	if err := d.Remove("nope"); !errors.Is(err, ErrUnknownLoop) {
		t.Fatalf("remove: want ErrUnknownLoop, got %v", err)
	}
	if err := d.SetPaused("nope", true); !errors.Is(err, ErrUnknownLoop) {
		t.Fatalf("pause: want ErrUnknownLoop, got %v", err)
	}
}

func newDaemon(t *testing.T, home string) *Daemon {
	t.Helper()
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	d, err := New(home, st, Deps{Pipeline: job.Pipeline{Shell: runner.Runner{Environ: os.Environ(), Cache: os.TempDir()}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d
}
