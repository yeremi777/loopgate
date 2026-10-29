// Package web serves the tracking pages of loopd's Jobs on 127.0.0.1.
package web

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yeremi777/loopgate/internal/daemon"
	"github.com/yeremi777/loopgate/internal/ipc"
	"github.com/yeremi777/loopgate/internal/store"
)

// Pages is where loopd serves the tracking pages: a port on 127.0.0.1, and
// the token that every request must carry.
type Pages struct {
	Port  int
	Token string
}

// URL is the address of path on the pages, carrying the token.
func (p Pages) URL(path string) string {
	return "http://" + p.host() + path + "?token=" + p.Token
}

func (p Pages) host() string { return "127.0.0.1:" + strconv.Itoa(p.Port) }

// Listen opens the pages' listener on the port kept in home with the token
// kept there, or, when that port is taken or none is kept yet, on a free port
// that it keeps with a new token, so the token that a page left open on the
// old port sends to whatever listens there now opens no page.
func Listen(home string) (net.Listener, Pages, error) {
	tokenFile := filepath.Join(home, "track.token")
	portFile := filepath.Join(home, "track.port")
	kept, err := keptPort(portFile)
	if err != nil {
		return nil, Pages{}, err
	}
	if kept != 0 {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(kept)))
		if err == nil {
			token, err := keptToken(tokenFile)
			if err != nil {
				return nil, Pages{}, errors.Join(err, ln.Close())
			}
			return ln, Pages{Port: kept, Token: token}, nil
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, Pages{}, err
	}
	token, err := newToken(tokenFile)
	if err != nil {
		return nil, Pages{}, errors.Join(err, ln.Close())
	}
	p := Pages{Port: ln.Addr().(*net.TCPAddr).Port, Token: token}
	if err := os.WriteFile(portFile, []byte(strconv.Itoa(p.Port)+"\n"), 0o600); err != nil {
		return nil, Pages{}, errors.Join(err, ln.Close())
	}
	if kept != 0 {
		log.Printf("loopd: tracking port %d is taken; the tracking pages are now on port %d", kept, p.Port)
	}
	return ln, p, nil
}

// keptPort is the port in file, or 0 when none is kept yet.
func keptPort(file string) (int, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("%s holds %q, not a port; remove it to pick a new one", file, data)
	}
	return port, nil
}

// keptToken is the token in file, or a new one when file holds none.
func keptToken(file string) (string, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return newToken(file)
	}
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if len(token) < 32 {
		return "", fmt.Errorf("%s holds a token shorter than 128 bits; remove it to make a new one", file)
	}
	return token, nil
}

// newToken replaces file with a token made from 256 random bits, readable
// only by its owner.
func newToken(file string) (string, error) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(token + "\n"); err != nil {
		return "", errors.Join(err, f.Close())
	}
	return token, f.Close()
}

// csp lets a page load scripts, styles and images, and connect, only to the
// listener that served it.
const csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Serve answers the tracking pages of d's Jobs on ln until ln closes.
func Serve(ln net.Listener, d *daemon.Daemon, p Pages) error {
	srv := &http.Server{Handler: guard(p, routes(d, p)), ReadHeaderTimeout: 5 * time.Second}
	return srv.Serve(ln)
}

// guard answers only a GET that names the listener as its Host and carries
// the token, so no other site's page and no other user can read a Job.
func guard(p Pages, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		token := r.URL.Query().Get("token")
		if r.Host != p.host() || subtle.ConstantTimeCompare([]byte(token), []byte(p.Token)) != 1 {
			http.Error(w, "forbidden: run loopctl track <job> for this page's current link", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet {
			h.Set("Allow", http.MethodGet)
			http.Error(w, "only GET", http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

//go:embed index.html job.html track.css track.js
var files embed.FS

var pages = template.Must(template.ParseFS(files, "index.html", "job.html"))

func routes(d *daemon.Daemon, p Pages) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		loops, err := d.List()
		if err != nil {
			fail(w, err)
			return
		}
		jobs, err := d.Jobs()
		if err != nil {
			fail(w, err)
			return
		}
		render(w, "index.html", indexPage{Token: p.Token, Loops: blocks(loops, jobs)})
	})
	mux.HandleFunc("GET /jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := d.Resolve(r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		render(w, "job.html", jobPage{Token: p.Token, ID: id})
	})
	mux.HandleFunc("GET /jobs/{id}/data", func(w http.ResponseWriter, r *http.Request) {
		id, err := d.Resolve(r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		tr, err := d.Track(id)
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tr)
	})
	mux.Handle("GET /jobs/{id}/events", ipc.Events(d))
	mux.Handle("GET /track.css", http.FileServerFS(files))
	mux.Handle("GET /track.js", http.FileServerFS(files))
	return mux
}

func render(w http.ResponseWriter, name string, data any) {
	var page bytes.Buffer
	if err := pages.ExecuteTemplate(&page, name, data); err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = page.WriteTo(w)
}

func fail(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	if errors.Is(err, store.ErrNotFound) {
		code = http.StatusNotFound
	}
	http.Error(w, err.Error(), code)
}

type jobPage struct {
	Token string
	ID    string
}

type indexPage struct {
	Token string
	Loops []*loopBlock
}

// loopBlock is one Loop of the index with its Jobs, Blocked, failed and exhausted first.
type loopBlock struct {
	Name, State, Trigger, Checkout string
	Jobs                           []jobRow
}

// jobRow is one Job of the index; Label is its State as the Job's page names it.
type jobRow struct {
	ID, State, Label, Work, Result, PR string
}

// blocks groups jobs under their Loops, sorted by name, as loopctl list
// does; a Job whose Loop was removed gets a block of its own.
func blocks(loops []daemon.LoopStatus, jobs []store.Job) []*loopBlock {
	var out []*loopBlock
	byName := map[string]*loopBlock{}
	for _, l := range loops {
		b := &loopBlock{Name: l.Name, State: "active", Trigger: l.Trigger, Checkout: daemon.Tilde(l.Checkout)}
		if l.Paused {
			b.State = "paused"
		}
		out = append(out, b)
		byName[l.Name] = b
	}
	for _, j := range jobs {
		b, ok := byName[j.Loop]
		if !ok {
			b = &loopBlock{Name: j.Loop, State: "removed"}
			out = append(out, b)
			byName[j.Loop] = b
		}
		row := jobRow{ID: j.ID, State: string(j.State), Label: string(j.State), Work: firstLine(j.Work), Result: firstLine(result(j))}
		if j.State == store.Manual {
			row.Label = "taken over"
		}
		if j.State == store.Done {
			row.PR = j.PRURL
		}
		b.Jobs = append(b.Jobs, row)
	}
	slices.SortFunc(out, func(a, b *loopBlock) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// result is how a Job stands: its PR, question, error or summary.
func result(j store.Job) string {
	switch j.State {
	case store.Done:
		return j.PRURL
	case store.Blocked:
		return j.Question
	case store.Failed:
		return j.Error
	case store.Exhausted:
		return j.Summary
	}
	return ""
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
