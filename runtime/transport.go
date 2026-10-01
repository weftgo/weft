package runtime

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/weftgo/weft/studio"
)

// inProcessClient is setup A's transport: an http.Client whose
// RoundTripper invokes the embedded Studio's own handler — a true
// in-process call, no socket, no loopback bind. The link's client code
// is unchanged: POSTs and the SSE command stream go through
// *http.Client like any remote Studio, only the bytes never leave the
// process.
//
// Server.Runtime() is the typed shortcut S4.1 names for this
// (studio/runtime's RuntimeServer); until step 8's merge wires it
// into the studio.Server placeholder (see notes-lane-c2.md), the
// handler is the public in-process side and this transport is how
// Local(srv) reaches it.
func inProcessClient(srv *studio.Server) *http.Client {
	return &http.Client{
		Transport:     &handlerTransport{h: srv.Handler()},
		Timeout:       0, // the SSE stream is long-lived
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// handlerTransport adapts an http.Handler to an http.RoundTripper.
type handlerTransport struct{ h http.Handler }

func (t *handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		// The client's request body is replayable here in one shot;
		// buffer it so the goroutine below owns its copy.
		body, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
	} else {
		req.Body = http.NoBody
	}
	pr, pw := io.Pipe()
	rec := newPipeResponseWriter()
	rec.pw = pw
	ctx, cancel := context.WithCancel(req.Context())
	go func() {
		defer close(rec.done)
		t.h.ServeHTTP(rec, req.Clone(ctx))
		_ = pw.Close()
	}()
	// The client needs StatusCode and Header the moment RoundTrip
	// returns; wait for the handler's first write (or its end).
	select {
	case <-rec.ready:
	case <-rec.done:
	}
	resp := &http.Response{
		StatusCode: rec.status(),
		Status:     http.StatusText(rec.status()),
		Header:     rec.headerSnapshot(),
		Body:       &cancelBody{r: pr, cancel: cancel},
		Request:    req,
		Close:      true, // every trip is a fresh handler run
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
	}
	return resp, nil
}

// cancelBody ties the response body's lifetime to the handler's
// context: closing the body cancels the handler (the SSE stream ends
// when the client stops reading).
type cancelBody struct {
	r      io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
}

func (b *cancelBody) Read(p []byte) (int, error) { return b.r.Read(p) }

func (b *cancelBody) Close() error {
	b.once.Do(func() {
		b.cancel()
		_ = b.r.Close()
	})
	return nil
}

// pipeResponseWriter is the ResponseWriter side of the in-process
// call: headers and status are captured synchronously, the body
// streams through an io.Pipe so SSE frames surface as they are
// written (every Write wakes the reader; Flush is a no-op by
// construction).
type pipeResponseWriter struct {
	mu     sync.Mutex
	pw     *io.PipeWriter
	header http.Header
	code   int
	wrote  bool

	ready chan struct{} // closed on the first write
	done  chan struct{} // closed when the handler returns
}

func newPipeResponseWriter() *pipeResponseWriter {
	return &pipeResponseWriter{
		header: make(http.Header),
		ready:  make(chan struct{}),
		done:   make(chan struct{}),
	}
}

func (w *pipeResponseWriter) Header() http.Header { return w.header }

func (w *pipeResponseWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.wrote {
		w.wrote, w.code = true, code
		close(w.ready)
	}
}

func (w *pipeResponseWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	first := !w.wrote
	w.wrote = true
	w.mu.Unlock()
	if first {
		close(w.ready)
	}
	return w.pw.Write(p)
}

func (w *pipeResponseWriter) Flush() {}

// status returns the status code written (200 when the handler never
// called WriteHeader).
func (w *pipeResponseWriter) status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.code == 0 {
		return http.StatusOK
	}
	return w.code
}

// headerSnapshot copies the headers as of the first write — the
// client must not see later mutations.
func (w *pipeResponseWriter) headerSnapshot() http.Header {
	w.mu.Lock()
	defer w.mu.Unlock()
	clone := make(http.Header, len(w.header))
	for k, v := range w.header {
		clone[k] = append([]string(nil), v...)
	}
	return clone
}

// bearerAuth sets the Studio token on a request (the Studio
// destination's token, S4.3's auth for the runtime-link routes; empty
// in setup A).
func bearerAuth(req *http.Request, token string) {
	if token != "" && !strings.HasPrefix(req.Header.Get("Authorization"), "Bearer ") {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}
