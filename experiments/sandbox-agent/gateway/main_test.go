package main

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// upstream records what reached it, and streams two SSE events with a pause
// between them so the test can tell streaming from buffering.
func upstream(t *testing.T, seen *http.Request) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = *r.Clone(r.Context())
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(300 * time.Millisecond)
		io.WriteString(w, "data: two\n\n")
	}))
}

func gateway(t *testing.T, up *httptest.Server, max int64) *httptest.Server {
	u, _ := url.Parse(up.URL)
	b := &backend{
		name:  "test",
		allow: regexp.MustCompile(`^/v1/messages$`),
		max:   max,
		rewrite: func(r *http.Request) error {
			r.URL.Scheme, r.URL.Host, r.Host = u.Scheme, u.Host, u.Host
			r.Header.Set("X-Api-Key", "real-key")
			return nil
		},
	}
	return httptest.NewServer(b.handler())
}

func TestRefusesNonModelRequests(t *testing.T) {
	var seen http.Request
	up := upstream(t, &seen)
	defer up.Close()
	gw := gateway(t, up, 10)
	defer gw.Close()
	for _, c := range []struct{ method, path string }{
		{"GET", "/v1/messages"},
		{"POST", "/v1/models"},
		{"POST", "/v1/messages/../files"},
		{"POST", "/v1/files"},
	} {
		req, _ := http.NewRequest(c.method, gw.URL+c.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 && resp.StatusCode != 301 {
			t.Errorf("%s %s: got %d, want 403", c.method, c.path, resp.StatusCode)
		}
	}
	if seen.URL != nil {
		t.Errorf("a refused request reached upstream: %s", seen.URL.Path)
	}
}

func TestReplacesClientCredentialAndStreams(t *testing.T) {
	var seen http.Request
	up := upstream(t, &seen)
	defer up.Close()
	gw := gateway(t, up, 10)
	defer gw.Close()
	req, _ := http.NewRequest("POST", gw.URL+"/v1/messages?key=client", strings.NewReader("{}"))
	req.Header.Set("X-Api-Key", "client-supplied")
	req.Header.Set("Authorization", "Bearer client-supplied")
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, _ := bufio.NewReader(resp.Body).ReadString('\n')
	if first := time.Since(start); line != "data: one\n" || first > 250*time.Millisecond {
		t.Errorf("first event %q after %v: the gateway buffered the stream", line, first)
	}
	if got := seen.Header.Get("X-Api-Key"); got != "real-key" {
		t.Errorf("upstream saw X-Api-Key %q, want the gateway's", got)
	}
	if got := seen.Header.Get("Authorization"); got != "" {
		t.Errorf("client Authorization reached upstream: %q", got)
	}
	if seen.URL.Query().Get("key") != "" {
		t.Errorf("client key query parameter reached upstream")
	}
}

func TestRequestCap(t *testing.T) {
	var seen http.Request
	up := upstream(t, &seen)
	defer up.Close()
	gw := gateway(t, up, 2)
	defer gw.Close()
	codes := []int{}
	for i := 0; i < 3; i++ {
		resp, err := http.Post(gw.URL+"/v1/messages", "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		codes = append(codes, resp.StatusCode)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 {
		t.Errorf("codes %v, want [200 200 429]", codes)
	}
}
