package mw

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"math"
	"math/rand/v2"
	"net"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/weftgo/weft"
)

// ErrRetryAfterTooLong is wrapped around a provider error whose
// retry-after ask exceeds MaxWait: rather than sleeping silently for
// minutes, Retry fails fast so an outer layer (a queue, a person) can
// decide. errors.Is on it, or on the provider's own error underneath.
var ErrRetryAfterTooLong = errors.New("mw: provider asked to retry after longer than the maximum wait")

const (
	defaultMaxRetries = 3
	defaultBaseDelay  = 500 * time.Millisecond
	defaultMaxDelay   = 8 * time.Second
	defaultMaxWait    = 60 * time.Second
)

// RetryOption configures Retry.
type RetryOption func(*retryConfig)

type retryConfig struct {
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
	maxWait    time.Duration
	classify   func(error) bool
}

// MaxRetries sets how many times a failed call is retried (default 3).
// Zero disables retrying while keeping the retry-after and classifier
// behaviour observable through Log. Negative values are ignored — the
// core options' rule, stated here too.
func MaxRetries(n int) RetryOption {
	return func(c *retryConfig) {
		if n >= 0 {
			c.maxRetries = n
		}
	}
}

// BaseDelay sets the first backoff delay (default 500ms). Delays double
// per attempt — 0.5s, 1s, 2s, … — capped at 8s, each with ±25% jitter,
// unless the provider names its own retry-after, which wins. Zero is
// meaningful (no delay between attempts); negative values are ignored.
func BaseDelay(d time.Duration) RetryOption {
	return func(c *retryConfig) {
		if d >= 0 {
			c.baseDelay = d
		}
	}
}

// MaxWait bounds a provider's retry-after ask (default 60s): a longer
// ask fails fast wrapping ErrRetryAfterTooLong instead of sleeping.
// MaxWait(0) removes the cap — whatever the provider asks, Retry
// waits — and negative values are ignored.
func MaxWait(d time.Duration) RetryOption {
	return func(c *retryConfig) {
		if d >= 0 {
			c.maxWait = d
		}
	}
}

// Classifier replaces the default retryability test. The default
// retries weft.ErrStreamIdle, net.Error values, and HTTP 408, 409, 429
// and 5xx responses reported by the vendor SDKs' error types; it never
// retries context cancellation, the kill switch, weft.ErrUnsupported,
// quota and billing 4xx, or a context-window overflow — the last must
// route to compaction, not to the same request again.
func Classifier(fn func(error) bool) RetryOption {
	return func(c *retryConfig) {
		if fn != nil {
			c.classify = fn
		}
	}
}

// Retry retries a model call that fails before yielding any event —
// the request-level failures a provider reports up front: rate limits,
// overload, transport errors, an idle stream. The vendor SDKs already
// retry at the transport layer (their MaxRetries adapter options);
// Retry is the loop-visible logic layer above them, with backoff the
// caller can see in Log and a retry-after the provider sends honoured
// (retry-after-ms, retry-after, and x-should-retry headers). A failure
// after events were yielded is never retried: part of the reply has
// already reached the loop. Exhausted retries return the last error
// wrapped, so errors.As on the SDK's type still works.
func Retry(opts ...RetryOption) weft.ModelMiddleware {
	cfg := retryConfig{
		maxRetries: defaultMaxRetries,
		baseDelay:  defaultBaseDelay,
		maxDelay:   defaultMaxDelay,
		maxWait:    defaultMaxWait,
		classify:   Retryable,
	}
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	return func(next weft.Model) weft.Model {
		return &retryModel{next: next, cfg: cfg}
	}
}

type retryModel struct {
	next weft.Model
	cfg  retryConfig
}

func (m *retryModel) Info() weft.ModelInfo { return weft.InfoOf(m.next) }

func (m *retryModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		for attempt := 0; ; attempt++ {
			yielded, failed := replay(ctx, m.next, req, yield)
			if failed == nil {
				return
			}
			if yielded || ctx.Err() != nil || !m.cfg.classify(failed) {
				yield(nil, failed)
				return
			}
			// The retry-after fail-fast outranks the attempt budget, so
			// even MaxRetries(0) reports ErrRetryAfterTooLong rather than
			// the raw error.
			delay, err := m.cfg.delay(attempt, failed, time.Now())
			if err != nil {
				yield(nil, err)
				return
			}
			if attempt >= m.cfg.maxRetries {
				if attempt == 0 {
					yield(nil, failed)
				} else {
					yield(nil, fmt.Errorf("mw: giving up after %d retries: %w", attempt, failed))
				}
				return
			}
			if !sleep(ctx, delay) {
				yield(nil, ctx.Err())
				return
			}
		}
	}
}

// delay picks the wait before retry number attempt+1: the provider's
// retry-after when it sent one (failing fast past maxWait — 0 meaning
// no cap), otherwise exponential backoff with jitter.
func (c *retryConfig) delay(attempt int, err error, now time.Time) (time.Duration, error) {
	if ask, ok := RetryAfter(err, now); ok && ask >= 0 {
		if c.maxWait > 0 && ask > c.maxWait {
			return 0, fmt.Errorf("%w: asked %s, maximum %s: %w", ErrRetryAfterTooLong, ask, c.maxWait, err)
		}
		return ask, nil
	}
	// No usable ask — none sent, or one RetryAfter rejected as
	// unconvertible (a parse that would wrap negative; see
	// fitsDuration). The defensive ask >= 0 above keeps even a future
	// parse path from sleeping zero on a wrapped value.
	return c.backoff(attempt), nil
}

func (c *retryConfig) backoff(attempt int) time.Duration {
	d := c.baseDelay
	for i := 0; i < attempt && d < c.maxDelay; i++ {
		d *= 2
	}
	d = min(d, c.maxDelay)
	if d <= 0 {
		return 0
	}
	// ±25% jitter, so a fleet of clients does not retry in lockstep. A
	// delay below 2ns has no jitter range (rand.Int64N(0) panics).
	if d/2 <= 0 {
		return d
	}
	jitter := time.Duration(rand.Int64N(int64(d)/2)) - d/4
	return d + jitter
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// Retryable is Retry's default classifier: true for weft.ErrStreamIdle,
// net.Error values, and HTTP 408, 409, 429 and 5xx statuses found on
// the error (see HTTPStatus); false for context errors, the kill
// switch, weft.ErrUnsupported, every other 4xx, and errors whose text
// names a context-window overflow. An x-should-retry header, when the
// provider sends one, overrides the status rule.
func Retryable(err error) bool {
	switch {
	case err == nil,
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, weft.ErrModelRequestsDenied),
		errors.Is(err, weft.ErrUnsupported),
		errors.Is(err, weft.ErrModelContract):
		return false
	case errors.Is(err, weft.ErrStreamIdle):
		return true
	case isContextOverflow(err):
		return false
	}
	if v := headerValue(err, "x-should-retry"); v != "" {
		return strings.EqualFold(v, "true")
	}
	if status, ok := HTTPStatus(err); ok {
		return status == 408 || status == 409 || status == 429 || status >= 500
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// overflowMarkers are substrings the providers use for a request that
// exceeds the model's context window. Overflow is a request-shape
// problem: retrying the same bytes cannot succeed.
var overflowMarkers = []string{
	"context_length_exceeded",
	"context length",
	"context window",
	"prompt is too long",
	"too many tokens",
	"maximum context",
	"input token count",
}

func isContextOverflow(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, m := range overflowMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// RetryAfter extracts the provider's retry-after ask from the error's
// HTTP response headers: retry-after-ms (milliseconds), then
// retry-after (seconds, or an HTTP date relative to now). ok is false
// when the error carries no such header, or carries one whose value
// cannot become a duration — unparseable, negative, or too large to
// convert (ParseFloat accepts 1e19 and Infinity; the int64 conversion
// of those wraps negative, which would slip past the maxWait cap and
// sleep zero, turning a misbehaving gateway's ask into up to
// MaxRetries+1 back-to-back requests).
func RetryAfter(err error, now time.Time) (time.Duration, bool) {
	if v := headerValue(err, "retry-after-ms"); v != "" {
		if ms, perr := strconv.ParseFloat(v, 64); perr == nil && fitsDuration(ms, float64(time.Millisecond)) {
			return time.Duration(ms * float64(time.Millisecond)), true
		}
	}
	v := headerValue(err, "retry-after")
	if v == "" {
		return 0, false
	}
	if secs, perr := strconv.ParseFloat(v, 64); perr == nil && fitsDuration(secs, float64(time.Second)) {
		return time.Duration(secs * float64(time.Second)), true
	}
	for _, layout := range []string{time.RFC1123, time.RFC1123Z, time.RFC850, time.ANSIC} {
		if t, perr := time.Parse(layout, v); perr == nil {
			return max(t.Sub(now), 0), true
		}
	}
	return 0, false
}

// fitsDuration reports whether f scaled by the unit converts to a
// time.Duration without wrapping: f must be finite, non-negative, and
// within int64 range once scaled. NaN fails every comparison, +Inf
// exceeds the bound, negatives are not waits. The bound is strict:
// float64(math.MaxInt64) rounds up to 2^63, so a value that lands
// exactly on it scales to a float the int64 conversion cannot hold
// (overflow is implementation-defined in Go, MinInt64 on amd64).
func fitsDuration(f, unit float64) bool {
	return f >= 0 && f < math.MaxInt64/unit
}

// HTTPStatus finds an HTTP status code on the error chain: the vendor
// SDKs' error types carry it as an exported StatusCode (openai-go,
// anthropic-sdk-go) or Code (google genai) integer field. The lookup is
// structural — mw imports no vendor SDK — and reports ok=false when no
// error on the chain has such a field.
func HTTPStatus(err error) (int, bool) {
	for _, e := range chain(err) {
		v := structOf(e)
		if !v.IsValid() {
			continue
		}
		for _, name := range []string{"StatusCode", "Code"} {
			f := v.FieldByName(name)
			if f.IsValid() && f.Kind() == reflect.Int && f.Int() >= 100 && f.Int() < 600 {
				return int(f.Int()), true
			}
		}
	}
	return 0, false
}

// headerValue finds a response header on the error chain: a Response
// field pointing at a struct with a Header that has a Get method
// (net/http's *Response, without importing net/http here).
func headerValue(err error, key string) string {
	for _, e := range chain(err) {
		v := structOf(e)
		if !v.IsValid() {
			continue
		}
		resp := v.FieldByName("Response")
		if !resp.IsValid() {
			continue
		}
		resp = structOf(resp.Interface())
		if !resp.IsValid() {
			continue
		}
		h := resp.FieldByName("Header")
		if !h.IsValid() || !h.CanInterface() {
			continue
		}
		if g, ok := h.Interface().(interface{ Get(string) string }); ok {
			if val := g.Get(key); val != "" {
				return val
			}
		}
	}
	return ""
}

func structOf(x any) reflect.Value {
	if x == nil {
		return reflect.Value{}
	}
	v := reflect.ValueOf(x)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	return v
}

// chain flattens err and everything it wraps, Join-style trees included.
func chain(err error) []error {
	var out []error
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		out = append(out, e)
		switch u := e.(type) {
		case interface{ Unwrap() error }:
			walk(u.Unwrap())
		case interface{ Unwrap() []error }:
			for _, w := range u.Unwrap() {
				walk(w)
			}
		}
	}
	walk(err)
	return out
}
