package web

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yeremi777/loopgate/internal/daemon"
	"github.com/yeremi777/loopgate/internal/store"
)

func TestTrackPortIsPickedFreeOnTheFirstStartAndReusedLater(t *testing.T) {
	home := t.TempDir()
	ln, p := listen(t, home)
	if p.Port == 0 || ln.Addr().String() != "127.0.0.1:"+strconv.Itoa(p.Port) {
		t.Fatalf("listening on %s, pages %+v; want 127.0.0.1 at the pages' port", ln.Addr(), p)
	}
	if kept := readFile(t, filepath.Join(home, "track.port")); kept != strconv.Itoa(p.Port)+"\n" {
		t.Fatalf("kept port %q, want %d", kept, p.Port)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if _, again := listen(t, home); again.Port != p.Port {
		t.Fatalf("second start on port %d, want the kept %d", again.Port, p.Port)
	}
}

func TestTrackPortThatIsTakenMovesToAFreeOneKeepsItAndLogsIt(t *testing.T) {
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	home := t.TempDir()
	_, first := listen(t, home)
	_, moved := listen(t, home)
	if moved.Port == first.Port || moved.Port == 0 {
		t.Fatalf("second listener on port %d while %d is taken", moved.Port, first.Port)
	}
	if kept := readFile(t, filepath.Join(home, "track.port")); kept != strconv.Itoa(moved.Port)+"\n" {
		t.Fatalf("kept port %q, want the new %d", kept, moved.Port)
	}
	if want := "tracking port " + strconv.Itoa(first.Port) + " is taken; the tracking pages are now on port " + strconv.Itoa(moved.Port); !strings.Contains(logged.String(), want) {
		t.Fatalf("log %q lacks %q", logged.String(), want)
	}
}

func TestTrackTokenIsRandomKeptPrivateAndReused(t *testing.T) {
	home := t.TempDir()
	ln, p := listen(t, home)
	if !regexp.MustCompile(`^[0-9a-f]{32,}$`).MatchString(p.Token) {
		t.Fatalf("token %q is not at least 128 bits of hex", p.Token)
	}
	file := filepath.Join(home, "track.token")
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file %v, %v; want mode 0600", info, err)
	}
	if kept := readFile(t, file); strings.TrimSpace(kept) != p.Token {
		t.Fatalf("kept token %q, want %q", kept, p.Token)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if _, again := listen(t, home); again.Token != p.Token {
		t.Fatalf("second start has token %q, want the kept %q", again.Token, p.Token)
	}
	if _, other := listen(t, t.TempDir()); other.Token == p.Token {
		t.Fatal("two homes made the same token")
	}
}

func TestTrackTokenIsRenewedWhenTheKeptPortIsTakenAndKeptWhenItBinds(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, "track.token")
	_, first := listen(t, home)
	moved, p := listen(t, home)
	if p.Port == first.Port || p.Token == first.Token {
		t.Fatalf("taken port %d moved to %+v; want a new port and a new token", first.Port, p)
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file %v, %v; want mode 0600", info, err)
	}
	if kept := readFile(t, file); strings.TrimSpace(kept) != p.Token || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(p.Token) {
		t.Fatalf("kept token %q, pages' token %q; want the new 256-bit token kept", kept, p.Token)
	}
	if err := moved.Close(); err != nil {
		t.Fatal(err)
	}
	if _, again := listen(t, home); again.Port != p.Port || again.Token != p.Token {
		t.Fatalf("start on the free kept port has %+v, want the kept %+v", again, p)
	}
}

// forbidden is the body of every 403.
const forbidden = "forbidden: run loopctl track <job> for this page's current link\n"

func TestTrackTokenIsRequiredOnEveryRequest(t *testing.T) {
	p := serve(t)
	base := "http://127.0.0.1:" + strconv.Itoa(p.Port)
	for _, path := range []string{"/", "/?token=", "/?token=" + strings.Repeat("0", len(p.Token)), "/track.css", "/jobs/0123456789ab", "/jobs/0123456789ab/events"} {
		res := get(t, base+path, "")
		if body := readBody(t, res); res.StatusCode != http.StatusForbidden || body != forbidden {
			t.Errorf("GET %s: %d %q, want 403 %q", path, res.StatusCode, body, forbidden)
		}
	}
	if code := get(t, p.URL("/"), "").StatusCode; code != http.StatusOK {
		t.Fatalf("GET / with the token: %d, want 200", code)
	}
}

func TestTrackHostOtherThanTheListenersIsRefused(t *testing.T) {
	p := serve(t)
	port := strconv.Itoa(p.Port)
	for _, host := range []string{"localhost:" + port, "127.0.0.1", "127.0.0.1:1", "evil.example:" + port} {
		res := get(t, p.URL("/"), host)
		if body := readBody(t, res); res.StatusCode != http.StatusForbidden || body != forbidden {
			t.Errorf("Host %s: %d %q, want 403 %q", host, res.StatusCode, body, forbidden)
		}
	}
}

func TestTrackOnlyGetIsAnswered(t *testing.T) {
	p := serve(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodHead, http.MethodOptions} {
		req, err := http.NewRequest(method, p.URL("/"), nil)
		if err != nil {
			t.Fatal(err)
		}
		res := do(t, req)
		if res.StatusCode != http.StatusMethodNotAllowed || res.Header.Get("Allow") != http.MethodGet {
			t.Errorf("%s /: %d, Allow %q; want 405 allowing GET", method, res.StatusCode, res.Header.Get("Allow"))
		}
	}
}

func TestTrackCSPLetsAPageLoadAndConnectOnlyToTheListener(t *testing.T) {
	p := serve(t)
	base := "http://127.0.0.1:" + strconv.Itoa(p.Port)
	for _, res := range []*http.Response{get(t, p.URL("/"), ""), get(t, base+"/", "")} {
		csp := res.Header.Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "connect-src 'self'", "img-src 'self'", "base-uri 'none'", "form-action 'none'", "frame-ancestors 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%d response CSP %q lacks %q", res.StatusCode, csp, want)
			}
		}
		if strings.Contains(csp, "unsafe") || strings.Contains(csp, "*") || strings.Contains(csp, "http") {
			t.Errorf("CSP %q lets a page reach beyond the listener", csp)
		}
		if res.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("Referrer-Policy %q; a link off the page would carry the token", res.Header.Get("Referrer-Policy"))
		}
	}
}

func listen(t *testing.T, home string) (net.Listener, Pages) {
	t.Helper()
	ln, p, err := Listen(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln, p
}

// serve answers the pages of a loopd with no Loops and no Jobs.
func serve(t *testing.T) Pages {
	t.Helper()
	p, _ := serveJobs(t, nil)
	return p
}

// pagesEnv is a loopd home whose store the test fills before it reads the pages.
type pagesEnv struct {
	home string
	st   *store.Store
}

// serveJobs answers the pages of a loopd with the given Loop files, by name.
func serveJobs(t *testing.T, loops map[string]string) (Pages, pagesEnv) {
	t.Helper()
	home := t.TempDir()
	for name, loop := range loops {
		write(t, filepath.Join(home, "loops", name+".yaml"), loop)
	}
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	d, err := daemon.New(home, st, daemon.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	ln, p := listen(t, home)
	go func() { _ = Serve(ln, d, p) }()
	return p, pagesEnv{home: home, st: st}
}

// job stores a Job of loop that started in its directory and ended as end.
func (e pagesEnv) job(t *testing.T, loop, work string, end store.Finish) store.Job {
	t.Helper()
	j, err := e.st.CreateJob(loop)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(e.home, "jobs", j.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := e.st.StartJob(j.ID, store.Start{Work: work, Branch: "loop/" + loop + "-1-" + j.ID, Worktree: filepath.Join(dir, "worktree")}); err != nil {
		t.Fatal(err)
	}
	if end.State != "" {
		if err := e.st.FinishJob(j.ID, end); err != nil {
			t.Fatal(err)
		}
	}
	j, err = e.st.Job(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func timerLoop(name string) string {
	return "name: " + name + "\ncheckout: /src/app\ntrigger:\n  timer: { every: 1h }\ntask: tidy\nverify: \"true\"\nbounds: { max_iterations: 4, timeout: 1m }\n"
}

func TestIndexListsEachLoopWithItsJobsLinkingToTheirPages(t *testing.T) {
	p, e := serveJobs(t, map[string]string{"deps": timerLoop("deps"), "idle": timerLoop("idle")})
	done := e.job(t, "deps", "task tidy", store.Finish{State: store.Done, PRURL: "https://github.com/me/app/pull/9"})
	blocked := e.job(t, "deps", "task <b>bold</b>", store.Finish{State: store.Blocked, Question: "<script>alert(1)</script>"})
	orphan := e.job(t, "gone", "task old", store.Finish{State: store.Failed, Error: "fetch failed"})
	e.job(t, "deps", "task mine", store.Finish{State: store.Manual})

	res := get(t, p.URL("/"), "")
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET /: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	for _, want := range []string{
		">deps<", ">idle<", ">gone<", "removed", "no jobs", "timer", "/src/app",
		`href="/jobs/` + done.ID + `?token=` + p.Token + `"`, ">" + done.ID + "<", "https://github.com/me/app/pull/9",
		`href="/jobs/` + blocked.ID + `?token=` + p.Token + `"`, "&lt;b&gt;bold&lt;/b&gt;", "&lt;script&gt;alert(1)&lt;/script&gt;",
		`href="/jobs/` + orphan.ID + `?token=` + p.Token + `"`, "fetch failed",
		`href="/track.css?token=` + p.Token + `"`,
		`<span class="chip s-manual">taken over</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index lacks %q", want)
		}
	}
	if n := strings.Count(body, "taken over"); n != 1 {
		t.Errorf("index says taken over %d times, want once, in the job's chip", n)
	}
	if strings.Contains(body, "<script>alert") || strings.Contains(body, "<b>bold") {
		t.Fatalf("index put a job's text in as markup:\n%s", body)
	}
	if i, j := strings.Index(body, ">deps<"), strings.Index(body, ">gone<"); i > j {
		t.Fatalf("loops not sorted by name:\n%s", body)
	}
}

func TestJobPageIsTheShellThatLoadsTheScriptAndStyleWithTheToken(t *testing.T) {
	p, e := serveJobs(t, nil)
	j := e.job(t, "deps", "task tidy", store.Finish{})
	res := get(t, p.URL("/jobs/"+strings.ToUpper(j.ID[:5])), "")
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("job page: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	for _, want := range []string{`data-job="` + j.ID + `"`, `src="/track.js?token=` + p.Token + `"`, `href="/track.css?token=` + p.Token + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("job page lacks %q:\n%s", want, body)
		}
	}
	for path, kind := range map[string]string{"/track.js": "javascript", "/track.css": "text/css"} {
		res := get(t, p.URL(path), "")
		if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Content-Type"), kind) || len(readBody(t, res)) == 0 {
			t.Errorf("%s: %d %s", path, res.StatusCode, res.Header.Get("Content-Type"))
		}
	}
	if code := get(t, p.URL("/jobs/ffffffffffff"), "").StatusCode; code != http.StatusNotFound {
		t.Fatalf("page of no job: %d, want 404", code)
	}
}

func TestJobDataHasTheJobItsTimesItsLoopsBoundAndItsStages(t *testing.T) {
	p, e := serveJobs(t, map[string]string{"deps": timerLoop("deps")})
	j := e.job(t, "deps", "task tidy", store.Finish{State: store.Exhausted, Summary: "4 implement runs did not pass the checks"})
	write(t, filepath.Join(e.home, "jobs", j.ID, "stages.jsonl"),
		`{"stage":"implement 1","start":"2026-10-20T10:00:00Z","end":"2026-10-20T10:03:00Z","status":"done","cost_usd":0.5}`+"\n")
	res := get(t, p.URL("/jobs/"+j.ID+"/data"), "")
	var tr daemon.Track
	if err := json.Unmarshal([]byte(readBody(t, res)), &tr); err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("data: %d, %v", res.StatusCode, err)
	}
	if tr.Job.ID != j.ID || tr.Job.State != store.Exhausted || tr.Job.Started.IsZero() || tr.Job.Ended.IsZero() || tr.MaxIterations != 4 {
		t.Fatalf("data %+v", tr)
	}
	if len(tr.Stages) != 1 || tr.Stages[0].Stage != "implement 1" || tr.Stages[0].CostUSD != 0.5 || tr.Stages[0].End.Sub(tr.Stages[0].Start) != 3*time.Minute {
		t.Fatalf("stages %+v", tr.Stages)
	}
}

func TestPagesAndTheirScriptCarryNoEmDash(t *testing.T) {
	p, e := serveJobs(t, map[string]string{"deps": timerLoop("deps")})
	j := e.job(t, "deps", "task tidy", store.Finish{})
	e.job(t, "deps", "task ship", store.Finish{State: store.Done, PRURL: "https://github.com/me/app/pull/9"})
	for _, path := range []string{"/", "/jobs/" + j.ID, "/track.js", "/track.css"} {
		if body := readBody(t, get(t, p.URL(path), "")); strings.ContainsRune(body, '\u2014') {
			t.Errorf("%s carries an em dash, which DESIGN.md leaves off the pages", path)
		}
	}
}

func TestEventsOfAnEndedJobEndAtOnceWithTheJob(t *testing.T) {
	p, e := serveJobs(t, nil)
	j := e.job(t, "deps", "task tidy", store.Finish{State: store.Done, PRURL: "https://github.com/me/app/pull/9"})
	body := readBody(t, get(t, p.URL("/jobs/"+j.ID+"/events"), ""))
	var line struct {
		Job *store.Job `json:"job"`
	}
	if err := json.Unmarshal([]byte(body), &line); err != nil || line.Job == nil || line.Job.State != store.Done {
		t.Fatalf("events of a done job: %q, %v", body, err)
	}
}

func get(t *testing.T, url, host string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	return do(t, req)
}

func do(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func readBody(t *testing.T, res *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
