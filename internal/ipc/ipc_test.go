package ipc

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yeremi777/loopgate/internal/daemon"
	"github.com/yeremi777/loopgate/internal/feed"
	"github.com/yeremi777/loopgate/internal/gittest"
	"github.com/yeremi777/loopgate/internal/job"
	"github.com/yeremi777/loopgate/internal/runner"
	"github.com/yeremi777/loopgate/internal/store"
)

func fixLoop(checkout string) string {
	return "name: fix\ncheckout: " + checkout + "\ntrigger:\n  github: { repo: me/app, label: agent, every: 10m }\nverify: \"true\"\nbounds: { max_iterations: 5, timeout: 20m }\n"
}

func TestCommandsReachDaemon(t *testing.T) {
	c := serve(t)
	ctx := context.Background()
	checkout := gittest.Checkout(t, "https://github.com/me/app.git")

	name, err := c.Add(ctx, []byte(fixLoop(checkout)))
	if err != nil || name != "fix" {
		t.Fatalf("add: name=%q err=%v", name, err)
	}
	if err := c.Pause(ctx, "fix"); err != nil {
		t.Fatal(err)
	}
	got, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []daemon.LoopStatus{{Name: "fix", Paused: true, Trigger: "github me/app", Checkout: checkout}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after pause: got %+v, want %+v", got, want)
	}
	if err := c.Resume(ctx, "fix"); err != nil {
		t.Fatal(err)
	}
	got, _ = c.List(ctx)
	if want := []daemon.LoopStatus{{Name: "fix", Trigger: "github me/app", Checkout: checkout}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after resume: got %+v, want %+v", got, want)
	}
	if err := c.Remove(ctx, "fix"); err != nil {
		t.Fatal(err)
	}
	if got, _ = c.List(ctx); len(got) != 0 {
		t.Fatalf("after remove: got %+v", got)
	}
}

func TestAddThatTheClientAbandonsAddsNothing(t *testing.T) {
	c := serve(t)
	checkout := gittest.Checkout(t, "https://github.com/me/app.git")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := c.Add(ctx, []byte(strings.Replace(fixLoop(checkout), `verify: "true"`, "verify: sleep 5", 1))); err == nil {
		t.Fatal("abandoned add succeeded")
	}
	time.Sleep(6 * time.Second)
	if got, err := c.List(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("after an abandoned add: loops %+v, err %v", got, err)
	}
}

func TestDaemonErrorsReachClient(t *testing.T) {
	c := serve(t)
	ctx := context.Background()
	if _, err := c.Add(ctx, []byte("name: fix\n")); err == nil {
		t.Fatal("add of invalid loop succeeded")
	}
	if err := c.Pause(ctx, "nope"); err == nil {
		t.Fatal("pause of unknown loop succeeded")
	}
	if err := c.Remove(ctx, "nope"); err == nil {
		t.Fatal("remove of unknown loop succeeded")
	}
}

func TestNotRunning(t *testing.T) {
	dir := shortTempDir(t)
	missing := NewClient(filepath.Join(dir, "missing.sock"))
	if _, err := missing.List(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("missing socket: want ErrNotRunning, got %v", err)
	}

	stale := filepath.Join(dir, "stale.sock")
	ln, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(stale).List(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("stale socket: want ErrNotRunning, got %v", err)
	}
}

func TestFollowNamesTheStatusOfALoopdThatCannotFollow(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "loopd.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() { _ = http.Serve(ln, http.NotFoundHandler()) }()
	c := NewClient(sock)
	_, err = c.Follow(context.Background(), 1, func(feed.Event) {})
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "loopctl install") {
		t.Fatalf("follow against a loopd without the events route: %v", err)
	}
	if _, err := c.Jobs(context.Background()); err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "loopctl install") {
		t.Fatalf("jobs against a loopd without the jobs route: %v", err)
	}
}

func serve(t *testing.T) *Client {
	t.Helper()
	home := shortTempDir(t)
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	d, err := daemon.New(home, st, daemon.Deps{Pipeline: job.Pipeline{Shell: runner.Runner{Environ: os.Environ(), Cache: os.TempDir()}}})
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(home, "loopd.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() { _ = Serve(ln, d) }()
	return NewClient(sock)
}

// shortTempDir keeps socket paths under the 104-byte limit of macOS.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "lg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
