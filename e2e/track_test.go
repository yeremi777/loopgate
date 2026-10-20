package e2e

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTrackOpensTheJobsPageAndASecondJobsPageOnTheSamePort(t *testing.T) {
	e, first := startStreamJob(t, "0")
	opened := fakeOpen(t, e.env, "")
	e.waitEnded(t, first)

	url := e.track(t, first[:6], first)
	if got := readFile(t, opened); got != url+"\n" {
		t.Fatalf("open got %q, want %q", got, url)
	}
	if page := fetch(t, url); !strings.Contains(page, `data-job="`+first+`"`) {
		t.Fatalf("page at %s:\n%s", url, page)
	}

	m := regexp.MustCompile(`job ([0-9a-f]{12}) running`).FindStringSubmatch(e.mustLoopctl("run", "deps"))
	if m == nil {
		t.Fatal("second run printed no job id")
	}
	second := m[1]
	other := e.track(t, second, second)
	base := regexp.MustCompile(`^http://127\.0\.0\.1:\d+/`).FindString(url)
	if !strings.HasPrefix(other, base) || other == url {
		t.Fatalf("second job's page %s is not another page at %s", other, base)
	}
	if got := readFile(t, opened); got != url+"\n"+other+"\n" {
		t.Fatalf("open got %q, want both pages", got)
	}

	index := fetch(t, strings.Replace(url, "/jobs/"+first, "/", 1))
	token := url[strings.Index(url, "?token="):]
	for _, id := range []string{first, second} {
		if !strings.Contains(index, `href="/jobs/`+id+token+`"`) {
			t.Fatalf("index lacks a link to job %s:\n%s", id, index)
		}
	}
	if !strings.Contains(index, ">deps<") {
		t.Fatalf("index lacks the loop:\n%s", index)
	}
}

func TestTrackPrintsTheURLAndExitsZeroWhenOpenFails(t *testing.T) {
	e, id := startStreamJob(t, "0")
	fakeOpen(t, e.env, "echo 'no browser' >&2; exit 1")
	out := e.mustLoopctl("track", id)
	url := regexp.MustCompile(`http://127\.0\.0\.1:\d+/jobs/` + id + `\?token=[0-9a-f]+`).FindString(out)
	if url == "" || !strings.HasPrefix(out, "tracking job "+id+" at "+url+"\n") || strings.Count(out, url) < 2 || !strings.Contains(out, "no browser") {
		t.Fatalf("track when open fails:\n%s", out)
	}
}

func TestTrackPageOfAnEndedJobShowsItsStagesAgainAfterARestart(t *testing.T) {
	e, id := startStreamJob(t, "0")
	fakeOpen(t, e.env, "")
	e.waitEnded(t, id)
	url := e.track(t, id, id)
	data := strings.Replace(url, "?token=", "/data?token=", 1)

	stagesOf := func() string {
		var tr struct {
			Job struct {
				State   string
				Started time.Time
				Ended   time.Time
			} `json:"job"`
			MaxIterations int `json:"max_iterations"`
			Stages        []struct {
				Stage  string `json:"stage"`
				Status string `json:"status"`
			} `json:"stages"`
		}
		if err := json.Unmarshal([]byte(fetch(t, data)), &tr); err != nil {
			t.Fatal(err)
		}
		if tr.Job.State != "done" || tr.Job.Started.IsZero() || tr.Job.Ended.Before(tr.Job.Started) || tr.MaxIterations != 2 {
			t.Fatalf("data of the done job: %+v", tr)
		}
		var stages []string
		for _, s := range tr.Stages {
			stages = append(stages, s.Stage+" "+s.Status)
		}
		return strings.Join(stages, ", ")
	}
	want := "implement 1 done, verify 1 pass, rubric 1 done, land done, publish done"
	if got := stagesOf(); got != want {
		t.Fatalf("stages %q, want %q", got, want)
	}
	events := fetch(t, strings.Replace(url, "?token=", "/events?token=", 1))
	if !strings.HasPrefix(events, `{"job":`) {
		t.Fatalf("events of an ended job: %q; want only the job", events)
	}

	e.restart(t)
	if page := fetch(t, url); !strings.Contains(page, `data-job="`+id+`"`) {
		t.Fatalf("page after restart:\n%s", page)
	}
	if got := stagesOf(); got != want {
		t.Fatalf("stages after restart %q, want %q", got, want)
	}
}

func TestTrackEventsEndWithoutTheJobWhenLoopdStopsAndTheURLWorksAfterItStarts(t *testing.T) {
	e, id := startStreamJob(t, "10")
	fakeOpen(t, e.env, "")
	url := e.track(t, id, id)
	res, err := http.Get(strings.Replace(url, "?token=", "/events?token=", 1))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	lines := bufio.NewScanner(res.Body)
	for lines.Scan() && !strings.Contains(lines.Text(), `"tool":"Write"`) {
	}
	if err := e.loopd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(res.Body)
	if strings.Contains(string(rest), `{"job":`) || !strings.Contains(string(rest), "loopd stopped") {
		t.Fatalf("events after loopd stopped: %q; want loopd stopped and no job", rest)
	}
	_ = e.loopd.Wait()

	e.startLoopd()
	if page := fetch(t, url); !strings.Contains(page, `data-job="`+id+`"`) {
		t.Fatalf("page after loopd started again:\n%s", page)
	}
}

func TestHelpNamesTrackOnItsOwnLine(t *testing.T) {
	out, code := newEnv(t, "exit 0").loopctl("help")
	if code != 0 || strings.Count(out, "loopctl track") != 1 || !strings.Contains(out, "\n  loopctl track <job> ") {
		t.Fatalf("help lacks one loopctl track <job> line:\n%s", out)
	}
}

// fakeOpen puts an open on e's PATH that runs body after it writes its
// arguments to the file it returns.
func fakeOpen(t *testing.T, e env, body string) string {
	t.Helper()
	opened := filepath.Join(t.TempDir(), "opened")
	fakes := strings.SplitN(e.path, ":", 2)[0]
	writeExec(t, filepath.Join(fakes, "open"), "#!/bin/sh\necho \"$*\" >> "+opened+"\n"+body+"\n")
	return opened
}

// track runs loopctl track on arg and returns the page's URL, which it
// checks names Job id.
func (e streamEnv) track(t *testing.T, arg, id string) string {
	t.Helper()
	out := e.mustLoopctl("track", arg)
	m := regexp.MustCompile(`^tracking job ` + id + ` at (http://127\.0\.0\.1:\d+/jobs/` + id + `\?token=[0-9a-f]{32,})\n$`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("track %s printed %q", arg, out)
	}
	return m[1]
}

func (e streamEnv) waitEnded(t *testing.T, id string) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		out := e.mustLoopctl("logs", id)
		if !regexp.MustCompile(`(?m)^state:\s+(running|queued)$`).MatchString(out) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s did not end:\n%s", id, out)
		}
	}
}

// restart stops loopd with SIGTERM and starts it again on the same home.
func (e *streamEnv) restart(t *testing.T) {
	t.Helper()
	if err := e.loopd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := e.loopd.Wait(); err != nil {
		t.Fatalf("loopd exit after SIGTERM: %v", err)
	}
	e.loopd = e.startLoopd()
}

func fetch(t *testing.T, url string) string {
	t.Helper()
	res, err := (&http.Client{Timeout: 10 * time.Second}).Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %s: %s", url, res.Status, body)
	}
	return string(body)
}
