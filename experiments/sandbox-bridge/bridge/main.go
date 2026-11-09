// Command stdio-bridge carries the sandbox's proxy connections over the
// sandbox process's own stdin and stdout.
//
// The Cloud Run sandbox has no network, and its bind mounts are 9p, which
// refuses Unix-socket connections and FIFOs in both directions (from the runner spike).
// The one channel that does cross the boundary is the stdio of `sandbox do`,
// which the spike measured as binary-clean and interactive. So the host runs
// the sandbox as a child, multiplexes many TCP streams over that one pipe pair
// with yamux, and hands each stream to egress-proxy. The sandbox side listens
// on 127.0.0.1:3128 and opens one stream per accepted connection.
//
//	stdio-bridge host -target proxy=/bridge/proxy.sock,build=/bridge/build.sock -- sandbox do ... -- stdio-bridge inside ... -- CMD...
//	stdio-bridge inside -forward 127.0.0.1:3128=proxy,127.0.0.1:3129=build -- CMD...
//
// Each stream starts with the target's name and a newline, so one pipe pair
// carries several host services. The host only dials names it was given.
//
// Inside, stdout is the tunnel, so the wrapped command's stdout is sent to
// stderr, which the sandbox passes through to the host's logs.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hashicorp/yamux"
)

// stdioConn presents a reader and a writer as the io.ReadWriteCloser yamux
// needs.
type stdioConn struct {
	io.Reader
	io.Writer
	closers []io.Closer
}

func (c *stdioConn) Close() error {
	var err error
	for _, cl := range c.closers {
		err = errors.Join(err, cl.Close())
	}
	return err
}

func yamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = os.Stderr
	cfg.KeepAliveInterval = 15 * time.Second
	cfg.ConnectionWriteTimeout = 30 * time.Second
	cfg.MaxStreamWindowSize = 4 << 20
	return cfg
}

func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	<-done
	a.Close()
	b.Close()
}

// parsePairs reads "a=b,c=d" into a map.
func parsePairs(s string) (map[string]string, error) {
	m := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if !ok || k == "" || v == "" {
			return nil, fmt.Errorf("bad pair %q", kv)
		}
		m[k] = v
	}
	return m, nil
}

// bufferedConn returns bytes the name reader already buffered before reading
// the connection itself.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err != nil {
		return 1
	}
	return 0
}

func runHost(args []string) int {
	fs := flag.NewFlagSet("host", flag.ExitOnError)
	targetSpec := fs.String("target", "proxy=/bridge/proxy.sock", "name=unix-socket pairs, comma-separated")
	_ = fs.Parse(args)
	targets, err := parsePairs(*targetSpec)
	if err != nil {
		log.Printf("stdio-bridge host: -target: %v", err)
		return 2
	}
	argv := fs.Args()
	if len(argv) == 0 {
		log.Print("stdio-bridge host: no sandbox command given")
		return 2
	}
	// The command is the launcher's own argv, not job input: running it is the point.
	cmd := exec.Command(argv[0], argv[1:]...) // nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		log.Print(err)
		return 1
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		log.Print(err)
		return 1
	}
	if err := cmd.Start(); err != nil {
		log.Printf("stdio-bridge host: start: %v", err)
		return 1
	}
	sess, err := yamux.Server(&stdioConn{Reader: out, Writer: in, closers: []io.Closer{in}}, yamuxConfig())
	if err != nil {
		log.Print(err)
		return 1
	}
	go func() {
		for {
			st, err := sess.Accept()
			if err != nil {
				return // session closed: the sandbox exited
			}
			go func(st net.Conn) {
				br := bufio.NewReader(st)
				name, err := br.ReadString('\n')
				sock, ok := targets[strings.TrimSpace(name)]
				if err != nil || !ok {
					log.Printf("stdio-bridge host: stream for unknown target %q", strings.TrimSpace(name))
					st.Close()
					return
				}
				up, err := net.Dial("unix", sock)
				if err != nil {
					log.Printf("stdio-bridge host: dial %s: %v", sock, err)
					st.Close()
					return
				}
				pipe(&bufferedConn{Conn: st, r: br}, up)
			}(st)
		}
	}()
	// Ignore SIGTERM. Cloud Run signals every process in the container when an
	// execution is cancelled, and this pipe is the runner's only route out: it
	// must stay up through the 10 s grace period so the runner, stopped by the
	// launcher, can report the job to GitHub (spike S6).
	signal.Ignore(syscall.SIGTERM)
	err = cmd.Wait()
	sess.Close()
	return exitCode(err)
}

func runInside(args []string) int {
	fs := flag.NewFlagSet("inside", flag.ExitOnError)
	fwdSpec := fs.String("forward", "127.0.0.1:3128=proxy", "listen-address=target pairs, comma-separated")
	_ = fs.Parse(args)
	forwards, err := parsePairs(*fwdSpec)
	if err != nil {
		log.Printf("stdio-bridge inside: -forward: %v", err)
		return 2
	}
	argv := fs.Args()
	if len(argv) == 0 {
		log.Print("stdio-bridge inside: no command given")
		return 2
	}
	sess, err := yamux.Client(&stdioConn{Reader: os.Stdin, Writer: os.Stdout, closers: []io.Closer{os.Stdout}}, yamuxConfig())
	if err != nil {
		log.Print(err)
		return 1
	}
	var listeners []net.Listener
	for addr, target := range forwards {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			log.Printf("stdio-bridge inside: listen %s: %v", addr, err)
			return 1
		}
		listeners = append(listeners, l)
		go func(l net.Listener, target string) {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				go func(c net.Conn) {
					st, err := sess.Open()
					if err == nil {
						_, err = io.WriteString(st, target+"\n")
					}
					if err != nil {
						log.Printf("stdio-bridge inside: open stream to %s: %v", target, err)
						c.Close()
						return
					}
					pipe(c, st)
				}(c)
			}
		}(l, target)
	}
	// The command is the launcher's own argv, not job input: running it is the point.
	cmd := exec.Command(argv[0], argv[1:]...) // nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd.Stdin = nil
	cmd.Stdout = os.Stderr // stdout is the tunnel
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	for _, l := range listeners {
		l.Close()
	}
	// Do not call sess.Close here. It waits for the receive loop, which is
	// blocked on stdin, and closing stdout does not reach the host while
	// `sandbox do` holds the pipe. Each side then waits on the other until the
	// host's keepalive gives up, 30-45 s later and billed. Exiting closes
	// everything, and the command that used the streams has finished.
	return exitCode(err)
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("stdio-bridge: ")
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: stdio-bridge host|inside [flags] -- CMD...")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "host":
		os.Exit(runHost(os.Args[2:]))
	case "inside":
		os.Exit(runInside(os.Args[2:]))
	default:
		fmt.Fprintln(os.Stderr, "usage: stdio-bridge host|inside [flags] -- CMD...")
		os.Exit(2)
	}
}
