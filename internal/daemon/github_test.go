package daemon

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeremi777/loopgate/internal/job"
	"github.com/yeremi777/loopgate/internal/store"
)

func TestPollRunsLowestUnclaimedIssueOnce(t *testing.T) {
	r := newRunEnv(t)
	r.issues.open = []job.Issue{{Number: 3, Title: "three"}, {Number: 9, Title: "nine"}}
	r.release()
	r.d.Tick(t0)
	r.d.Tick(t0.Add(10 * time.Minute))
	first := r.waitDone(t, r.nth(t, 1))
	if !strings.HasPrefix(first.Branch, "loop/issues-3-") || first.State != store.Done {
		t.Fatalf("first issue job %+v", first)
	}
	r.d.Tick(t0.Add(20 * time.Minute))
	second := r.waitDone(t, r.nth(t, 2))
	if !strings.HasPrefix(second.Branch, "loop/issues-9-") {
		t.Fatalf("second issue job %+v", second)
	}
	r.d.Tick(t0.Add(30 * time.Minute))
	if n := r.jobCount(t); n != 2 {
		t.Fatalf("%d jobs; a handled issue ran again", n)
	}
	if got := r.issues.said(3); len(got) != 1 || !strings.Contains(got[0], "https://github.com/me/app/pull/1") {
		t.Fatalf("issue 3 comments %q", got)
	}
}

func TestPollWhileBusyLeavesIssueUnclaimed(t *testing.T) {
	r := newRunEnv(t)
	r.issues.open = []job.Issue{{Number: 3, Title: "three"}}
	r.d.Tick(t0)
	if _, err := r.d.Run("deps", ""); err != nil {
		t.Fatal(err)
	}
	r.d.Tick(t0.Add(10 * time.Minute))
	if n := r.jobCount(t); n != 1 {
		t.Fatalf("%d jobs while busy", n)
	}
	r.release()
	r.waitDone(t, r.nth(t, 1))
	r.d.Tick(t0.Add(20 * time.Minute))
	if j := r.waitDone(t, r.nth(t, 2)); !strings.HasPrefix(j.Branch, "loop/issues-3-") {
		t.Fatalf("issue skipped while busy was lost: %+v", j)
	}
}

func TestRequeueRunsAnIssueAgain(t *testing.T) {
	r := newRunEnv(t)
	r.issues.open = []job.Issue{{Number: 3, Title: "three"}}
	r.release()
	r.d.Tick(t0)
	r.d.Tick(t0.Add(10 * time.Minute))
	r.waitDone(t, r.nth(t, 1))
	if err := r.d.Requeue("issues", 3); err != nil {
		t.Fatal(err)
	}
	r.d.Tick(t0.Add(20 * time.Minute))
	if j := r.waitDone(t, r.nth(t, 2)); !strings.HasPrefix(j.Branch, "loop/issues-3-") {
		t.Fatalf("requeued issue job %+v", j)
	}
}

func TestBlockedIssueGetsQuestionAndLabel(t *testing.T) {
	r := newRunEnv(t)
	r.stages.blockFirst = true
	r.issues.open = []job.Issue{{Number: 4, Title: "four"}}
	r.release()
	r.d.Tick(t0)
	r.d.Tick(t0.Add(10 * time.Minute))
	if j := r.waitDone(t, r.nth(t, 1)); j.State != store.Blocked {
		t.Fatalf("job %+v", j)
	}
	if got := r.issues.said(4); len(got) != 1 || !strings.Contains(got[0], "which db?") {
		t.Fatalf("issue 4 comments %q", got)
	}
	if got := r.issues.labelled(4); got != "needs-drill" {
		t.Fatalf("issue 4 label %q", got)
	}
}

type fakeIssues struct {
	mu       sync.Mutex
	open     []job.Issue
	comments map[int][]string
	labels   map[int]string
}

func (f *fakeIssues) List(context.Context, string, string) ([]job.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]job.Issue{}, f.open...), nil
}

func (f *fakeIssues) Comment(_ context.Context, _ string, n int, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments[n] = append(f.comments[n], body)
	return nil
}

func (f *fakeIssues) Label(_ context.Context, _ string, n int, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.labels[n] = label
	return nil
}

func (f *fakeIssues) said(n int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.comments[n]...)
}

func (f *fakeIssues) labelled(n int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.labels[n]
}
