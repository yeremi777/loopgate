package store

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestStateSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	st := open(t, path)
	if err := st.SetPaused("fix", true); err != nil {
		t.Fatal(err)
	}
	job, err := st.CreateJob("fix")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetJobState(job.ID, Blocked); err != nil {
		t.Fatal(err)
	}
	if fresh, err := st.ClaimIssue("fix", 42); err != nil || !fresh {
		t.Fatalf("first claim: fresh=%v err=%v", fresh, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st = open(t, path)
	if paused, err := st.Paused("fix"); err != nil || !paused {
		t.Fatalf("paused=%v err=%v, want true", paused, err)
	}
	got, err := st.Job(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Job{ID: job.ID, Loop: "fix", State: Blocked}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if fresh, err := st.ClaimIssue("fix", 42); err != nil || fresh {
		t.Fatalf("claim after reopen: fresh=%v err=%v, want already claimed", fresh, err)
	}
}

func TestNewJobIsQueuedAndIssuesAreClaimedPerLoop(t *testing.T) {
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	job, err := st.CreateJob("fix")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != Queued || job.Loop != "fix" {
		t.Fatalf("got %+v, want queued job of fix", job)
	}
	if fresh, err := st.ClaimIssue("other", 42); err != nil || !fresh {
		t.Fatal("issue 42 of another loop is still new")
	}
	if fresh, err := st.ClaimIssue("fix", 42); err != nil || !fresh {
		t.Fatal("issue 42 of fix is new")
	}
	if paused, err := st.Paused("unknown"); err != nil || paused {
		t.Fatalf("unknown loop: paused=%v err=%v", paused, err)
	}
}

func TestUnknownJob(t *testing.T) {
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if _, err := st.Job("999999999999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := st.SetJobState("999999999999", Running); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestJobsOrderTheOnesInProgressAndTheFiveNewestEndedNewestFirstByCreation(t *testing.T) {
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	ids := []string{"00000000000a", "ffffffffffff", "000000000001", "777777777777", "aaaaaaaaaaaa", "100000000000", "fffffffffffe", "000000000002", "555555555555", "333333333333"}
	next := 0
	st.newID = func() string { next++; return ids[next-1] }
	states := []JobState{Blocked, Failed, Exhausted, Manual, Done, Done, Failed, Done, Done, Running}
	for i, state := range states {
		j, err := st.CreateJob("fix")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.StartJob(j.ID, Start{Work: fmt.Sprintf("job %d", i+1)}); err != nil {
			t.Fatal(err)
		}
		if err := st.FinishJob(j.ID, Finish{State: state, PRURL: "https://github.com/me/app/pull/1"}); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := st.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	var works []string
	for _, j := range jobs {
		works = append(works, j.Work)
	}
	if want := []string{"job 10", "job 9", "job 8", "job 7", "job 6", "job 5", "job 4", "job 1"}; !slices.Equal(works, want) {
		t.Fatalf("jobs %v, want %v", works, want)
	}
	if j := jobs[1]; j.ID != "555555555555" || j.Loop != "fix" || j.State != Done || j.PRURL != "https://github.com/me/app/pull/1" {
		t.Fatalf("job 9 read back as %+v", j)
	}
}

func TestNewIDIsTwelveHexCharactersAndNeverOneAlreadyStored(t *testing.T) {
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	seen := map[string]bool{}
	for range 50 {
		j, err := st.CreateJob("fix")
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(j.ID) || seen[j.ID] {
			t.Fatalf("new id %q: want 12 lowercase hex characters, never repeated", j.ID)
		}
		seen[j.ID] = true
	}

	stored, err := st.CreateJob("fix")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetJobState(stored.ID, Blocked); err != nil {
		t.Fatal(err)
	}
	candidates := []string{stored.ID, stored.ID, "0123456789ab"}
	st.newID = func() string { id := candidates[0]; candidates = candidates[1:]; return id }
	fresh, err := st.CreateJob("docs")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ID != "0123456789ab" {
		t.Fatalf("new job got id %q, want the first id not stored", fresh.ID)
	}
	if got, _ := st.Job(stored.ID); got.Loop != "fix" || got.State != Blocked {
		t.Fatalf("stored job changed to %+v", got)
	}
}

func open(t *testing.T, path string) *Store {
	t.Helper()
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestJobDetailsSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	st := open(t, path)
	job, err := st.CreateJob("fix")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.StartJob(job.ID, Start{Work: "spec docs/specs/x.md", Branch: "loop/fix-1", Worktree: "/jobs/1/worktree", Base: "abc123", Issue: 7}); err != nil {
		t.Fatal(err)
	}
	end := Finish{State: Done, Summary: "added Double", PRURL: "https://github.com/me/app/pull/7", CostUSD: 1.25}
	if err := st.FinishJob(job.ID, end); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := open(t, path).Job(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := Job{
		ID: job.ID, Loop: "fix", State: Done, Work: "spec docs/specs/x.md",
		Branch: "loop/fix-1", Worktree: "/jobs/1/worktree", Base: "abc123", Issue: 7,
		Summary: "added Double", PRURL: "https://github.com/me/app/pull/7", CostUSD: 1.25,
	}
	if got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestInterruptFailsJobsLeftRunning(t *testing.T) {
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	running, _ := st.CreateJob("fix")
	if err := st.StartJob(running.ID, Start{Work: "task x", Branch: "loop/fix-1", Worktree: "/wt"}); err != nil {
		t.Fatal(err)
	}
	queued, _ := st.CreateJob("fix")
	blocked, _ := st.CreateJob("fix")
	if err := st.FinishJob(blocked.ID, Finish{State: Blocked, Question: "which db?"}); err != nil {
		t.Fatal(err)
	}

	if err := st.InterruptRunning("loopd restarted"); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]JobState{running.ID: Failed, queued.ID: Failed, blocked.ID: Blocked} {
		job, err := st.Job(id)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != want {
			t.Fatalf("job %s: state %s, want %s", id, job.State, want)
		}
		if want == Failed && job.Error != "loopd restarted" {
			t.Fatalf("job %s: error %q", id, job.Error)
		}
	}
}

func TestFinishAddsCostAcrossRuns(t *testing.T) {
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	job, _ := st.CreateJob("fix")
	if err := st.FinishJob(job.ID, Finish{State: Manual, CostUSD: 1.5}); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishJob(job.ID, Finish{State: Done, CostUSD: 0.75}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Job(job.ID); got.CostUSD != 2.25 {
		t.Fatalf("cost %v, want 2.25 over both runs", got.CostUSD)
	}
}

func TestUnclaimLetsAnIssueRunAgain(t *testing.T) {
	st := open(t, filepath.Join(t.TempDir(), "state.db"))
	if fresh, _ := st.ClaimIssue("fix", 3); !fresh {
		t.Fatal("first claim not fresh")
	}
	if err := st.UnclaimIssue("fix", 3); err != nil {
		t.Fatal(err)
	}
	if fresh, _ := st.ClaimIssue("fix", 3); !fresh {
		t.Fatal("claim after unclaim not fresh")
	}
}

// integerSchema is a state database from before random ids, with three Jobs.
const integerSchema = `
CREATE TABLE loops (name TEXT PRIMARY KEY, paused INTEGER NOT NULL DEFAULT 0);
CREATE TABLE jobs (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	loop     TEXT NOT NULL,
	state    TEXT NOT NULL,
	work     TEXT NOT NULL DEFAULT '',
	branch   TEXT NOT NULL DEFAULT '',
	worktree TEXT NOT NULL DEFAULT '',
	base     TEXT NOT NULL DEFAULT '',
	issue    INTEGER NOT NULL DEFAULT 0,
	question TEXT NOT NULL DEFAULT '',
	summary  TEXT NOT NULL DEFAULT '',
	pr_url   TEXT NOT NULL DEFAULT '',
	cost_usd REAL NOT NULL DEFAULT 0,
	error    TEXT NOT NULL DEFAULT ''
);
CREATE TABLE issues (loop TEXT NOT NULL, number INTEGER NOT NULL, PRIMARY KEY (loop, number));
INSERT INTO loops (name, paused) VALUES ('fix', 1);
INSERT INTO issues (loop, number) VALUES ('fix', 42);
INSERT INTO jobs (id, loop, state, work, branch, worktree, base, issue, question, summary, pr_url, cost_usd, error) VALUES
	(1, 'fix', 'done', 'issue 42 Fix login', 'loop/fix-42-1', '/h/jobs/1/worktree', 'abc', 42, '', 'fixed', 'https://github.com/me/app/pull/1', 1.5, ''),
	(2, 'fix', 'blocked', 'task x', 'loop/fix-20260101-000000-2', '/h/jobs/2/worktree', 'def', 0, 'which db?', '', '', 0.25, ''),
	(10, 'docs', 'failed', 'spec docs/specs/d.md', '', '', '', 0, '', '', '', 0, 'fetch failed');`

func TestConvertIntegerIDsGivesEachOldJobANewIDOnceAndKeepsEverythingElse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(integerSchema); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	st := open(t, path)
	jobs, err := st.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	want := []Job{
		{Loop: "docs", State: Failed, Work: "spec docs/specs/d.md", Error: "fetch failed"},
		{Loop: "fix", State: Blocked, Work: "task x", Branch: "loop/fix-20260101-000000-2", Worktree: "/h/jobs/2/worktree", Base: "def", Question: "which db?", CostUSD: 0.25},
		{Loop: "fix", State: Done, Work: "issue 42 Fix login", Branch: "loop/fix-42-1", Worktree: "/h/jobs/1/worktree", Base: "abc", Issue: 42, Summary: "fixed", PRURL: "https://github.com/me/app/pull/1", CostUSD: 1.5},
	}
	if len(jobs) != len(want) {
		t.Fatalf("jobs %+v, want %d", jobs, len(want))
	}
	for i, n := range []int{10, 2, 1} {
		id := jobs[i].ID
		if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(id) {
			t.Fatalf("job %d got id %q", n, id)
		}
		if line := fmt.Sprintf("job %d is now %s\n", n, id); !strings.Contains(logged.String(), line) {
			t.Fatalf("log lacks %q:\n%s", line, logged.String())
		}
		want[i].ID = id
		if jobs[i] != want[i] {
			t.Fatalf("job %d read back as %+v\nwant %+v", n, jobs[i], want[i])
		}
	}
	if paused, _ := st.Paused("fix"); !paused {
		t.Fatal("conversion lost the paused loop")
	}
	if fresh, _ := st.ClaimIssue("fix", 42); fresh {
		t.Fatal("conversion lost the claimed issue")
	}
	next, err := st.CreateJob("fix")
	if err != nil {
		t.Fatal(err)
	}
	if jobs, _ := st.Jobs(); jobs[0].ID != next.ID {
		t.Fatalf("a job created after conversion is not the newest: %+v", jobs)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	logged.Reset()
	again, err := open(t, path).Jobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 4 || again[0].ID != next.ID || again[1] != want[0] || again[2] != want[1] || again[3] != want[2] {
		t.Fatalf("second open changed the jobs: %+v", again)
	}
	if logged.Len() != 0 {
		t.Fatalf("second open logged %q", logged.String())
	}
}
