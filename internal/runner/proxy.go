package runner

import (
	"context"
	"io"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// proxy is the setup command's only way out of Seatbelt: it tunnels CONNECT
// to port 443 of the network domains, refuses every other request, and keeps
// the hosts it refused.
type proxy struct {
	domains []string
	dial    func(ctx context.Context, network, addr string) (net.Conn, error)
	ln      net.Listener
	srv     *http.Server

	mu      sync.Mutex
	refused map[string]bool
}

// startProxy listens on localhost, the one address Seatbelt lets through.
func startProxy(domains []string, dial func(ctx context.Context, network, addr string) (net.Conn, error)) (*proxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &proxy{domains: domains, dial: dial, ln: ln, refused: map[string]bool{}}
	p.srv = &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = p.srv.Serve(ln) }()
	return p, nil
}

func (p *proxy) addr() string { return p.ln.Addr().String() }

// close stops the proxy and returns the hosts it refused, sorted.
func (p *proxy) close() []string {
	_ = p.srv.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.refused) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(p.refused))
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		host, port = r.Host, "80"
	}
	host = strings.ToLower(host)
	if r.Method != http.MethodConnect || port != "443" || !p.allows(host) {
		name := host
		if r.Method != http.MethodConnect || port != "443" {
			name = net.JoinHostPort(host, port)
		}
		p.mu.Lock()
		p.refused[name] = true
		p.mu.Unlock()
		http.Error(w, "loopgate: "+name+" is not in the loop's network list", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	up, err := p.dial(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = up.Close() }()
	conn, buf, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	go func() {
		_, _ = io.Copy(up, buf.Reader)
		if tcp, ok := up.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()
	_, _ = io.Copy(conn, up)
}

// allows matches the network list as the stages' sandbox does: a domain
// itself, or with a *. prefix any of its subdomains.
func (p *proxy) allows(host string) bool {
	for _, d := range p.domains {
		if parent, ok := strings.CutPrefix(d, "*."); ok {
			if strings.HasSuffix(host, "."+parent) {
				return true
			}
		} else if host == d {
			return true
		}
	}
	return false
}
