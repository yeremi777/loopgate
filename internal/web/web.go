// Package web serves the tracking pages of loopd's Jobs on 127.0.0.1.
package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yeremi777/loopgate/internal/daemon"
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

// Listen opens the pages' listener on the port kept in home, or on a free
// port that it keeps when that one is taken or none is kept yet, with the
// token kept in home, which it makes on the first start.
func Listen(home string) (net.Listener, Pages, error) {
	token, err := keptToken(filepath.Join(home, "track.token"))
	if err != nil {
		return nil, Pages{}, err
	}
	portFile := filepath.Join(home, "track.port")
	kept, err := keptPort(portFile)
	if err != nil {
		return nil, Pages{}, err
	}
	if kept != 0 {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(kept)))
		if err == nil {
			return ln, Pages{Port: kept, Token: token}, nil
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, Pages{}, err
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

// keptToken is the token in file, made from 256 random bits on first use.
func keptToken(file string) (string, error) {
	data, err := os.ReadFile(file)
	if err == nil {
		token := strings.TrimSpace(string(data))
		if len(token) < 32 {
			return "", fmt.Errorf("%s holds a token shorter than 128 bits; remove it to make a new one", file)
		}
		return token, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
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
			http.Error(w, "forbidden", http.StatusForbidden)
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

func routes(d *daemon.Daemon, p Pages) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}
