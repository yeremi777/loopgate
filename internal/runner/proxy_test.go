package runner

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"
)

// echoUpstream stands in for every remote host: the proxy's dial reaches it
// whatever address the tunnel names, and the addresses asked for are recorded.
func echoUpstream(t *testing.T, dialed ...*[]string) func(context.Context, string, string) (net.Conn, error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	var mu sync.Mutex
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		mu.Lock()
		for _, d := range dialed {
			*d = append(*d, addr)
		}
		mu.Unlock()
		var d net.Dialer
		return d.DialContext(ctx, network, ln.Addr().String())
	}
}

// connect asks p for a tunnel to target and returns the status and the tunnel.
func connect(t *testing.T, p *proxy, target string) (int, net.Conn) {
	t.Helper()
	c, err := net.Dial("tcp", p.addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := io.WriteString(c, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, c
}

func TestProxyTunnelsToNetworkDomainsOnPort443(t *testing.T) {
	var dialed []string
	p, err := startProxy([]string{"registry.npmjs.org", "*.pythonhosted.org"}, echoUpstream(t, &dialed))
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	for _, target := range []string{"registry.npmjs.org:443", "Registry.NPMJS.org:443", "files.pythonhosted.org:443"} {
		code, c := connect(t, p, target)
		if code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", target, code)
		}
		if _, err := io.WriteString(c, "ping"); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 4)
		if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping" {
			t.Fatalf("%s: tunnel returned %q, %v", target, buf, err)
		}
	}
	if got := p.close(); got != nil {
		t.Fatalf("refused %q, want none", got)
	}
	if want := []string{"registry.npmjs.org:443", "registry.npmjs.org:443", "files.pythonhosted.org:443"}; !reflect.DeepEqual(dialed, want) {
		t.Fatalf("dialed %q, want %q", dialed, want)
	}
}

// A process that outlives the setup command must lose its tunnel when setup ends.
func TestProxyCloseEndsOpenTunnels(t *testing.T) {
	p, err := startProxy([]string{"registry.npmjs.org"}, echoUpstream(t))
	if err != nil {
		t.Fatal(err)
	}
	code, c := connect(t, p, "registry.npmjs.org:443")
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	p.close()
	if err := c.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(c, "ping")
	buf := make([]byte, 4)
	n, err := io.ReadFull(c, buf)
	var netErr net.Error
	if err == nil || (errors.As(err, &netErr) && netErr.Timeout()) {
		t.Fatalf("tunnel still open after close: read %q, %v", buf[:n], err)
	}
}

func TestProxyRefusesEverythingElseAndNamesTheHosts(t *testing.T) {
	p, err := startProxy([]string{"registry.npmjs.org", "*.pythonhosted.org"}, echoUpstream(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"example.com:443", "pythonhosted.org:443", "evilpythonhosted.org:443", "registry.npmjs.org:80", "example.com:443"} {
		if code, _ := connect(t, p, target); code != http.StatusForbidden {
			t.Fatalf("%s: status %d, want 403", target, code)
		}
	}
	viaProxy := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: p.addr()})}}
	res, err := viaProxy.Get("http://registry.npmjs.org/")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("plain http: status %d, want 403", res.StatusCode)
	}
	want := []string{"evilpythonhosted.org", "example.com", "pythonhosted.org", "registry.npmjs.org:80"}
	if got := p.close(); !reflect.DeepEqual(got, want) {
		t.Fatalf("refused %q, want %q", got, want)
	}
}
