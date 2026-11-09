// Command egress-proxy is the only route out of the runner's sandbox.
//
// It is an HTTP proxy (CONNECT for TLS, absolute-form requests for plain
// HTTP) that allows a destination only when its host name is on an allowlist
// AND every address it resolves to is public. The second check is what keeps
// an allowlisted name from being pointed at the metadata server or a private
// range, and the proxy dials the address it checked rather than resolving
// again, so a DNS answer cannot change between the check and the connection.
//
// It listens on a Unix socket because the sandbox has no network: the socket
// on a bind-mounted directory is the bridge, and socat inside the sandbox
// exposes it as 127.0.0.1:3128 for tools that only speak TCP proxies.
//
// One JSON line per decision goes to stdout, which Cloud Run ships to Cloud
// Logging.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

type allowlist struct {
	exact    map[string]bool
	suffixes []string // "*.example.com" is stored as "example.com"
}

func loadAllowlist(path string) (*allowlist, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	a := &allowlist{exact: map[string]bool{}}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		line = strings.ToLower(strings.TrimSuffix(line, "."))
		if rest, ok := strings.CutPrefix(line, "*."); ok {
			a.suffixes = append(a.suffixes, rest)
			continue
		}
		a.exact[line] = true
	}
	return a, s.Err()
}

// allows reports whether host is on the list. A wildcard matches any depth of
// subdomain but not the bare domain, the same as a TLS wildcard would not.
func (a *allowlist) allows(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if a.exact[host] {
		return true
	}
	for _, s := range a.suffixes {
		if strings.HasSuffix(host, "."+s) {
			return true
		}
	}
	return false
}

var cgnat = mustCIDR("100.64.0.0/10")

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// publicIP rejects everything a job has no business reaching through the
// proxy: loopback, RFC 1918, link-local (169.254.169.254 is the metadata
// server), CGNAT, multicast and unspecified addresses.
func publicIP(ip net.IP) bool {
	nonPublic := ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip)
	return !nonPublic
}

type denied struct{ host, reason string }

func (d *denied) Error() string { return d.host + ": " + d.reason }

type proxy struct {
	allow    *allowlist
	ports    map[string]bool
	resolver *net.Resolver
	dialer   *net.Dialer
	logMu    sync.Mutex
}

func (p *proxy) logDecision(action, host, port, reason string, extra map[string]any) {
	rec := map[string]any{
		"time": time.Now().UTC().Format(time.RFC3339Nano), "component": "egress-proxy",
		"action": action, "host": host, "port": port,
	}
	if reason != "" {
		rec["reason"] = reason
	}
	for k, v := range extra {
		rec[k] = v
	}
	b, _ := json.Marshal(rec)
	p.logMu.Lock()
	defer p.logMu.Unlock()
	fmt.Println(string(b))
}

// dial checks the destination and connects to the address it checked.
func (p *proxy) dial(ctx context.Context, hostport string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return nil, &denied{hostport, "malformed host:port"}
	}
	if !p.ports[port] {
		return nil, &denied{host, "port " + port + " not allowed"}
	}
	if net.ParseIP(host) != nil {
		return nil, &denied{host, "IP literals are not allowed; use a host name"}
	}
	if !p.allow.allows(host) {
		return nil, &denied{host, "not on allowlist"}
	}
	addrs, err := p.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	var lastErr error
	for _, a := range addrs {
		if !publicIP(a.IP) {
			lastErr = &denied{host, "resolves to non-public address " + a.IP.String()}
			continue
		}
		c, err := p.dialer.DialContext(ctx, "tcp", net.JoinHostPort(a.IP.String(), port))
		if err == nil {
			return c, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("resolve %s: no addresses", host)
	}
	return nil, lastErr
}

func (p *proxy) refuse(w http.ResponseWriter, host, port string, err error) {
	var d *denied
	code := http.StatusBadGateway
	action := "error"
	if errors.As(err, &d) {
		code, action = http.StatusForbidden, "deny"
	}
	p.logDecision(action, host, port, err.Error(), nil)
	http.Error(w, "egress-proxy: "+err.Error(), code)
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.connect(w, r)
		return
	}
	if r.URL.Host == "" {
		http.Error(w, "egress-proxy: only proxy requests are served", http.StatusBadRequest)
		return
	}
	p.forward(w, r)
}

func (p *proxy) connect(w http.ResponseWriter, r *http.Request) {
	host, port, _ := net.SplitHostPort(r.Host)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	up, err := p.dial(ctx, r.Host)
	if err != nil {
		p.refuse(w, host, port, err)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		up.Close()
		http.Error(w, "egress-proxy: hijack unsupported", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		up.Close()
		return
	}
	p.logDecision("allow", host, port, "", map[string]any{"method": "CONNECT", "upstream": up.RemoteAddr().String()})
	_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	go func() {
		// Bytes the client sent after the CONNECT line are already buffered.
		br := buf.Reader
		if n := br.Buffered(); n > 0 {
			b, _ := br.Peek(n)
			_, _ = up.Write(b)
		}
		_, _ = io.Copy(up, client)
		if tc, ok := up.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()
	_, _ = io.Copy(client, up)
	client.Close()
	up.Close()
}

var hopByHop = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"}

func (p *proxy) forward(w http.ResponseWriter, r *http.Request) {
	host, port := r.URL.Hostname(), r.URL.Port()
	if port == "" {
		port = "80"
	}
	tr := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return p.dial(ctx, net.JoinHostPort(host, port))
		},
		ResponseHeaderTimeout: 30 * time.Second,
	}
	defer tr.CloseIdleConnections()
	out := r.Clone(r.Context())
	out.RequestURI = ""
	for _, h := range hopByHop {
		out.Header.Del(h)
	}
	resp, err := tr.RoundTrip(out)
	if err != nil {
		p.refuse(w, host, port, unwrapDenied(err))
		return
	}
	defer resp.Body.Close()
	p.logDecision("allow", host, port, "", map[string]any{"method": r.Method, "status": resp.StatusCode})
	for _, h := range hopByHop {
		resp.Header.Del(h)
	}
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// unwrapDenied digs a *denied out of the transport's wrapping so plain-HTTP
// refusals are reported as 403, the same as CONNECT ones.
func unwrapDenied(err error) error {
	var d *denied
	if errors.As(err, &d) {
		return d
	}
	return err
}

func main() {
	unixPath := flag.String("unix", "", "listen on this Unix socket (mode 0666)")
	tcpAddr := flag.String("tcp", "", "listen on this TCP address, e.g. 127.0.0.1:3128")
	allowPath := flag.String("allowlist", "/etc/egress-proxy/allowlist.txt", "host allowlist, one per line, *.domain wildcards")
	ports := flag.String("ports", "443,80", "destination ports allowed")
	flag.Parse()

	al, err := loadAllowlist(*allowPath)
	if err != nil {
		log.Fatalf("egress-proxy: allowlist: %v", err)
	}
	p := &proxy{allow: al, ports: map[string]bool{}, resolver: net.DefaultResolver,
		dialer: &net.Dialer{Timeout: 10 * time.Second}}
	for _, pt := range strings.Split(*ports, ",") {
		p.ports[strings.TrimSpace(pt)] = true
	}
	// Stay up through Cloud Run's SIGTERM grace period: the runner needs the
	// proxy to report a cancelled job to GitHub before SIGKILL (spike S6).
	signal.Ignore(syscall.SIGTERM)
	srv := &http.Server{Handler: p, ReadHeaderTimeout: 15 * time.Second}

	var ls []net.Listener
	if *unixPath != "" {
		_ = os.Remove(*unixPath)
		l, err := net.Listen("unix", *unixPath)
		if err != nil {
			log.Fatalf("egress-proxy: listen %s: %v", *unixPath, err)
		}
		// The sandbox runs as a non-root user and must be able to connect.
		if err := os.Chmod(*unixPath, 0o666); err != nil {
			log.Fatalf("egress-proxy: chmod %s: %v", *unixPath, err)
		}
		ls = append(ls, l)
	}
	if *tcpAddr != "" {
		l, err := net.Listen("tcp", *tcpAddr)
		if err != nil {
			log.Fatalf("egress-proxy: listen %s: %v", *tcpAddr, err)
		}
		ls = append(ls, l)
	}
	if len(ls) == 0 {
		log.Fatal("egress-proxy: give -unix, -tcp or both")
	}
	p.logDecision("start", "", "", "", map[string]any{"exact": len(al.exact), "wildcards": len(al.suffixes)})
	errc := make(chan error, len(ls))
	for _, l := range ls {
		go func(l net.Listener) { errc <- srv.Serve(l) }(l)
	}
	log.Fatal(<-errc)
}
