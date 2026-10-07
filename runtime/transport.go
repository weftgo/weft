package runtime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
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
// (studio/runtime's RuntimeServer, wired into studio.Server at the
// lane-C merge); the handler is still the public in-process side and
// this transport is how Local(srv) reaches it.
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
		defer func() {
			// net/http's server contains a handler panic per connection;
			// in-process this goroutine is the connection. Without the
			// same containment a panic in Studio's handler would take the
			// app down — over a socket it would cost one response.
			if r := recover(); r != nil {
				slog.Error("weft/runtime: in-process Studio handler panicked", "path", req.URL.Path, "panic", fmt.Sprint(r))
				rec.WriteHeader(http.StatusInternalServerError)
				_ = pw.CloseWithError(io.ErrUnexpectedEOF)
				return
			}
			_ = pw.Close()
		}()
		t.h.ServeHTTP(rec, req.Clone(ctx))
	}()
	// The client needs StatusCode and Header the moment RoundTrip
	// returns; wait for the handler's first write (or its end).
	select {
	case <-rec.ready:
	case <-rec.done:
	case <-req.Context().Done():
		// The caller's deadline (the register timeout, the link's
		// shutdown) holds here as it does on a socket: a handler that
		// has not answered does not hold the caller. Closing the read
		// side fails the handler's late writes instead of blocking them.
		cancel()
		_ = pr.CloseWithError(req.Context().Err())
		return nil, req.Context().Err()
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
	sent   http.Header // header as of the first write: what the client sees
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
		w.sent = w.header.Clone()
		close(w.ready)
	}
}

func (w *pipeResponseWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	if !w.wrote {
		w.wrote = true
		w.sent = w.header.Clone()
		close(w.ready)
	}
	w.mu.Unlock()
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

// headerSnapshot returns the headers as of the first write — taken on
// the handler's own goroutine when it wrote, so the client never reads
// the map the handler may still be setting — or as the handler left
// them when it returned without writing.
func (w *pipeResponseWriter) headerSnapshot() http.Header {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sent != nil {
		return w.sent
	}
	return w.header.Clone()
}

// bearerAuth sets the Studio token on a request (the Studio
// destination's token, S4.3's auth for the runtime-link routes; empty
// in setup A).
func bearerAuth(req *http.Request, token string) {
	if token != "" && !strings.HasPrefix(req.Header.Get("Authorization"), "Bearer ") {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}
