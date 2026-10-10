package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
)

// The raw record (ADR 0028 §12, plan A6): one attempt's wire request
// and response as the adapter sent and received them, reported through
// Reporter.Raw and emitted as one weft.raw log record on the chat
// span's context, beside the attempt's request record. It is content
// of another size, so it is asked for by its own EventName: nothing is
// built unless a destination answers Enabled for weft.raw — which
// weft/otel's pipeline does only under otel.RawBodies(true) and
// WEFT_ENV=dev, and only for its Local and Studio destinations.

const (
	eventNameRaw = "weft.raw"
	// attrRawIndex is the raw record's per-run position (the dedup
	// key's pos), contiguous from 0 in emission order.
	attrRawIndex = attribute.Key("weft.raw.index")
	// attrTruncatedBytes mirrors weft/otel's cut attribute: the bytes a
	// cap removed. On a raw record it is the sum over both bodies.
	attrTruncatedBytes = attribute.Key("weft.content.truncated_bytes")

	// rawBodyCap is the most of one body a raw record carries (256
	// KiB); the rest is counted in the body's truncated_bytes and the
	// record's weft.content.truncated_bytes, never cut silently.
	rawBodyCap = 256 << 10

	// rawRedacted replaces a sensitive URL query value.
	rawRedacted = "REDACTED"
)

// rawSensitiveHeaders are stripped from every raw record whatever the
// reporter did (ADR 0028 §12): the credentials the first-party SDKs set
// and the cookie pair. Compared case-insensitively.
var rawSensitiveHeaders = []string{
	"authorization", "proxy-authorization", "x-api-key", "api-key",
	"cookie", "set-cookie", "x-goog-api-key",
}

// sensitiveHeader reports whether a header must not be recorded: one of
// rawSensitiveHeaders, or a name that reads as a credential (it
// contains "auth", "secret", "apikey", "api-key" or "api_key", or ends
// in -key, _key, -token or _token). The list is the floor; the
// heuristic catches a gateway's own credential header.
func sensitiveHeader(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if slices.Contains(rawSensitiveHeaders, n) {
		return true
	}
	for _, s := range []string{"auth", "secret", "apikey", "api-key", "api_key"} {
		if strings.Contains(n, s) {
			return true
		}
	}
	for _, s := range []string{"-key", "_key", "-token", "_token"} {
		if strings.HasSuffix(n, s) {
			return true
		}
	}
	return false
}

// sensitiveValue reports a header value that carries a credential
// whatever its header is called: an HTTP auth scheme's token.
func sensitiveValue(v string) bool {
	l := strings.ToLower(strings.TrimSpace(v))
	return strings.HasPrefix(l, "bearer ") || strings.HasPrefix(l, "basic ")
}

// sensitiveParam reports a URL query parameter whose value is not
// recorded: a sensitive header's name, or one of the query spellings
// of a credential.
func sensitiveParam(name string) bool {
	switch strings.ToLower(name) {
	case "key", "token", "sig", "signature", "code", "password", "access_token":
		return true
	}
	return sensitiveHeader(name)
}

// rawBody is the raw record's body (ADR 0028 §12).
type rawBody struct {
	Step            int         `json:"step"`
	Attempt         int64       `json:"attempt"`
	Request         rawMessage  `json:"request"`
	Response        *rawMessage `json:"response"`
	RedactedHeaders []string    `json:"redacted_headers"`
	RedactedQuery   []string    `json:"redacted_query,omitempty"`
}

// rawMessage is one side of the pair. Body is the bytes as a string
// when they are UTF-8, else base64 with Encoding "base64"; Bytes is the
// whole body's size before the cap, TruncatedBytes what the cap (the
// reporter's and the record's) removed.
type rawMessage struct {
	Method         string              `json:"method,omitempty"`
	URL            string              `json:"url,omitempty"`
	Status         int                 `json:"status,omitempty"`
	Headers        map[string][]string `json:"headers"`
	MediaType      string              `json:"media_type,omitempty"`
	Body           string              `json:"body"`
	Encoding       string              `json:"encoding,omitempty"`
	Bytes          int64               `json:"bytes"`
	TruncatedBytes int64               `json:"truncated_bytes,omitempty"`
}

// rawWanted is the Enabled question for weft.raw: false outside a
// run's model call (no recorder), after the call ended, under
// weft.Content(false), or when no destination asks. Content(true) does
// not force it — wire bodies are recorded only where asked for.
func (s *stepReport) rawWanted() (ok bool) {
	if s.rec == nil || s.ended.Load() {
		return false
	}
	if s.rec.capture != nil && !*s.rec.capture {
		return false
	}
	defer func() {
		if recover() != nil {
			ok = false
			if s.panics != nil {
				s.panics.Add(1)
			}
		}
	}()
	return s.rec.elog.Enabled(s.Context, log.EnabledParameters{EventName: eventNameRaw})
}

// raw receives an attempt's wire bodies. Nobody asking (rawWanted
// false), the report is dropped and only its sizes reach a Debug line;
// otherwise it becomes one raw record, attributed to the attempt in
// flight — the one the reporter's next Attempt report will number, so
// a layer reports Raw before the Attempt of the same request — and
// emitted under the lock the call's end takes, like a further
// attempt's request record (ADR 0028 §7).
func (s *stepReport) raw(p RawPair) {
	if s.ended.Load() {
		return
	}
	defer s.contain()
	if !s.rawWanted() {
		if l := s.obs.logger(); l.Enabled(s.Context, slog.LevelDebug) {
			l.LogAttrs(s.Context, slog.LevelDebug, "model raw dropped",
				slog.String(logRun, s.runID),
				slog.Int(logStep, s.step),
				slog.String(logMediaType, p.MediaType),
				slog.Int(logRequestBytes, len(p.Request)),
				slog.Int(logResponseBytes, len(p.Response)))
		}
		return
	}
	body, cut := buildRaw(s.step, s.n.Load()+1, p)
	b, err := json.Marshal(body)
	if err != nil {
		s.rec.debug(s.Context, s.step, "raw", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended.Load() {
		return
	}
	s.rec.emitRaw(s.Context, s.step, body.Attempt, cut, b)
}

// emitRaw writes one raw record.
func (r *recorder) emitRaw(ctx context.Context, step int, attempt, cut int64, body []byte) {
	attrs := []attribute.KeyValue{
		attrRecord.String("raw"),
		attrRunID.String(r.runID),
		attrContent.String(contentFull),
		attrRawIndex.Int64(r.rawIdx.Add(1) - 1),
		attrStepIndex.Int(step),
		attrAttemptIndex.Int64(attempt),
	}
	if cut > 0 {
		attrs = append(attrs, attrTruncatedBytes.Int64(cut))
	}
	r.emitRecord(ctx, step, eventNameRaw, body, attrs)
}

// buildRaw shapes a reported pair into the record's body: the
// sensitive headers stripped from both sides (whatever the reporter
// already stripped is listed too), the URL's userinfo dropped and its
// sensitive query values replaced, each body capped at rawBodyCap. cut
// is the bytes removed from both bodies, the reporter's own cuts
// included.
func buildRaw(step int, attempt int64, p RawPair) (rawBody, int64) {
	redacted := map[string]bool{}
	for _, h := range p.RedactedHeaders {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			redacted[h] = true
		}
	}
	u, query := scrubURL(p.URL)
	req := rawMessage{
		Method:    p.Method,
		URL:       u,
		Headers:   keepHeaders(p.RequestHeaders, redacted),
		MediaType: p.MediaType,
	}
	req.Body, req.Encoding, req.Bytes, req.TruncatedBytes = capRaw(p.Request, p.RequestTruncated)
	out := rawBody{Step: step, Attempt: attempt, Request: req, RedactedQuery: query}
	cut := req.TruncatedBytes
	if p.Status != 0 || len(p.Response) > 0 || len(p.ResponseHeaders) > 0 || p.ResponseTruncated > 0 {
		resp := rawMessage{
			Status:    p.Status,
			Headers:   keepHeaders(p.ResponseHeaders, redacted),
			MediaType: p.ResponseMediaType,
		}
		resp.Body, resp.Encoding, resp.Bytes, resp.TruncatedBytes = capRaw(p.Response, p.ResponseTruncated)
		cut += resp.TruncatedBytes
		out.Response = &resp
	}
	out.RedactedHeaders = make([]string, 0, len(redacted))
	for h := range redacted {
		out.RedactedHeaders = append(out.RedactedHeaders, h)
	}
	slices.Sort(out.RedactedHeaders)
	return out, cut
}

// keepHeaders copies the headers that may be recorded and adds every
// stripped name, lowercased, to redacted. Never nil, so the record
// always says "headers": {} rather than null.
func keepHeaders(h map[string][]string, redacted map[string]bool) map[string][]string {
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		if sensitiveHeader(k) || slices.ContainsFunc(vs, sensitiveValue) {
			redacted[strings.ToLower(strings.TrimSpace(k))] = true
			continue
		}
		out[k] = slices.Clone(vs)
	}
	return out
}

// scrubURL returns the URL with its userinfo dropped and each sensitive
// query parameter's values replaced, and the names it replaced (sorted,
// "userinfo" for the dropped credentials). A URL that does not parse is
// not recorded at all: "", ["url"].
func scrubURL(raw string) (string, []string) {
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", []string{"url"}
	}
	var names []string
	if u.User != nil {
		u.User = nil
		names = append(names, "userinfo")
	}
	if u.RawQuery != "" {
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			u.RawQuery = ""
			names = append(names, "query")
		} else {
			changed := false
			for k, vs := range q {
				if sensitiveParam(k) {
					for i := range vs {
						vs[i] = rawRedacted
					}
					names = append(names, k)
					changed = true
				}
			}
			if changed {
				u.RawQuery = q.Encode()
			}
		}
	}
	slices.Sort(names)
	return u.String(), names
}

// capRaw renders one body for the record: at most rawBodyCap bytes, cut
// back to a rune boundary when the bytes are UTF-8 text, as a string,
// or base64 when they are not UTF-8. size is the whole body's length
// (what the reporter cut included), cut what was removed.
func capRaw(b []byte, reporterCut int64) (body, encoding string, size, cut int64) {
	reporterCut = max(reporterCut, 0)
	size = int64(len(b)) + reporterCut
	text := utf8.Valid(b)
	keep := b
	if len(keep) > rawBodyCap {
		k := rawBodyCap
		// A text body is cut back to a rune start (at most three
		// bytes) so it stays text; a binary body is base64 either way.
		for text && k > 0 && !utf8.RuneStart(b[k]) {
			k--
		}
		keep = b[:k]
	}
	cut = int64(len(b)-len(keep)) + reporterCut
	if text {
		return string(keep), "", size, cut
	}
	return base64.StdEncoding.EncodeToString(keep), "base64", size, cut
}
