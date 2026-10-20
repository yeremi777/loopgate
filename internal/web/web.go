// Package web serves the tracking pages of loopd's Jobs on 127.0.0.1.
package web

import (
	"errors"
	"net"

	"github.com/yeremi777/loopgate/internal/daemon"
)

// Pages is where loopd serves the tracking pages: a port on 127.0.0.1, and
// the token that every request must carry.
type Pages struct {
	Port  int
	Token string
}

// URL is the address of path on the pages, carrying the token.
func (p Pages) URL(path string) string { panic("not implemented") }

// Listen opens the pages' listener on the port kept in home, or on a free
// port that it keeps when that one is taken or none is kept yet, with the
// token kept in home, which it makes on the first start.
func Listen(home string) (net.Listener, Pages, error) {
	return nil, Pages{}, errors.New("not implemented")
}

// Serve answers the tracking pages of d's Jobs on ln until ln closes.
func Serve(ln net.Listener, d *daemon.Daemon, p Pages) error {
	return errors.New("not implemented")
}
