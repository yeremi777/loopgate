// Package ipc carries loopctl commands to loopd as HTTP over a Unix socket.
package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"syscall"
	"time"

	"github.com/yeremi777/loopgate/internal/daemon"
	"github.com/yeremi777/loopgate/internal/feed"
	"github.com/yeremi777/loopgate/internal/store"
)

var ErrNotRunning = errors.New("loopd not running")

const maxLoopFile = 1 << 20

// Serve answers loopctl requests on ln until ln closes. pageURL is the
// address of a Job's tracking page.
func Serve(ln net.Listener, d *daemon.Daemon, pageURL func(id string) string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /loops", func(w http.ResponseWriter, r *http.Request) {
		loops, err := d.List()
		reply(w, loops, err)
	})
	mux.HandleFunc("POST /loops", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLoopFile))
		if err != nil {
			reply(w, struct{}{}, err)
			return
		}
		loop, err := d.Add(r.Context(), data)
		reply(w, loop.Name, err)
	})
	mux.HandleFunc("DELETE /loops/{name}", func(w http.ResponseWriter, r *http.Request) {
		reply(w, struct{}{}, d.Remove(r.PathValue("name")))
	})
	mux.HandleFunc("POST /loops/{name}/pause", func(w http.ResponseWriter, r *http.Request) {
		reply(w, struct{}{}, d.SetPaused(r.PathValue("name"), true))
	})
	mux.HandleFunc("POST /loops/{name}/resume", func(w http.ResponseWriter, r *http.Request) {
		reply(w, struct{}{}, d.SetPaused(r.PathValue("name"), false))
	})
	mux.HandleFunc("POST /loops/{name}/run", func(w http.ResponseWriter, r *http.Request) {
		var req runRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoopFile)).Decode(&req); err != nil {
			reply(w, store.Job{}, err)
			return
		}
		j, err := d.Run(r.PathValue("name"), req.Spec)
		reply(w, j, err)
	})
	mux.HandleFunc("GET /jobs", func(w http.ResponseWriter, r *http.Request) {
		jobs, err := d.Jobs()
		reply(w, jobs, err)
	})
	mux.HandleFunc("GET /jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := d.Resolve(r.PathValue("id"))
		if err != nil {
			reply(w, daemon.JobLog{}, err)
			return
		}
		log, err := d.Job(id)
		reply(w, log, err)
	})
	mux.HandleFunc("DELETE /jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := d.Resolve(r.PathValue("id"))
		if err != nil {
			reply(w, daemon.Removal{}, err)
			return
		}
		removed, err := d.RemoveJob(id)
		reply(w, removed, err)
	})
	mux.HandleFunc("POST /jobs/{id}/try", func(w http.ResponseWriter, r *http.Request) {
		id, err := d.Resolve(r.PathValue("id"))
		if err != nil {
			reply(w, daemon.Tried{}, err)
			return
		}
		tried, err := d.Try(id)
		reply(w, tried, err)
	})
	mux.HandleFunc("GET /jobs/{id}/page", func(w http.ResponseWriter, r *http.Request) {
		id, err := d.Resolve(r.PathValue("id"))
		reply(w, Page{ID: id, URL: pageURL(id)}, err)
	})
	mux.Handle("GET /jobs/{id}/events", Events(d))
	mux.HandleFunc("POST /loops/{name}/requeue/{issue}", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.PathValue("issue"))
		if err != nil {
			reply(w, struct{}{}, fmt.Errorf("issue %q is not a number", r.PathValue("issue")))
			return
		}
		reply(w, struct{}{}, d.Requeue(r.PathValue("name"), n))
	})
	mux.HandleFunc("POST /loops/{name}/stop", func(w http.ResponseWriter, r *http.Request) {
		reply(w, struct{}{}, d.Stop(r.PathValue("name")))
	})
	for action, do := range map[string]func(string) (store.Job, error){"takeover": d.Takeover, "handback": d.Handback} {
		mux.HandleFunc("POST /jobs/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			id, err := d.Resolve(r.PathValue("id"))
			if err != nil {
				reply(w, store.Job{}, err)
				return
			}
			j, err := do(id)
			reply(w, j, err)
		})
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return srv.Serve(ln)
}

// Events streams the events of the Job that the id path value names, as
// one JSON line each, then a line with the Job as it ended or the error
// that ended the stream.
func Events(d *daemon.Daemon) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		enc := json.NewEncoder(w)
		rc := http.NewResponseController(w)
		id, err := d.Resolve(r.PathValue("id"))
		if err == nil {
			err = d.Follow(r.Context(), id, func(e feed.Event) error {
				if err := enc.Encode(followLine{Event: &e}); err != nil {
					return err
				}
				return rc.Flush()
			})
		}
		var end followLine
		if err == nil {
			var log daemon.JobLog
			log, err = d.Job(id)
			end.Job = &log.Job
		}
		if err != nil {
			end = followLine{Error: err.Error()}
		}
		_ = enc.Encode(end)
	}
}

type runRequest struct {
	Spec string `json:"spec"`
}

// Page is a Job's full id and the address of its tracking page.
type Page struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// followLine is one line of a followed Job's stream: an event, the Job as
// it ended, or the error that ended the stream.
type followLine struct {
	Event *feed.Event `json:"event,omitempty"`
	Job   *store.Job  `json:"job,omitempty"`
	Error string      `json:"error,omitempty"`
}

type response[T any] struct {
	Data  T      `json:"data"`
	Error string `json:"error,omitempty"`
}

func reply[T any](w http.ResponseWriter, data T, err error) {
	w.Header().Set("Content-Type", "application/json")
	resp := response[T]{Data: data}
	if err != nil {
		resp.Error = err.Error()
		w.WriteHeader(statusOf(err))
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func statusOf(err error) int {
	switch {
	case errors.Is(err, daemon.ErrUnknownLoop), errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, daemon.ErrLoopExists), errors.Is(err, daemon.ErrBusy), errors.Is(err, daemon.ErrNotTakeable),
		errors.Is(err, daemon.ErrNotManual), errors.Is(err, daemon.ErrNothingRunning), errors.Is(err, daemon.ErrStarting),
		errors.Is(err, store.ErrNotEnded), errors.Is(err, daemon.ErrNotDone):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

// Client calls loopd. add and follow wait on untimed instead of http,
// because loopd bounds the Trial gate and a Job by the Loop's own timeout.
type Client struct{ http, untimed *http.Client }

func NewClient(socket string) *Client {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		},
	}
	// run fetches origin and adds a worktree before it answers.
	return &Client{http: &http.Client{Timeout: 5 * time.Minute, Transport: transport}, untimed: &http.Client{Transport: transport}}
}

func (c *Client) List(ctx context.Context) ([]daemon.LoopStatus, error) {
	var loops []daemon.LoopStatus
	err := call(ctx, c.http, http.MethodGet, "/loops", nil, &loops)
	return loops, err
}

// Add registers a Loop file once its Trial gate passes and returns the Loop's name.
func (c *Client) Add(ctx context.Context, data []byte) (string, error) {
	var name string
	err := call(ctx, c.untimed, http.MethodPost, "/loops", data, &name)
	return name, err
}

func (c *Client) Remove(ctx context.Context, name string) error {
	return call[struct{}](ctx, c.http, http.MethodDelete, "/loops/"+url.PathEscape(name), nil, nil)
}

func (c *Client) Pause(ctx context.Context, name string) error {
	return call[struct{}](ctx, c.http, http.MethodPost, "/loops/"+url.PathEscape(name)+"/pause", nil, nil)
}

func (c *Client) Resume(ctx context.Context, name string) error {
	return call[struct{}](ctx, c.http, http.MethodPost, "/loops/"+url.PathEscape(name)+"/resume", nil, nil)
}

// Run starts a Job of the named Loop; spec is an absolute path or empty.
func (c *Client) Run(ctx context.Context, name, spec string) (store.Job, error) {
	body, err := json.Marshal(runRequest{Spec: spec})
	if err != nil {
		return store.Job{}, err
	}
	var j store.Job
	err = call(ctx, c.http, http.MethodPost, "/loops/"+url.PathEscape(name)+"/run", body, &j)
	return j, err
}

func (c *Client) Jobs(ctx context.Context) ([]store.Job, error) {
	var jobs []store.Job
	err := call(ctx, c.http, http.MethodGet, "/jobs", nil, &jobs)
	return jobs, err
}

func (c *Client) Job(ctx context.Context, id string) (daemon.JobLog, error) {
	var log daemon.JobLog
	err := call(ctx, c.http, http.MethodGet, "/jobs/"+url.PathEscape(id), nil, &log)
	return log, err
}

// Page is the tracking page of the Job whose id starts with prefix.
func (c *Client) Page(ctx context.Context, prefix string) (Page, error) {
	var page Page
	err := call(ctx, c.http, http.MethodGet, "/jobs/"+url.PathEscape(prefix)+"/page", nil, &page)
	return page, err
}

// RemoveJob removes a Job that ended, with its worktree, directory and,
// unless someone committed on it since, its branch.
func (c *Client) RemoveJob(ctx context.Context, id string) (daemon.Removal, error) {
	var removed daemon.Removal
	err := call(ctx, c.http, http.MethodDelete, "/jobs/"+url.PathEscape(id), nil, &removed)
	return removed, err
}

// Try readies a done Job's worktree to run by hand.
func (c *Client) Try(ctx context.Context, id string) (daemon.Tried, error) {
	var tried daemon.Tried
	err := call(ctx, c.http, http.MethodPost, "/jobs/"+url.PathEscape(id)+"/try", nil, &tried)
	return tried, err
}

func (c *Client) Requeue(ctx context.Context, name string, issue int) error {
	return call[struct{}](ctx, c.http, http.MethodPost, "/loops/"+url.PathEscape(name)+"/requeue/"+strconv.Itoa(issue), nil, nil)
}

func (c *Client) Stop(ctx context.Context, name string) error {
	return call[struct{}](ctx, c.http, http.MethodPost, "/loops/"+url.PathEscape(name)+"/stop", nil, nil)
}

func (c *Client) Takeover(ctx context.Context, id string) (store.Job, error) {
	var j store.Job
	err := call(ctx, c.http, http.MethodPost, "/jobs/"+url.PathEscape(id)+"/takeover", nil, &j)
	return j, err
}

func (c *Client) Handback(ctx context.Context, id string) (store.Job, error) {
	var j store.Job
	err := call(ctx, c.http, http.MethodPost, "/jobs/"+url.PathEscape(id)+"/handback", nil, &j)
	return j, err
}

// Follow shows each event of the running Job as it happens and returns the
// Job once its outcome is stored; it fails with daemon.ErrStopped when loopd
// goes away.
func (c *Client) Follow(ctx context.Context, id string, show func(feed.Event)) (store.Job, error) {
	res, err := send(ctx, c.untimed, http.MethodGet, "/jobs/"+url.PathEscape(id)+"/events", nil)
	if err != nil {
		return store.Job{}, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return store.Job{}, olderLoopd(res)
	}
	dec := json.NewDecoder(res.Body)
	for {
		var line followLine
		if err := dec.Decode(&line); err != nil {
			return store.Job{}, daemon.ErrStopped
		}
		switch {
		case line.Event != nil:
			show(*line.Event)
		case line.Job != nil:
			return *line.Job, nil
		default:
			return store.Job{}, errors.New(line.Error)
		}
	}
}

func call[T any](ctx context.Context, client *http.Client, method, path string, body []byte, out *T) error {
	res, err := send(ctx, client, method, path, body)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	var resp response[T]
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return olderLoopd(res)
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	if out != nil {
		*out = resp.Data
	}
	return nil
}

// olderLoopd is the error for a reply that is not loopd's own, as from a
// loopd that has no route for the request or answers in an older shape.
func olderLoopd(res *http.Response) error {
	return fmt.Errorf("loopd answered %s; it may be older than loopctl, so run loopctl install", res.Status)
}

func send(ctx context.Context, client *http.Client, method, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://loopd"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
		return nil, ErrNotRunning
	}
	return res, err
}
