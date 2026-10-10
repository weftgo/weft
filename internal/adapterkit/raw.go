package adapterkit

import (
	"bytes"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/weftgo/weft/core"
)

// RawCap is the most of one wire body the adapters keep for a raw
// record (ADR 0028 §12): 256 KiB, the core's own cap. What is past it
// is counted, never buffered.
const RawCap = 256 << 10

// CaptureRaw sends r through next and, when the run's reporter wants
// raw records (core.Reporter.WantsRaw, asked before anything is
// buffered), reports the exchange as one core.RawPair: the request line,
// the headers minus sensitive (the adapter's SDK's own list — the core
// strips its floor again, case-insensitively) and the body; the status,
// headers and body of the response, the body teed as the caller reads
// it — a streamed response is the raw SSE bytes as received — and
// reported once, at its EOF, read error or Close. Each body is kept up
// to RawCap and the rest counted.
//
// It is reporting, never behaviour: the request the next layer gets
// carries the same bytes, the response the caller reads is the same
// stream, and a capture that fails (the request body cannot be read)
// drops the pair with a Debug line and sends the request on with the
// read error where the original body would have surfaced it. One call
// per HTTP request: an SDK's own transport retries report one pair
// each.
func CaptureRaw(r *http.Request, next func(*http.Request) (*http.Response, error), sensitive []string) (*http.Response, error) {
	rep := core.ReportFromContext(r.Context())
	if !rep.WantsRaw() {
		return next(r)
	}
	r = r.Clone(r.Context()) // the body is replaced below; the caller's request stays its own
	pair := core.RawPair{Method: r.Method, URL: r.URL.String(), MediaType: mediaType(r.Header)}
	redacted := map[string]bool{}
	pair.RequestHeaders = keepHeaders(r.Header, sensitive, redacted)
	if r.Body != nil && r.Body != http.NoBody {
		b, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			slog.Debug("weft adapter: raw capture dropped: the request body could not be read", "err", err.Error())
			r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(b), errReader{err}))
			return next(r)
		}
		r.Body = io.NopCloser(bytes.NewReader(b))
		pair.Request, pair.RequestTruncated = capped(b)
	}
	resp, err := next(r)
	if err != nil || resp == nil {
		pair.RedactedHeaders = names(redacted)
		rep.Raw(pair)
		return resp, err
	}
	pair.Status = resp.StatusCode
	pair.ResponseMediaType = mediaType(resp.Header)
	pair.ResponseHeaders = keepHeaders(resp.Header, sensitive, redacted)
	pair.RedactedHeaders = names(redacted)
	if resp.Body == nil || resp.Body == http.NoBody {
		rep.Raw(pair)
		return resp, nil
	}
	resp.Body = &teeBody{rc: resp.Body, length: resp.ContentLength, done: func(body []byte, dropped int64, complete bool) {
		pair.Response, pair.ResponseTruncated, pair.ResponseIncomplete = body, dropped, !complete
		rep.Raw(pair)
	}}
	return resp, nil
}

// RawTransport wraps base (nil: http.DefaultTransport) so every request
// through it is captured as CaptureRaw does — for an SDK that offers no
// per-request middleware (google's genai, whose client takes an
// *http.Client).
func RawTransport(base http.RoundTripper, sensitive []string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return rawTransport{base: base, sensitive: sensitive}
}

type rawTransport struct {
	base      http.RoundTripper
	sensitive []string
}

func (t rawTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return CaptureRaw(r, t.base.RoundTrip, t.sensitive)
}

// keepHeaders copies h minus the names in sensitive (case-insensitive),
// adding each stripped name, lowercased, to redacted.
func keepHeaders(h http.Header, sensitive []string, redacted map[string]bool) map[string][]string {
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		lk := strings.ToLower(k)
		if slices.Contains(sensitive, lk) {
			redacted[lk] = true
			continue
		}
		out[k] = slices.Clone(vs)
	}
	return out
}

func names(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// mediaType is the header's Content-Type without its parameters.
func mediaType(h http.Header) string {
	ct := h.Get("Content-Type")
	if ct == "" {
		return ""
	}
	if mt, _, err := mime.ParseMediaType(ct); err == nil {
		return mt
	}
	return ct
}

// capped is b's first RawCap bytes and the count of the rest.
func capped(b []byte) ([]byte, int64) {
	if len(b) <= RawCap {
		return b, 0
	}
	return b[:RawCap], int64(len(b) - RawCap)
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// teeBody passes a response body through unchanged, keeping its first
// RawCap bytes and counting the rest, and calls done once — at EOF, at
// a read error, or at Close, whichever comes first. complete is true
// only at EOF: a body closed before it (an SDK's transport retry
// discarding a 429/5xx unread, a timeout, a reader that stopped) or
// ended by a read error is incomplete, and when Content-Length was
// known the bytes never read are counted as dropped.
type teeBody struct {
	rc      io.ReadCloser
	length  int64 // Content-Length, -1 when unknown
	mu      sync.Mutex
	buf     []byte
	dropped int64
	eof     bool
	sealed  bool // reported: later reads pass through untouched
	once    sync.Once
	done    func(body []byte, dropped int64, complete bool)
}

func (t *teeBody) Read(p []byte) (int, error) {
	n, err := t.rc.Read(p)
	if n > 0 {
		t.mu.Lock()
		if t.sealed {
			t.mu.Unlock()
			return n, err
		}
		keep := min(n, RawCap-len(t.buf))
		if keep > 0 {
			t.buf = append(t.buf, p[:keep]...)
		}
		t.dropped += int64(n - max(keep, 0))
		t.mu.Unlock()
	}
	if err != nil {
		if err == io.EOF {
			t.mu.Lock()
			t.eof = true
			t.mu.Unlock()
		}
		t.finish()
	}
	return n, err
}

func (t *teeBody) Close() error {
	t.finish()
	return t.rc.Close()
}

func (t *teeBody) finish() {
	t.once.Do(func() {
		t.mu.Lock()
		body, dropped, complete := t.buf, t.dropped, t.eof
		if !complete && t.length >= 0 {
			dropped += max(t.length-int64(len(body))-t.dropped, 0)
		}
		t.sealed = true
		t.mu.Unlock()
		t.done(body, dropped, complete)
	})
}
