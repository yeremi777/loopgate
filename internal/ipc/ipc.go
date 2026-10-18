// Package ipc carries loopctl commands to loopd over a Unix socket.
package ipc

import (
	"context"
	"errors"
	"net"

	"github.com/yeremi777/loopgate/internal/daemon"
)

var ErrNotRunning = errors.New("loopd not running")

// Serve answers loopctl requests on ln until ln closes.
func Serve(ln net.Listener, d *daemon.Daemon) error { return errors.New("not implemented") }

type Client struct{}

func NewClient(socket string) *Client { return &Client{} }

func (c *Client) List(ctx context.Context) ([]daemon.LoopStatus, error) {
	return nil, errors.New("not implemented")
}

// Add registers a Loop file and returns the Loop's name.
func (c *Client) Add(ctx context.Context, data []byte) (string, error) {
	return "", errors.New("not implemented")
}

func (c *Client) Remove(ctx context.Context, name string) error { return errors.New("not implemented") }

func (c *Client) Pause(ctx context.Context, name string) error { return errors.New("not implemented") }

func (c *Client) Resume(ctx context.Context, name string) error { return errors.New("not implemented") }
