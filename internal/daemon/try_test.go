package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yeremi777/loopgate/internal/gittest"
	"github.com/yeremi777/loopgate/internal/store"
)

func TestTryCopiesTheIncludedIgnoredFilesIntoTheDoneJobsOwnWorktree(t *testing.T) {
	r, j := triedEnv(t)
	writeFile(filepath.Join(r.checkout, ".worktreeinclude"), ".env\nconfig/\n*.local\n!skip.local\nnotes.txt\n")
	writeFile(filepath.Join(r.checkout, ".env"), "SECRET=1\n")
	if err := os.Chmod(filepath.Join(r.checkout, ".env"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(filepath.Join(r.checkout, "config/local.json"), `{"db":"x"}`)
	writeFile(filepath.Join(r.checkout, "secret.key"), "not included")
	writeFile(filepath.Join(r.checkout, "skip.local"), "excluded by !skip.local")
	writeFile(filepath.Join(r.checkout, "notes.txt"), "not ignored")
	if err := os.Symlink(".env", filepath.Join(r.checkout, "link.local")); err != nil {
		t.Fatal(err)
	}
	head := runGit(t, j.Worktree, "rev-parse", "HEAD")

	tried, err := r.d.Try(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".env", "config/local.json"}; !slices.Equal(tried.Copied, want) {
		t.Fatalf("copied %q, want %q", tried.Copied, want)
	}
	if tried.Job.ID != j.ID || tried.Job.Worktree != j.Worktree || tried.Job.TestGuide != "go run ./cmd/app, then GET /ok" {
		t.Fatalf("tried job %+v", tried.Job)
	}
	if got := readFile(t, filepath.Join(j.Worktree, "config/local.json")); got != `{"db":"x"}` {
		t.Fatalf("config/local.json in the worktree %q", got)
	}
	writeFile(filepath.Join(j.Worktree, ".env"), "stale")
	if _, err := r.d.Try(j.ID); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(j.Worktree, ".env")); got != "SECRET=1\n" {
		t.Fatalf(".env in the worktree %q, want the checkout's copy over the earlier one", got)
	}
	if info, err := os.Stat(filepath.Join(j.Worktree, ".env")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf(".env in the worktree: %v, %v; want mode 0600", info, err)
	}
	for _, kept := range []string{"secret.key", "skip.local", "notes.txt", "link.local", ".worktreeinclude"} {
		if _, err := os.Lstat(filepath.Join(j.Worktree, kept)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s reached the worktree: %v", kept, err)
		}
	}
	if now := runGit(t, j.Worktree, "rev-parse", "HEAD"); now != head {
		t.Fatalf("head %s after try, want %s", now, head)
	}
	if status := runGit(t, j.Worktree, "status", "--porcelain"); status != "" {
		t.Fatalf("worktree status after try %q", status)
	}
}

func TestTryWithoutAWorktreeincludeCopiesNothing(t *testing.T) {
	r, j := triedEnv(t)
	writeFile(filepath.Join(r.checkout, ".env"), "SECRET=1\n")
	tried, err := r.d.Try(j.ID)
	if err != nil || len(tried.Copied) != 0 {
		t.Fatalf("tried %+v, %v; want nothing copied", tried, err)
	}
	if _, err := os.Stat(filepath.Join(j.Worktree, ".env")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".env reached the worktree: %v", err)
	}
}

func TestTryRefusesFilesTheJobsBranchDoesNotIgnoreAndNamesInfoExclude(t *testing.T) {
	r, j := triedEnv(t)
	writeFile(filepath.Join(r.checkout, ".worktreeinclude"), ".env\nconfig/\n")
	writeFile(filepath.Join(r.checkout, ".env"), "SECRET=1\n")
	writeFile(filepath.Join(r.checkout, "config/local.json"), "{}")
	writeFile(filepath.Join(j.Worktree, ".gitignore"), "config/local.json\n")
	gittest.Run(t, j.Worktree, "commit", "-q", "-am", "stop ignoring .env")

	exclude := runGit(t, r.checkout, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	_, err := r.d.Try(j.ID)
	want := "job " + j.ID + ": its branch " + j.Branch + " does not ignore .env, so a copy could be committed, and nothing was copied; to copy these files, add them to " + Tilde(exclude)
	if err == nil || err.Error() != want {
		t.Fatalf("err %v, want %q", err, want)
	}
	for _, name := range []string{".env", "config/local.json"} {
		if _, err := os.Stat(filepath.Join(j.Worktree, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s reached the worktree: %v", name, err)
		}
	}

	writeFile(exclude, ".env\n")
	if tried, err := r.d.Try(j.ID); err != nil || !slices.Equal(tried.Copied, []string{".env", "config/local.json"}) {
		t.Fatalf("try once info/exclude lists .env: %+v, %v", tried, err)
	}
	if status := runGit(t, j.Worktree, "status", "--porcelain"); status != "" {
		t.Fatalf("worktree status after try %q", status)
	}
}

func TestTryRefusesFilesTheJobsBranchTracks(t *testing.T) {
	r, j := triedEnv(t)
	writeFile(filepath.Join(r.checkout, ".worktreeinclude"), ".env\n")
	writeFile(filepath.Join(r.checkout, ".env"), "SECRET=1\n")
	writeFile(filepath.Join(j.Worktree, ".env"), "COMMITTED=1\n")
	gittest.Run(t, j.Worktree, "add", "-f", ".env")
	gittest.Run(t, j.Worktree, "commit", "-q", "-m", "track .env")

	_, err := r.d.Try(j.ID)
	want := "job " + j.ID + ": its branch " + j.Branch + " tracks .env, so a copy would overwrite committed content, and nothing was copied; to try the job, remove them from " + Tilde(filepath.Join(r.checkout, ".worktreeinclude")) + " or from that branch"
	if err == nil || err.Error() != want {
		t.Fatalf("err %v, want %q", err, want)
	}
	if got := readFile(t, filepath.Join(j.Worktree, ".env")); got != "COMMITTED=1\n" {
		t.Fatalf(".env in the worktree %q, want the committed one", got)
	}
}

func TestTryRefusesAJobThatIsNotDoneNamingItsStateAndARemedy(t *testing.T) {
	r := newRunEnv(t)
	for _, tc := range []struct {
		state    store.JobState
		worktree string
		remedy   string
	}{
		{store.Queued, r.checkout, "; wait for it to end, following it with `loopctl logs -f %s`"},
		{store.Running, r.checkout, "; wait for it to end, following it with `loopctl logs -f %s`"},
		{store.Manual, r.checkout, "; to try it, hand it back with `loopctl handback %s` and try it once it is done"},
		{store.Blocked, r.checkout, "; to work in it, run `loopctl takeover %s`"},
		{store.Failed, r.checkout, "; to work in it, run `loopctl takeover %s`"},
		{store.Exhausted, r.checkout, "; to work in it, run `loopctl takeover %s`"},
		{store.Failed, "", "; it never got a worktree, so to remove it, run `loopctl rm %s`"},
	} {
		j, err := r.st.CreateJob("deps")
		if err != nil {
			t.Fatal(err)
		}
		if tc.worktree != "" {
			if err := r.st.StartJob(j.ID, store.Start{Work: "task x", Branch: "loop/deps-1", Worktree: tc.worktree}); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.st.SetJobState(j.ID, tc.state); err != nil {
			t.Fatal(err)
		}
		want := "job " + j.ID + " is " + string(tc.state) + ": only a done job can be tried" + fmt.Sprintf(tc.remedy, j.ID)
		if _, err := r.d.Try(j.ID); !errors.Is(err, ErrNotDone) || err.Error() != want {
			t.Fatalf("try of a %s job: %v, want %q", tc.state, err, want)
		}
	}
	if _, err := r.d.Try("999999999999"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("try of an unknown job: %v", err)
	}
}

func TestTryOfAFailedJobWithoutAWorktreeNamesRmWhichRemovesIt(t *testing.T) {
	r := newRunEnv(t)
	j, err := r.st.CreateJob("deps")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.st.SetJobState(j.ID, store.Failed); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.Try(j.ID); err == nil || !strings.Contains(err.Error(), "`loopctl rm "+j.ID+"`") {
		t.Fatalf("try of a failed job without a worktree: %v", err)
	}
	if err := os.MkdirAll(r.d.jobDir(j.ID), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.RemoveJob(j.ID); err != nil {
		t.Fatalf("rm of a failed job without a worktree: %v", err)
	}
	if _, err := r.st.Job(j.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("row of job %s stays: %v", j.ID, err)
	}
	if _, err := os.Stat(r.d.jobDir(j.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("directory of job %s stays: %v", j.ID, err)
	}
}

func TestTryOfAJobWhoseLoopWasRemovedNamesTheLoop(t *testing.T) {
	r, j := triedEnv(t)
	if err := r.d.Remove("deps"); err != nil {
		t.Fatal(err)
	}
	want := "job " + j.ID + ": loop deps: unknown loop; to try the job, add the loop again with `loopctl add <loop.yaml>`"
	if _, err := r.d.Try(j.ID); !errors.Is(err, ErrUnknownLoop) || err.Error() != want {
		t.Fatalf("try of a removed loop's job: %v, want %q", err, want)
	}

	loop := "name: deps\ncheckout: " + r.checkout + "\ntrigger:\n  timer: { every: 1h }\ntask: bump go dependencies\nverify: \"true\"\nbounds: { max_iterations: 3, timeout: 1m }\n"
	if _, err := r.d.Add(context.Background(), []byte(loop)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.Try(j.ID); err != nil {
		t.Fatalf("try once loop deps is added again: %v", err)
	}
}

func TestTryOfAJobWhoseWorktreeIsGoneNamesThePath(t *testing.T) {
	r, j := triedEnv(t)
	if err := os.RemoveAll(j.Worktree); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Dir(j.Worktree))
	want := "job " + j.ID + ": its worktree ~/" + filepath.Base(j.Worktree) + " is gone; to remove the job, run `loopctl rm " + j.ID + "`"
	if _, err := r.d.Try(j.ID); err == nil || err.Error() != want {
		t.Fatalf("try of a job with no worktree: %v, want %q", err, want)
	}

	if _, err := r.d.RemoveJob(j.ID); err != nil {
		t.Fatalf("rm of a job with no worktree: %v", err)
	}
	assertRemoved(t, r, j)
}

func TestTryFailsAtTheWorktreeWhenItCannotBeRead(t *testing.T) {
	r, j := triedEnv(t)
	jobDir := filepath.Dir(j.Worktree)
	if err := os.Chmod(jobDir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(jobDir, 0o700) })
	_, err := r.d.Try(j.ID)
	if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), j.Worktree) || strings.Contains(err.Error(), "check-ignore") {
		t.Fatalf("try of a job whose worktree cannot be read: %v", err)
	}
}

// triedEnv runs a deps Job to done on a checkout whose origin ignores .env,
// config/local.json, secret.key and *.local, with a test guide.
func triedEnv(t *testing.T) (*runEnv, store.Job) {
	t.Helper()
	r := newRunEnv(t)
	writeFile(filepath.Join(r.checkout, ".gitignore"), ".env\nconfig/local.json\nsecret.key\n*.local\n")
	writeFile(filepath.Join(r.checkout, "config/app.json"), "{}")
	gittest.Run(t, r.checkout, "add", ".gitignore", "config/app.json")
	gittest.Run(t, r.checkout, "commit", "-q", "-m", "ignore local files")
	gittest.Run(t, r.checkout, "push", "-q", "origin", "HEAD:main")
	r.stages.try = "go run ./cmd/app, then GET /ok"
	r.release()
	started, err := r.d.Run("deps", "")
	if err != nil {
		t.Fatal(err)
	}
	j := r.waitDone(t, started.ID)
	if j.State != store.Done {
		t.Fatalf("job %+v, want done", j)
	}
	return r, j
}
