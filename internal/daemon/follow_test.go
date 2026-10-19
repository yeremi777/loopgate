package daemon

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/yeremi777/loopgate/internal/feed"
	"github.com/yeremi777/loopgate/internal/store"
)

func TestFollowStreamsTheRunningJobAndEndsOnceItsOutcomeIsStored(t *testing.T) {
	r := newRunEnv(t)
	started, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	attached := make(chan struct{})
	followed := make(chan error)
	go func() {
		followed <- r.d.Follow(context.Background(), started.ID, func(e feed.Event) error {
			if got = append(got, e.Stage+" | "+e.Text); len(got) == 1 {
				close(attached)
			}
			return nil
		})
	}()
	<-attached
	r.release()
	if err := <-followed; err != nil {
		t.Fatal(err)
	}
	if j, err := r.st.Job(started.ID); err != nil || j.State != store.Done {
		t.Fatalf("job when follow ended: %+v, %v; want done", j, err)
	}
	for _, want := range []string{"implement 1 | start", "implement 1 | Edit ok.txt", "verify 1 | pass: verify command exited 0", "publish | draft PR https://github.com/me/app/pull/1"} {
		if !slices.Contains(got, want) {
			t.Fatalf("events %q lack %q", got, want)
		}
	}
}

func TestFollowWhileLoopdStopsSaysLoopdStopped(t *testing.T) {
	r := newRunEnv(t)
	started, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	attached := make(chan struct{})
	followed := make(chan error)
	go func() {
		sent := 0
		followed <- r.d.Follow(context.Background(), started.ID, func(feed.Event) error {
			if sent++; sent == 1 {
				close(attached)
			}
			return nil
		})
	}()
	<-attached
	r.d.Close()
	if err := <-followed; !errors.Is(err, ErrStopped) || err.Error() != "loopd stopped" {
		t.Fatalf("follow while loopd stops: %v, want loopd stopped", err)
	}
}

func TestFollowOfAJobThatIsNotRunningReturnsAtOnce(t *testing.T) {
	r := newRunEnv(t)
	started, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	r.release()
	r.waitDone(t, started.ID)
	sent := 0
	if err := r.d.Follow(context.Background(), started.ID, func(feed.Event) error { sent++; return nil }); err != nil || sent != 0 {
		t.Fatalf("follow of a done job: %v, %d events", err, sent)
	}
	if err := r.d.Follow(context.Background(), 99, func(feed.Event) error { return nil }); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("follow of an unknown job: %v", err)
	}
}

func TestFollowThatEndsLeavesTheJobRunning(t *testing.T) {
	r := newRunEnv(t)
	started, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	followed := make(chan error)
	go func() { followed <- r.d.Follow(ctx, started.ID, func(feed.Event) error { return nil }) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-followed; !errors.Is(err, context.Canceled) {
		t.Fatalf("follow after cancel: %v", err)
	}
	if j, err := r.st.Job(started.ID); err != nil || j.State != store.Running {
		t.Fatalf("job after the viewer left: %+v, %v; want running", j, err)
	}
	r.release()
	if got := r.waitDone(t, started.ID); got.State != store.Done {
		t.Fatalf("job ended %s, want done", got.State)
	}
}
