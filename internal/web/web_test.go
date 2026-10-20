package web

import (
	"bytes"
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

func TestTrackTokenIsRequiredOnEveryRequest(t *testing.T) {
	p := serve(t)
	base := "http://127.0.0.1:" + strconv.Itoa(p.Port)
	for _, path := range []string{"/", "/?token=", "/?token=" + strings.Repeat("0", len(p.Token)), "/track.css", "/jobs/0123456789ab", "/jobs/0123456789ab/events"} {
		if code := get(t, base+path, "").StatusCode; code != http.StatusForbidden {
			t.Errorf("GET %s: %d, want 403", path, code)
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
		if code := get(t, p.URL("/"), host).StatusCode; code != http.StatusForbidden {
			t.Errorf("Host %s: %d, want 403", host, code)
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
	home := t.TempDir()
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
	return p
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

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
