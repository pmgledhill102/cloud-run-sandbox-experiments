// model-gateway: the host-side end of an agent's model calls.
//
// The agent runs in a Cloud Run sandbox with no network. stdio-bridge carries
// its plain-HTTP model calls to a Unix socket here, in the trusted host
// container. The gateway allows only model endpoints, adds the credential, and
// forwards over TLS. The sandbox never holds a credential:
//
//   - claude: Anthropic API. The key is read from Secret Manager into memory
//     at start, using the host's metadata-server identity. Never written to
//     disk, never put on a command line.
//   - gemini: the Gemini API shape that Gemini CLI's GATEWAY auth mode speaks,
//     translated to Vertex AI and authorised with the host's own
//     service-account token. No Gemini key exists anywhere.
//
// Each backend has a request cap; past it the gateway answers 429. Logs carry
// method, path, status, bytes and latency, never bodies or headers.
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const metadata = "http://metadata.google.internal/computeMetadata/v1/"

func metadataGet(path string) (string, error) {
	req, _ := http.NewRequest("GET", metadata+path, nil)
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("metadata %s: %d", path, resp.StatusCode)
	}
	return string(b), err
}

// tokenSource caches the host's access token and refreshes it before expiry.
type tokenSource struct {
	mu  sync.Mutex
	tok string
	exp time.Time
}

func (t *tokenSource) get() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tok != "" && time.Until(t.exp) > 2*time.Minute {
		return t.tok, nil
	}
	raw, err := metadataGet("instance/service-accounts/default/token")
	if err != nil {
		return "", err
	}
	var v struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return "", err
	}
	t.tok, t.exp = v.AccessToken, time.Now().Add(time.Duration(v.ExpiresIn)*time.Second)
	return t.tok, nil
}

func secretLatest(ts *tokenSource, project, name string) (string, error) {
	tok, err := ts.get()
	if err != nil {
		return "", err
	}
	u := fmt.Sprintf("https://secretmanager.googleapis.com/v1/projects/%s/secrets/%s/versions/latest:access", project, name)
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("secret %s: %d", name, resp.StatusCode)
	}
	var v struct {
		Payload struct {
			Data string `json:"data"`
		} `json:"payload"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	b, err := base64.StdEncoding.DecodeString(v.Payload.Data)
	return strings.TrimSpace(string(b)), err
}

type backend struct {
	name    string
	allow   *regexp.Regexp // matched against the incoming path
	max     int64
	count   atomic.Int64
	rewrite func(r *http.Request) error // sets URL, host and credential
}

func logf(b, format string, a ...any) {
	log.Printf(`{"component":"model-gateway","backend":%q,"msg":%q}`, b, fmt.Sprintf(format, a...))
}

func (b *backend) handler() http.Handler {
	rp := &httputil.ReverseProxy{
		Rewrite:       func(*httputil.ProxyRequest) {}, // the work is done in ServeHTTP
		FlushInterval: -1,                              // stream SSE as it arrives
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logf(b.name, "upstream error: %v", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		if r.Method != http.MethodPost || !b.allow.MatchString(r.URL.Path) {
			logf(b.name, "refused %s %s", r.Method, r.URL.Path)
			http.Error(w, "not a model endpoint", http.StatusForbidden)
			return
		}
		n := b.count.Add(1)
		if n > b.max {
			logf(b.name, "cap reached: request %d of %d refused", n, b.max)
			http.Error(w, "request cap reached", http.StatusTooManyRequests)
			return
		}
		// Whatever credential the client sent is dropped, never forwarded.
		for _, h := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key", "Cookie"} {
			r.Header.Del(h)
		}
		q := r.URL.Query()
		q.Del("key")
		r.URL.RawQuery = q.Encode()
		path := r.URL.Path
		if err := b.rewrite(r); err != nil {
			logf(b.name, "credential: %v", err)
			http.Error(w, "gateway credential error", http.StatusBadGateway)
			return
		}
		sw := &statusWriter{ResponseWriter: w, code: 200}
		rp.ServeHTTP(sw, r)
		logf(b.name, "request %d: %s %s -> %d, %d bytes, %dms", n, r.Method, path, sw.code, sw.n, time.Since(start).Milliseconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
	n    int64
}

func (s *statusWriter) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }
func (s *statusWriter) Write(p []byte) (int, error) {
	n, err := s.ResponseWriter.Write(p)
	s.n += int64(n)
	return n, err
}
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func serve(sock string, h http.Handler) {
	_ = os.Remove(sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		log.Fatalf("listen %s: %v", sock, err)
	}
	_ = os.Chmod(sock, 0o666)
	go func() { log.Fatal((&http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second}).Serve(l)) }()
}

func main() {
	claudeSock := flag.String("claude", "", "Unix socket for the Anthropic backend (empty: off)")
	claudeSecret := flag.String("claude-secret", "sandbox-agent-anthropic-key", "Secret Manager secret holding the Anthropic key")
	claudeMax := flag.Int64("claude-max", 150, "request cap for the Anthropic backend")
	geminiSock := flag.String("gemini", "", "Unix socket for the Gemini-to-Vertex backend (empty: off)")
	geminiMax := flag.Int64("gemini-max", 40, "request cap for the Gemini backend")
	geminiLoc := flag.String("gemini-location", "global", "Vertex AI location")
	flag.Parse()

	ts := &tokenSource{}
	project, err := metadataGet("project/project-id")
	if err != nil {
		log.Fatalf("project id: %v", err)
	}

	if *claudeSock != "" {
		key, err := secretLatest(ts, project, *claudeSecret)
		if err != nil {
			log.Fatalf("anthropic key: %v", err)
		}
		up, _ := url.Parse("https://api.anthropic.com")
		b := &backend{
			name:  "claude",
			allow: regexp.MustCompile(`^/v1/messages(/count_tokens)?$`),
			max:   *claudeMax,
			rewrite: func(r *http.Request) error {
				r.URL.Scheme, r.URL.Host, r.Host = up.Scheme, up.Host, up.Host
				r.Header.Set("X-Api-Key", key)
				return nil
			},
		}
		serve(*claudeSock, b.handler())
		logf("claude", "listening on %s, cap %d", *claudeSock, *claudeMax)
	}

	if *geminiSock != "" {
		host := "aiplatform.googleapis.com"
		if *geminiLoc != "global" {
			host = *geminiLoc + "-aiplatform.googleapis.com"
		}
		// Gemini API: /v1beta/models/{model}:{method}
		// Vertex AI:  /v1beta1/projects/{p}/locations/{l}/publishers/google/models/{model}:{method}
		re := regexp.MustCompile(`^/v1(beta)?/models/([A-Za-z0-9._-]+):(generateContent|streamGenerateContent|countTokens)$`)
		b := &backend{
			name:  "gemini",
			allow: re,
			max:   *geminiMax,
			rewrite: func(r *http.Request) error {
				m := re.FindStringSubmatch(r.URL.Path)
				tok, err := ts.get()
				if err != nil {
					return err
				}
				r.URL.Scheme, r.URL.Host, r.Host = "https", host, host
				r.URL.Path = fmt.Sprintf("/v1beta1/projects/%s/locations/%s/publishers/google/models/%s:%s", project, *geminiLoc, m[2], m[3])
				r.URL.RawPath = ""
				r.Header.Set("Authorization", "Bearer "+tok)
				return nil
			},
		}
		serve(*geminiSock, b.handler())
		logf("gemini", "listening on %s, cap %d, location %s", *geminiSock, *geminiMax, *geminiLoc)
	}
	select {}
}
