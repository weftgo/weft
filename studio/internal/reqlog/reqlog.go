// Package reqlog is Studio's server log of failed requests (UX-E-37,
// audit row D10): every request carries a request id — the client's
// X-Request-Id when it sent a well-formed one, else a generated one —
// echoed on the response; a refused ingest batch (a 4xx on /v1/) is one
// WARN line and every 5xx one ERROR line on slog.Default, each naming
// the request id, the status and the reason the handler answered with
// (Refuse; Cause when the body masks the real error). Studio's error
// bodies carry the id on a 5xx too, so a report from a user meets its
// line in studio.log. A handler panic is one ERROR line with the id; the
// panic goes on up to net/http.
//
// Ingest refusals are rate-limited per (remote host, route, status,
// code), the route being its class — /v1/traces, /v1/logs, else /v1/*,
// so a scan of distinct /v1/<x> paths shares one key: the first in a
// minute is logged, the rest counted onto the next line (suppressed=n)
// — a misconfigured exporter retrying every second leaves one line a
// minute, never a flood. The limiter holds at most 1,024 keys: full, it
// drops the expired windows first — the refusals they had suppressed
// since their last line are carried onto the next line logged
// (suppressed_expired=n), never lost with them; when none has expired,
// a refusal under a new key is not logged until a window turns, and the
// next line logged carries the count (suppressed_overflow=n). Behind a reverse
// proxy every client arrives from the proxy's address, so they share
// one remote and one limit. 5xx lines are never limited.
//
// The logger also counts the ingest batches (POSTs under /v1/) it saw
// accepted and refused, with the last refusal and its time (Ingest):
// /api/meta's ingest section.
package reqlog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
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
// cause, when set, is what the log names instead of reason: the real
// error behind a body that masks it (Cause).
type info struct {
	id     string
	code   string
	reason string
	cause  string
}

// logged is the reason the log names: the cause when one was recorded.
func (in *info) logged() string {
	if in.cause != "" {
		return in.cause
	}
	return in.reason
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

// Cause records the real reason behind a refusal whose body masks it (a
// 5xx answered to a panel token names the operation, never the
// database's error text): the server log names msg, the body keeps what
// Refuse was given. A no-op outside Wrap.
func Cause(ctx context.Context, msg string) {
	if in, _ := ctx.Value(ctxKey{}).(*info); in != nil {
		in.cause = msg
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

// Logger is one server's request log: its rate limit's state and the
// ingest counters.
type Logger struct {
	mu   sync.Mutex
	seen map[string]*window
	// overflow counts the refusals not logged because the limiter was
	// full of live windows; the next line logged carries it.
	overflow int
	// expired carries the suppressed counts of the windows the full map
	// dropped: the next line logged reports them.
	expired int
	now     func() time.Time
	ingest  IngestCounts
}

// IngestCounts is what the logger saw of ingest (POSTs under /v1/):
// the batches answered 2xx, the batches refused (4xx and 5xx), and the
// last refusal — "<status> <code>: <reason>" — with its time (zero
// before the first).
type IngestCounts struct {
	Accepted  int64
	Refused   int64
	LastError string
	LastAt    time.Time
}

// Ingest returns the ingest counters so far: in memory, a restart
// starts them again from zero.
func (l *Logger) Ingest() IngestCounts {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ingest
}

// countIngest folds one answered ingest batch into the counters.
func (l *Logger) countIngest(status int, in *info) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if status < 400 {
		l.ingest.Accepted++
		return
	}
	l.ingest.Refused++
	msg := strconv.Itoa(status)
	if in.code != "" {
		msg += " " + in.code
	}
	if r := in.logged(); r != "" {
		msg += ": " + r
	}
	l.ingest.LastError, l.ingest.LastAt = msg, l.now().UTC()
}

type window struct {
	start      time.Time
	suppressed int
}

// limitWindow is the ingest refusal limit's window per key.
const limitWindow = time.Minute

// maxKeys bounds the limiter's map: full, it drops the expired windows,
// else stops logging new keys until one turns.
const maxKeys = 1024

// New returns a request log.
func New() *Logger { return &Logger{seen: map[string]*window{}, now: time.Now} }

// allow reports whether a refusal under key is logged now, how many
// under it were suppressed since its last line, how many went unlogged
// because the limiter was full, and how many the dropped expired
// windows had suppressed (the last two reset once reported).
func (l *Logger) allow(key string) (ok bool, suppressed, overflow, expired int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	w := l.seen[key]
	if w != nil && now.Sub(w.start) < limitWindow {
		w.suppressed++
		return false, 0, 0, 0
	}
	if w != nil {
		suppressed = w.suppressed
	}
	if w == nil && len(l.seen) >= maxKeys {
		for k, old := range l.seen {
			if now.Sub(old.start) >= limitWindow {
				l.expired += old.suppressed
				delete(l.seen, k)
			}
		}
		if len(l.seen) >= maxKeys {
			l.overflow++
			return false, 0, 0, 0
		}
	}
	l.seen[key] = &window{start: now}
	overflow, l.overflow = l.overflow, 0
	expired, l.expired = l.expired, 0
	return true, suppressed, overflow, expired
}

// routeClass is a key's route: the two OTLP routes by name, every other
// /v1/ path one class.
func routeClass(path string) string {
	switch path {
	case "/v1/traces", "/v1/logs":
		return path
	}
	return "/v1/*"
}

// Wrap gives every request its id (on the context and the response's
// X-Request-Id) and logs, once it is answered, a 5xx at ERROR and a
// refused ingest batch (a 4xx under /v1/) at WARN. A handler panic is
// logged at ERROR with the id and panics on (http.ErrAbortHandler,
// net/http's deliberate abort, is not logged).
func (l *Logger) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(Header)
		if !validID(id) {
			id = newID()
		}
		in := &info{id: id}
		w.Header().Set(Header, id)
		sw := &statusWriter{ResponseWriter: w}
		var rw http.ResponseWriter = sw
		if _, ok := w.(http.Flusher); ok {
			rw = flushWriter{sw}
		}
		defer func() {
			if v := recover(); v != nil {
				if err, _ := v.(error); err != http.ErrAbortHandler { //nolint:errorlint // net/http compares the value itself
					slog.Error("studio: handler panicked",
						append(l.attrs(r, in, http.StatusInternalServerError, 0, 0, 0), "panic", fmt.Sprint(v))...)
				}
				panic(v)
			}
		}()
		next.ServeHTTP(rw, r.WithContext(context.WithValue(r.Context(), ctxKey{}, in)))
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		ingest := strings.HasPrefix(r.URL.Path, "/v1/")
		if ingest && r.Method == http.MethodPost {
			l.countIngest(status, in)
		}
		switch {
		case status >= 500:
			slog.Error("studio: request failed", l.attrs(r, in, status, 0, 0, 0)...)
		case status >= 400 && ingest:
			key := hostOf(r.RemoteAddr) + "|" + routeClass(r.URL.Path) + "|" + in.code + "|" + http.StatusText(status)
			if ok, n, over, exp := l.allow(key); ok {
				slog.Warn("studio: ingest batch refused", l.attrs(r, in, status, n, over, exp)...)
			}
		}
	})
}

func (l *Logger) attrs(r *http.Request, in *info, status, suppressed, overflow, expired int) []any {
	a := []any{
		"request_id", in.id, "status", status, "method", r.Method, "path", r.URL.Path,
		"remote", hostOf(r.RemoteAddr),
	}
	if in.code != "" {
		a = append(a, "code", in.code)
	}
	if reason := in.logged(); reason != "" {
		a = append(a, "reason", reason)
	}
	if r.ContentLength > 0 {
		a = append(a, "bytes", r.ContentLength)
	}
	if suppressed > 0 {
		a = append(a, "suppressed", suppressed)
	}
	if overflow > 0 {
		a = append(a, "suppressed_overflow", overflow)
	}
	if expired > 0 {
		a = append(a, "suppressed_expired", expired)
	}
	return a
}

func hostOf(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// statusWriter keeps the status a handler wrote; Unwrap keeps
// http.ResponseController working. It is an http.Flusher only as
// flushWriter, which Wrap picks when the underlying writer flushes — so
// a handler's w.(http.Flusher) check (the live stream, the runtime
// link) answers what the connection can do.
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

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// flushWriter is a statusWriter over a writer that flushes.
type flushWriter struct{ *statusWriter }

func (w flushWriter) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.ResponseWriter.(http.Flusher).Flush()
}
