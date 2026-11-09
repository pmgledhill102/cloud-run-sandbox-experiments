// Command probe is the non-curl client for the sandbox-bridge rig. Each
// subcommand tries one way out and prints one line: ok or fail, and why.
// It exits 0 either way; the reader judges the outcome against the context.
//
//	probe proxy-get URL         HTTPS GET through HTTPS_PROXY (Go's own proxy support)
//	probe direct-get URL        HTTPS GET ignoring any proxy
//	probe dial HOST:PORT        raw TCP connect, no proxy, no TLS
//	probe resolve NAME          DNS lookup with Go's resolver
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Println("usage: probe proxy-get|direct-get|dial|resolve TARGET")
		os.Exit(2)
	}
	cmd, target := os.Args[1], os.Args[2]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var err error
	var detail string
	switch cmd {
	case "proxy-get":
		detail, err = get(ctx, target, http.ProxyFromEnvironment)
	case "direct-get":
		detail, err = get(ctx, target, nil)
	case "dial":
		var c net.Conn
		c, err = (&net.Dialer{}).DialContext(ctx, "tcp", target)
		if err == nil {
			detail = "connected to " + c.RemoteAddr().String()
			c.Close()
		}
	case "resolve":
		var addrs []string
		addrs, err = net.DefaultResolver.LookupHost(ctx, target)
		detail = fmt.Sprint(addrs)
	default:
		err = fmt.Errorf("unknown subcommand %q", cmd)
	}
	if err != nil {
		fmt.Printf("fail %s %s: %v\n", cmd, target, err)
		return
	}
	fmt.Printf("ok %s %s: %s\n", cmd, target, detail)
}

func get(ctx context.Context, target string, proxy func(*http.Request) (*url.URL, error)) (string, error) {
	tr := &http.Transport{Proxy: proxy}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	n, _ := io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	return fmt.Sprintf("http %d, %d bytes", resp.StatusCode, n), nil
}
