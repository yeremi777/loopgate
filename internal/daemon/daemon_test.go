package daemon

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yeremi777/loopgate/internal/config"
	"github.com/yeremi777/loopgate/internal/store"
)

const fixLoop = `
name: fix
checkout: /src/app
trigger:
  github: { repo: me/app, label: agent, every: 10m }
verify: go test ./...
bounds: { max_iterations: 5, timeout: 20m }
`

func TestAddedLoopsSurviveRestart(t *testing.T) {
	home := t.TempDir()
	d := newDaemon(t, home)
	if _, err := d.Add([]byte(fixLoop)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Add([]byte(strings.Replace(fixLoop, "name: fix", "name: docs", 1))); err != nil {
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
	want := []LoopStatus{{Name: "docs", Paused: true}, {Name: "fix", Paused: false}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestAddRejectsInvalidAndDuplicateLoops(t *testing.T) {
	d := newDaemon(t, t.TempDir())
	var fe *config.FieldError
	if _, err := d.Add([]byte("name: fix\n")); !errors.As(err, &fe) {
		t.Fatalf("want FieldError, got %v", err)
	}
	if _, err := d.Add([]byte(fixLoop)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Add([]byte(fixLoop)); !errors.Is(err, ErrLoopExists) {
		t.Fatalf("want ErrLoopExists, got %v", err)
	}
	if got, _ := d.List(); len(got) != 1 {
		t.Fatalf("want one loop, got %+v", got)
	}
}

func TestRemovedLoopIsGoneAfterRestart(t *testing.T) {
	home := t.TempDir()
	d := newDaemon(t, home)
	if _, err := d.Add([]byte(fixLoop)); err != nil {
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

	if _, err := d.Add([]byte(fixLoop)); err != nil {
		t.Fatal(err)
	}
	got, _ := d.List()
	if want := []LoopStatus{{Name: "fix", Paused: false}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("re-added loop: got %+v, want %+v", got, want)
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
	d, err := New(home, st, Deps{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}
