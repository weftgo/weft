// Package reqlog is Studio's server log of failed requests (UX-E-37,
// audit row D10): every request carries a request id — the client's
// X-Request-Id when it sent a well-formed one, else a generated one —
// echoed on the response; a refused ingest batch (a 4xx on /v1/) is one
// WARN line and every 5xx one ERROR line on slog.Default, each naming
// the request id, the status and the reason the handler answered with
// (Refuse). Studio's error bodies carry the id on a 5xx too, so a
// report from a user meets its line in studio.log.
//
// Ingest refusals are rate-limited per (remote host, status, code): the
// first in a minute is logged, the rest counted onto the next line
// (suppressed=n) — a misconfigured exporter retrying every second
// leaves one line a minute, never a flood. 5xx lines are never limited.
package reqlog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Header is the request id's header, read from the request and set on
// every response.
const Header = "X-Request-Id"

// maxID bounds a client's id: longer, or carrying anything but
// [A-Za-z0-9._:-], and Studio generates its own.
const maxID = 64

type ctxKey struct{}

// info is one request's: its id and the reason a handler refused it.
type info struct {
	id     string
	code   string
	reason string
}

// ID is the request's id, "" outside Wrap.
func ID(ctx context.Context) string {
	if in, _ := ctx.Value(ctxKey{}).(*info); in != nil {
		return in.id
	}
	return ""
}

// Refuse records why the request is answered with an error (the error
// body's code and message): the line Wrap logs names it. A no-op
// outside Wrap.
func Refuse(ctx context.Context, code, msg string) {
	if in, _ := ctx.Value(ctxKey{}).(*info); in != nil {
		in.code, in.reason = code, msg
	}
}

// validID reports whether a client's id is kept.
func validID(s string) bool {
	if s == "" || len(s) > maxID {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == ':', c == '-':
		default:
			return false
		}
	}
	return true
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Logger is one server's request log: its rate limit's state.
type Logger struct {
	mu   sync.Mutex
	seen map[string]*window
	now  func() time.Time
}

type window struct {
	start      time.Time
	suppressed int
}

// limitWindow is the ingest refusal limit's window per key.
const limitWindow = time.Minute

// maxKeys bounds the limiter's map; past it the map starts over.
const maxKeys = 1024

// New returns a request log.
func New() *Logger { return &Logger{seen: map[string]*window{}, now: time.Now} }

// allow reports whether a refusal under key is logged now and how many
// were suppressed since the last line.
func (l *Logger) allow(key string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	w := l.seen[key]
	if w != nil && now.Sub(w.start) < limitWindow {
		w.suppressed++
		return false, 0
	}
	n := 0
	if w != nil {
		n = w.suppressed
	}
	if w == nil && len(l.seen) >= maxKeys {
		l.seen = map[string]*window{}
	}
	l.seen[key] = &window{start: now}
	return true, n
}

// Wrap gives every request its id (on the context and the response's
// X-Request-Id) and logs, once it is answered, a 5xx at ERROR and a
// refused ingest batch (a 4xx under /v1/) at WARN.
func (l *Logger) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(Header)
		if !validID(id) {
			id = newID()
		}
		in := &info{id: id}
		w.Header().Set(Header, id)
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), ctxKey{}, in)))
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		switch {
		case status >= 500:
			slog.Error("studio: request failed", l.attrs(r, in, status, 0)...)
		case status >= 400 && strings.HasPrefix(r.URL.Path, "/v1/"):
			ok, n := l.allow(hostOf(r.RemoteAddr) + "|" + r.URL.Path + "|" + in.code + "|" + http.StatusText(status))
			if ok {
				slog.Warn("studio: ingest batch refused", l.attrs(r, in, status, n)...)
			}
		}
	})
}

func (l *Logger) attrs(r *http.Request, in *info, status, suppressed int) []any {
	a := []any{
		"request_id", in.id, "status", status, "method", r.Method, "path", r.URL.Path,
		"remote", hostOf(r.RemoteAddr),
	}
	if in.code != "" {
		a = append(a, "code", in.code)
	}
	if in.reason != "" {
		a = append(a, "reason", in.reason)
	}
	if r.ContentLength > 0 {
		a = append(a, "bytes", r.ContentLength)
	}
	if suppressed > 0 {
		a = append(a, "suppressed", suppressed)
	}
	return a
}

func hostOf(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// statusWriter keeps the status a handler wrote; Flush and Unwrap keep
// the live stream's flushing and http.ResponseController working.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
