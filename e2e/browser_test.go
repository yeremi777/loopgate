package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

const chromePath = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

func TestTrackPageFollowsTheJobLiveAndMovesToItsEndWithoutAReload(t *testing.T) {
	claude := strings.Replace(streamClaude, "sleep STAGE_SLEEP",
		`say '{"type":"text","text":"<img src=x onerror=alert(1)><b>bold</b>"}'; sleep 4`, 1)
	e, id := startJobOf(t, claude)
	fakeOpen(t, e.env, "")
	page := openPage(t, e.track(t, id, id))

	page.waitFor(t, `document.querySelector(".headline")?.textContent`, "Implementing, run 1 of 2")
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

	page.waitFor(t, `document.querySelector(".headline")?.textContent`, "Draft PR opened")
	if got := page.eval(t, `document.querySelector(".pr-link").textContent + " | " + document.querySelectorAll(".live").length`); got != "Open PR #9 | 0" {
		t.Fatalf("done page: %q, want the PR link and no live section", got)
	}
}

func TestTrackPageSaysLoopdStoppedWhenLoopdStops(t *testing.T) {
	e, id := startStreamJob(t, "10")
	fakeOpen(t, e.env, "")
	page := openPage(t, e.track(t, id, id))
	page.waitFor(t, `document.querySelector(".headline")?.textContent`, "Implementing, run 1 of 2")
	if err := e.loopd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	page.waitFor(t, `document.querySelector(".notice strong")?.textContent`, "loopd stopped")
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

// waitFor fails t unless expr becomes want within 20 seconds.
func (p *page) waitFor(t *testing.T, expr, want string) {
	t.Helper()
	var got string
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if got = p.eval(t, expr); got == want {
			return
		}	}
	t.Fatalf("%s is %q, want %q", expr, got, want)
}
