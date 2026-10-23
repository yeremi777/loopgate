package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

const chromePath = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

func TestTrackPageFollowsTheJobLiveAndMovesToItsEndWithoutAReload(t *testing.T) {
	implementing, implemented := hold(t)
	publishing, published := hold(t)
	claude := strings.Replace(streamClaude, "sleep STAGE_SLEEP",
		`say '{"type":"text","text":"<img src=x onerror=alert(1)><b>bold</b>"}'; `+implementing, 1)
	e, _ := streamLoop(t, claude, "1m")
	writeExec(t, filepath.Join(strings.SplitN(e.path, ":", 2)[0], "gh"), "#!/bin/sh\n[ \"$1\" = auth ] && exit 0\n"+publishing+"\ncat >/dev/null\necho https://github.com/me/app/pull/9\n")
	id := e.runJob(t)
	fakeOpen(t, e.env, "")
	page := openPage(t, e.track(t, id, id))

	page.waitFor(t, `document.querySelector(".headline")?.textContent`, "Implementing, run 1 of 2")
	page.waitFor(t, `document.title`, "Implementing, run 1 of 2 · "+id)
	page.waitFor(t, `document.querySelector(".log .entry.newest .body")?.textContent`, "<img src=x onerror=alert(1)><b>bold</b>")
	if got := page.eval(t, `document.querySelector(".log .entry .body").textContent`); got != "Write(ok.txt)" {
		t.Fatalf("first log entry %q, want the tool call as Tool(input)", got)
	}
	if got := page.eval(t, `document.querySelectorAll(".log img, .log b").length`); got != "0" {
		t.Fatalf("the job's text became %s elements", got)
	}
	if got := page.eval(t, `[...document.querySelectorAll(".stages-row:last-child > span")].slice(0, 2).map(e => e.textContent).join(" ")`); got != "running Implement 1" {
		t.Fatalf("last stages row %q, want the running stage", got)
	}
	if got := page.eval(t, logTimeContrastAtLeast45); got != "true" {
		t.Fatalf("log time contrast is under 4.5:1 on the log background")
	}
	page.noEmDash(t)
	implemented()

	page.waitFor(t, `document.querySelector(".live .empty")?.textContent`, "Pushing the branch and opening the draft PR")
	published()
	page.waitFor(t, `document.querySelector(".headline")?.textContent`, "Draft PR opened")
	page.waitFor(t, `document.title`, "Draft PR opened · "+id)
	if got := page.eval(t, `document.querySelector(".pr-link").textContent + " | " + document.querySelectorAll(".live").length`); got != "Open PR #9 | 0" {
		t.Fatalf("done page: %q, want the PR link and no live section", got)
	}
	if got := page.eval(t, `[...document.querySelectorAll(".stages-row:last-child > span")].slice(0, 2).map(e => e.textContent).join(" ")`); got != "done PR" {
		t.Fatalf("last stages row %q, want the publish stage named PR as in the stepper", got)
	}
	page.noEmDash(t)
}

func TestTrackPageSaysLoopdStoppedWhenLoopdStops(t *testing.T) {
	e, id := startStreamJob(t, "10")
	fakeOpen(t, e.env, "")
	page := openPage(t, e.track(t, id, id))
	page.waitFor(t, `document.querySelector(".headline")?.textContent`, "Implementing, run 1 of 2")
	if err := e.loopd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	page.waitFor(t, `document.querySelector(".notice")?.textContent`, "loopd stopped · reload this page; if it does not load, run loopctl install")
	page.waitFor(t, `document.title`, "loopd stopped · "+id)
	if got := page.eval(t, `document.querySelector(".state-label").textContent + " | " + document.querySelectorAll(".stages .s-running, .live").length`); got != "last seen running | 0" {
		t.Fatalf("once loopd stopped: %q, want a stale card and no running row or live section", got)
	}
}

func TestTrackPageOfAHandedBackJobNamesTheStageItRunsAgain(t *testing.T) {
	e, id := startStreamJob(t, "10")
	fakeOpen(t, e.env, "")
	url := e.track(t, id, id)
	e.mustLoopctl("takeover", id)
	e.mustLoopctl("handback", id)
	page := openPage(t, url)
	page.waitFor(t, `document.querySelector(".headline")?.textContent`, "Implementing, run 1 of 2")
	page.waitFor(t, `document.querySelector(".live .section-head .muted")?.textContent`, "Implement 1")
}

func TestTrackPageKeepsARetriedGatePendingWhileARetryRunsAndFailsItOnceTheJobEnds(t *testing.T) {
	type step struct {
		at   int
		note string
	}
	revise := `printf '{"type":"result","is_error":false,"total_cost_usd":0.1,"structured_output":{"status":"revise","summary":"fix it"}}\n'`
	for _, tc := range []struct {
		name, implement2, rest, rubric string
		retried                        step
		ended                          []step
	}{
		{"verify", `result "missed ok.txt"`, `result "missed ok.txt"; exit 0`, `result "clean"`, step{2, "verify 1 failed"}, []step{{2, "verify 2 failed"}}},
		{"rubric", `result "kept ok"`, "sleep 0", revise, step{3, "rubric 1 revise"}, []step{{3, "rubric 2 revise"}}},
		{"rubric then verify", `git rm -q ok.txt && git -c user.name=t -c user.email=t@example.com commit -q -m rm && result "removed ok"`, "sleep 0", revise, step{3, "rubric 1 revise"}, []step{{2, "verify 2 failed"}, {3, "rubric 1 revise"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			retrying, retried := hold(t)
			claude := strings.NewReplacer(
				"sleep STAGE_SLEEP", `case "$prompt" in *"did not pass"*) `+retrying+`; `+tc.implement2+`; exit 0;; esac; `+tc.rest,
				`result "clean"`, tc.rubric).Replace(streamClaude)
			e, id := startJobOf(t, claude)
			fakeOpen(t, e.env, "")
			page := openPage(t, e.track(t, id, id))
			state := func(at int) string {
				s := fmt.Sprintf(`document.querySelectorAll(".step")[%d]`, at)
				return page.eval(t, s+`.className + " | " + `+s+`.querySelector(".step-note").textContent + " | " + `+s+`.querySelectorAll(".node svg").length + " | " + getComputedStyle(`+s+`.querySelector(".step-note")).color`)
			}

			page.waitFor(t, `document.querySelector(".headline")?.textContent`, "Implementing, run 2 of 2")
			if got, want := state(tc.retried.at), "step st-pending | "+tc.retried.note+" | 0 | rgb(248, 81, 73)"; got != want {
				t.Fatalf("step %d while implement 2 runs: %q, want a pending node with a red note: %q", tc.retried.at, got, want)
			}
			page.noEmDash(t)
			retried()

			page.waitFor(t, `document.querySelector(".headline")?.textContent`, "Out of attempts")
			for _, s := range tc.ended {
				if got, want := state(s.at), "step st-failed | "+s.note+" | 1 | rgb(248, 81, 73)"; got != want {
					t.Fatalf("step %d once the job ended: %q, want a failed node with its x and red note: %q", s.at, got, want)
				}
			}
			if got := page.eval(t, `document.querySelector(".detail .cmd")?.textContent`); got != "loopctl takeover "+id {
				t.Fatalf("exhausted card command %q, want loopctl takeover %s", got, id)
			}
			page.noEmDash(t)
		})
	}
}

func TestTrackPageOpensTheLastFailedGateAndKeepsWhatTheUserToggles(t *testing.T) {
	retrying, retried := hold(t)
	claude := strings.NewReplacer(
		"sleep STAGE_SLEEP", `case "$prompt" in *"did not pass"*) `+retrying+`; result "missed ok.txt"; exit 0;; esac; result "missed ok.txt"; exit 0`,
		`result "clean"`, `result "clean"`).Replace(streamClaude)
	e, id := startJobOf(t, claude)
	fakeOpen(t, e.env, "")
	page := openPage(t, e.track(t, id, id))
	gates := `[...document.querySelectorAll(".stages details")].map(d => d.open).join()`
	page.waitFor(t, `document.querySelector(".headline")?.textContent`, "Implementing, run 2 of 2")
	page.waitFor(t, gates, "true")
	retried()
	page.waitFor(t, `document.querySelector(".headline")?.textContent`, "Out of attempts")
	page.waitFor(t, gates, "false,true")
	if got := page.eval(t, `document.querySelector(".stages details[open] .gate-out")?.textContent.length > 0`); got != "true" {
		t.Fatalf("the open gate shows no output")
	}
	page.eval(t, `document.querySelector(".stages details[open] summary").click()`)
	page.eval(t, `document.querySelectorAll(".stages summary")[0].click()`)
	page.eval(t, `render()`)
	if got := page.eval(t, gates); got != "true,false" {
		t.Fatalf("gates open after the user toggled both and the page redrew: %s, want true,false", got)
	}
}

func TestTrackPageOfAFailedJobWaitsForTheStagesFirstToolCallThenShowsTheErrorAndTakeover(t *testing.T) {
	failing, fail := hold(t)
	e, id := startJobOf(t, "#!/bin/sh\n"+failing+"\nexit 1\n")
	fakeOpen(t, e.env, "")
	page := openPage(t, e.track(t, id, id))
	page.waitFor(t, `document.querySelector(".live .empty")?.textContent`, "Waiting for the first tool call of Implement 1")
	fail()
	page.waitFor(t, `document.querySelector(".headline")?.textContent`, "Job failed")
	if got := page.eval(t, `document.querySelector(".detail .cmd")?.textContent`); got != "loopctl takeover "+id {
		t.Fatalf("failed card command %q, want loopctl takeover %s", got, id)
	}
	page.noEmDash(t)
}

func TestTrackPageCopiesTheCommandOfItsCardWithTheCopyButton(t *testing.T) {
	failing, fail := hold(t)
	e, id := startJobOf(t, "#!/bin/sh\n"+failing+"\nexit 1\n")
	fakeOpen(t, e.env, "")
	page := openPage(t, e.track(t, id, id))
	fail()
	page.waitFor(t, `document.querySelector(".headline")?.textContent`, "Job failed")
	page.eval(t, `(window.copied = [], navigator.clipboard.writeText = s => (window.copied.push(s), Promise.resolve()))`)
	page.waitFor(t, `document.querySelector(".detail .copy")?.getAttribute("aria-label")`, "Copy command")
	page.eval(t, `document.querySelector(".detail .copy").click()`)
	page.waitFor(t, `window.copied.join("|")`, "loopctl takeover "+id)
	page.waitFor(t, `document.querySelector(".detail .copy").getAttribute("aria-label")`, "Copied")
	page.eval(t, `navigator.clipboard.writeText = () => Promise.reject(new Error("denied"))`)
	page.eval(t, `document.querySelector(".detail .copy").click()`)
	page.waitFor(t, `document.querySelector(".detail .copy").getAttribute("aria-label")`, "Copy failed")
	if got := page.eval(t, `document.querySelector(".detail .cmd").textContent`); got != "loopctl takeover "+id {
		t.Fatalf("command text %q, want it unchanged by the button", got)
	}
}

func TestTrackPageOfAJobThatFailedBeforeItsWorktreeNamesRm(t *testing.T) {
	e, origin := streamLoop(t, "#!/bin/sh\nexit 1\n", "1m")
	if err := os.RemoveAll(origin); err != nil {
		t.Fatal(err)
	}
	if out, code := e.loopctl("run", "deps"); code == 0 {
		t.Fatalf("run without an origin exited 0:\n%s", out)
	}
	id := regexp.MustCompile(`[0-9a-f]{12}`).FindString(e.mustLoopctl("list"))
	fakeOpen(t, e.env, "")
	page := openPage(t, e.track(t, id, id))
	page.waitFor(t, `document.querySelector(".headline")?.textContent`, "Job failed")
	if got := page.eval(t, `document.querySelector(".detail .cmd")?.textContent`); got != "loopctl rm "+id {
		t.Fatalf("failed card command %q, want loopctl rm %s for a job with no worktree", got, id)
	}
}

func TestTrackPageOfAJobThatRanOutOfTimeSaysOutOfTime(t *testing.T) {
	e, id := startJobWithin(t, "#!/bin/sh\nsleep 30\n", "3s")
	fakeOpen(t, e.env, "")
	page := openPage(t, e.track(t, id, id))
	page.waitFor(t, `document.querySelector(".headline")?.textContent`, "Out of time")
	page.noEmDash(t)
}

func TestTrackPageShowsTheErrorLoopdReturned(t *testing.T) {
	e, id := startStreamJob(t, "0")
	fakeOpen(t, e.env, "")
	e.waitEnded(t, id)
	page := openPage(t, e.track(t, id, id))
	page.waitFor(t, `document.querySelector(".headline")?.textContent`, "Draft PR opened")
	e.mustLoopctl("rm", id)
	page.eval(t, `load().then(render, fail)`)
	page.waitFor(t, `document.querySelector(".notice")?.textContent`, "loopd returned an error · job "+id+": not found")
}

// logTimeContrastAtLeast45 is true when a log entry's time, blended with its opacity, reads at 4.5:1 on the log background.
const logTimeContrastAtLeast45 = `(() => {
	const rgb = (c) => c.match(/[\d.]+/g).slice(0, 3).map(Number);
	const lum = (c) => { const [r, g, b] = c.map((v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; }); return 0.2126 * r + 0.7152 * g + 0.0722 * b; };
	const time = document.querySelector(".log .entry .time");
	const bg = rgb(getComputedStyle(document.querySelector(".log")).backgroundColor);
	const op = Number(getComputedStyle(time).opacity);
	const fg = rgb(getComputedStyle(time).color).map((v, i) => v * op + bg[i] * (1 - op));
	const [hi, lo] = [lum(fg), lum(bg)].sort((a, b) => b - a);
	return (hi + 0.05) / (lo + 0.05) >= 4.5;
})()`

// hold is a shell line that waits until release runs, so a fake stays in its
// stage until the test has looked at the page.
func hold(t *testing.T) (wait string, release func()) {
	file := filepath.Join(t.TempDir(), "released")
	return "until [ -f '" + file + "' ]; do sleep 0.1; done", func() {
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// page is one tab of a headless Chrome driven over its DevTools pipe.
type page struct {
	in      *os.File
	out     *os.File
	buf     []byte
	n       int
	session string
}

func openPage(t *testing.T, url string) *page {
	t.Helper()
	if _, err := os.Stat(chromePath); err != nil {
		t.Skip("Google Chrome is not installed")
	}
	toChrome, in, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	out, fromChrome, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(chromePath, "--headless=new", "--disable-gpu", "--remote-debugging-pipe", "--user-data-dir="+t.TempDir(), "about:blank")
	cmd.ExtraFiles = []*os.File{toChrome, fromChrome}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Chrome quits once its pipe closes, and only then is its profile safe to remove.
	t.Cleanup(func() {
		_ = in.Close()
		quit := time.AfterFunc(10*time.Second, func() { _ = cmd.Process.Kill() })
		_ = cmd.Wait()
		quit.Stop()
	})
	_ = toChrome.Close()
	_ = fromChrome.Close()
	p := &page{in: in, out: out}
	var target struct {
		TargetID string `json:"targetId"`
	}
	p.call(t, "Target.createTarget", map[string]any{"url": url}, &target)
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	p.call(t, "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, &attached)
	p.session = attached.SessionID
	return p
}

// call sends one DevTools command and decodes its result into out.
func (p *page) call(t *testing.T, method string, params map[string]any, out any) {
	t.Helper()
	p.n++
	msg := map[string]any{"id": p.n, "method": method, "params": params}
	if p.session != "" {
		msg["sessionId"] = p.session
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.in.Write(append(data, 0)); err != nil {
		t.Fatal(err)
	}
	for {
		for bytes.IndexByte(p.buf, 0) < 0 {
			chunk := make([]byte, 1<<16)
			n, err := p.out.Read(chunk)
			if err != nil {
				t.Fatalf("chrome: %v", err)
			}
			p.buf = append(p.buf, chunk[:n]...)
		}
		i := bytes.IndexByte(p.buf, 0)
		var reply struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		err := json.Unmarshal(p.buf[:i], &reply)
		p.buf = p.buf[i+1:]
		if err != nil {
			t.Fatal(err)
		}
		if reply.ID != p.n {
			continue
		}
		if reply.Error != nil {
			t.Fatalf("%s: %s", method, reply.Error.Message)
		}
		if err := json.Unmarshal(reply.Result, out); err != nil {
			t.Fatal(err)
		}
		return
	}
}

// eval is the page's value of expr as a string.
func (p *page) eval(t *testing.T, expr string) string {
	t.Helper()
	var res struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
	}
	p.call(t, "Runtime.evaluate", map[string]any{"expression": "String(" + expr + ")", "returnByValue": true}, &res)
	return fmt.Sprint(res.Result.Value)
}

// noEmDash fails t when the page shows an em dash, which DESIGN.md leaves off it.
func (p *page) noEmDash(t *testing.T) {
	t.Helper()
	if got := p.eval(t, `document.body.innerText.includes("\u2014")`); got != "false" {
		t.Fatalf("page shows an em dash:\n%s", p.eval(t, `document.body.innerText`))
	}
}

// waitFor fails t unless expr becomes want within 20 seconds.
func (p *page) waitFor(t *testing.T, expr, want string) {
	t.Helper()
	var got string
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if got = p.eval(t, expr); got == want {
			return
		}
	}
	t.Fatalf("%s is %q, want %q", expr, got, want)
}
