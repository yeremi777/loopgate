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
	"syscall"
	"time"

	"github.com/yeremi777/loopgate/internal/daemon"
)

var ErrNotRunning = errors.New("loopd not running")

const maxLoopFile = 1 << 20

// Serve answers loopctl requests on ln until ln closes.
func Serve(ln net.Listener, d *daemon.Daemon) error {
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
		loop, err := d.Add(data)
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
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return srv.Serve(ln)
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
	case errors.Is(err, daemon.ErrUnknownLoop):
		return http.StatusNotFound
	case errors.Is(err, daemon.ErrLoopExists):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

type Client struct{ http *http.Client }

func NewClient(socket string) *Client {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	return &Client{http: &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", socket)
			},
		},
	}}
}

func (c *Client) List(ctx context.Context) ([]daemon.LoopStatus, error) {
	var loops []daemon.LoopStatus
	err := call(ctx, c, http.MethodGet, "/loops", nil, &loops)
	return loops, err
}

// Add registers a Loop file and returns the Loop's name.
func (c *Client) Add(ctx context.Context, data []byte) (string, error) {
	var name string
	err := call(ctx, c, http.MethodPost, "/loops", data, &name)
	return name, err
}

func (c *Client) Remove(ctx context.Context, name string) error {
	return call[struct{}](ctx, c, http.MethodDelete, "/loops/"+url.PathEscape(name), nil, nil)
}

func (c *Client) Pause(ctx context.Context, name string) error {
	return call[struct{}](ctx, c, http.MethodPost, "/loops/"+url.PathEscape(name)+"/pause", nil, nil)
}

func (c *Client) Resume(ctx context.Context, name string) error {
	return call[struct{}](ctx, c, http.MethodPost, "/loops/"+url.PathEscape(name)+"/resume", nil, nil)
}

func call[T any](ctx context.Context, c *Client, method, path string, body []byte, out *T) error {
	req, err := http.NewRequestWithContext(ctx, method, "http://loopd"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	res, err := c.http.Do(req)
	if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
		return ErrNotRunning
	}
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	var resp response[T]
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return fmt.Errorf("loopd reply: %w", err)
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	if out != nil {
		*out = resp.Data
	}
	return nil
}
