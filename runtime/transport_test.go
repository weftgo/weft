package runtime

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestInProcessClient pins the Local(srv) transport: POSTs and GETs
// against a handler, in-process — status codes, headers, bodies, and
// an SSE stream that surfaces frames as they are written, no socket
// anywhere.
func TestInProcessClient(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /echo", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Test", "yes")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write(append([]byte("echo:"), b...))
	})
	mux.HandleFunc("GET /stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for i := 0; i < 3; i++ {
			_, _ = fmt.Fprintf(w, "id: %d\nevent: tick\ndata: {\"n\":%d}\n\n", i, i)
			fl.Flush()
			time.Sleep(10 * time.Millisecond)
		}
	})
	client := &http.Client{Transport: &handlerTransport{h: mux}}

	// A POST: status, header, body.
	resp, err := client.Post("http://weft.studio.local/echo", "text/plain", strings.NewReader("hi"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Test") != "yes" {
		t.Errorf("X-Test = %q", resp.Header.Get("X-Test"))
	}
	if string(body) != "echo:hi" {
		t.Errorf("body = %q", body)
	}

	// The stream: the first frame surfaces while the handler is still
	// writing (a buffered recorder could not do that).
	sresp, err := client.Get("http://weft.studio.local/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sresp.Body.Close() }()
	if ct := sresp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("content type = %q", ct)
	}
	events := scanSSE(bufio.NewReader(sresp.Body))
	ev, err := events()
	if err != nil {
		t.Fatalf("first frame: %v", err)
	}
	if ev.id != "0" || ev.event != "tick" || string(ev.data) != `{"n":0}` {
		t.Errorf("first frame = %+v %s", ev, ev.data)
	}
	// Drain the rest so the handler goroutine finishes cleanly.
	for {
		if _, err := events(); err != nil {
			break
		}
	}
}

// TestBearerAuth pins the token header the link sets on every request.
func TestBearerAuth(t *testing.T) {
	var mu sync.Mutex
	var got string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.Header.Get("Authorization")
		mu.Unlock()
	}))
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodGet, ts.URL, nil)
	bearerAuth(req, "sekret")
	_, _ = http.DefaultClient.Do(req)
	mu.Lock()
	defer mu.Unlock()
	if got != "Bearer sekret" {
		t.Errorf("Authorization = %q", got)
	}
}
