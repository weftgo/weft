// Package scope is the Go side of the devtools' Scope (plan §13.3):
// the unit every discovery rung, deep link and API call carries — one
// conversation's public id and, optionally, the session, flow and run
// inside it, or a page of unrelated runs named by an agent and run
// metadata tags (a test batch, U22) — and the net/http middleware
// that hands it to the page.
//
// A Scope has one string form, shared byte for byte with the web
// client's lib/scope.ts (the golden studio/testdata/scope.golden.json
// pins both):
//
//	pub_…;session=s_…;flow=f_…;run=r_…;agent=…;tag.<k>=<v>
//
// the public id first and bare, the rest optional, in that fixed
// order (the tags by key), empty fields omitted, each value — and a
// tag's key — percent-encoded with encodeURIComponent's rules. A scope
// without a public id starts with ";" (";tag.arena.batch=b_1"); Parse
// also reads a first segment that is a known key=value
// ("tag.arena.batch=b_1", what a page writes in data-scope). Parse
// reads the form back and never fails: a newer writer's field is not
// this reader's to reject.
//
// The flow is run metadata (weft.flow, FlowKey): Header stamps it on
// every run its handler starts, as it stamps the page URL, and
// WithFlow does the same for any context. Studio's run, session,
// search, trace and live reads filter on it (flow=).
//
// The response header is the development rung: thread has no HTTP
// layer, so the app's own handler sets it, in one line —
//
//	mux.Handle("POST /chat", scope.Header(chat, func(r *http.Request) scope.Scope {
//		return scope.Scope{PublicID: publicIDOf(r)}
//	}))
//
// and a handler that learns the run id only once its turn starts
// calls scope.Set(w, …) before it writes. The header never carries a
// token; a Scope has nowhere to put one.
//
// Header also learns the page the request came from — its Referer, else
// its Origin — and stamps it, sanitised, as weft.page_url on every run
// the handler starts (core.WithMetadata on the request's context: no
// option for the handler to pass, thread's Send included), so Studio's
// "open in app" can return to that page with #weft_scope= and the
// devtools panel scopes itself there. PageURLFromContext reads it back.
//
// The package lives beside the root rather than in it: the root
// package is a generated facade over core, which never imports
// net/http (ADR 0016, ADR 0027).
package scope

import (
	"context"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/weftgo/weft/core"
)

// HeaderName is the response header the app's handler sets and the
// panel reads.
const HeaderName = "Weft-Scope"

// PageURLKey is the metadata key Header stamps the request's page URL
// under, on every run its handler starts (and so on each run's spans
// and records, and on Studio's run row).
const PageURLKey = "weft.page_url"

// FlowKey is the metadata key a scope's flow is stamped under (Header,
// WithFlow): the run's flow, which Studio's reads filter on (flow=)
// and the devtools panel narrows its list by.
const FlowKey = "weft.flow"

// maxPageURL bounds a page URL: core's metadata value limit (1024
// bytes). A longer one is dropped, never truncated — a cut URL names
// another page.
const maxPageURL = 1024

// Scope names what the devtools follow. PublicID is the join key the
// app hands out ("" is the dev list: no conversation); the session,
// flow and run narrow it. Agent and Tags name a page of runs that
// share no conversation (a test harness, a batch: U22): the runs of
// that agent whose metadata carries every tag pair (ANDed); they may
// stand without a public id, and narrow it when one is set. The JSON
// names are the web client's.
type Scope struct {
	PublicID  string `json:"publicId"`
	SessionID string `json:"session,omitempty"`
	FlowID    string `json:"flow,omitempty"`
	RunID     string `json:"run,omitempty"`
	Agent     string `json:"agent,omitempty"`
	// Tags are run metadata pairs (core.WithMetadata's keys:
	// "arena.batch" → "cli-20261012-115753"), each written tag.<k>=<v>.
	Tags map[string]string `json:"tags,omitempty"`
}

// IsZero reports whether every field is empty.
func (s Scope) IsZero() bool {
	return s.PublicID == "" && s.SessionID == "" && s.FlowID == "" && s.RunID == "" && s.Agent == "" && len(s.Tags) == 0
}

// Equal reports whether s and o name the same scope: every field
// equal, a nil and an empty Tags alike.
func (s Scope) Equal(o Scope) bool {
	return s.PublicID == o.PublicID && s.SessionID == o.SessionID && s.FlowID == o.FlowID &&
		s.RunID == o.RunID && s.Agent == o.Agent && maps.Equal(s.Tags, o.Tags)
}

// String is the scope's one string form: the public id first and bare,
// then session=, flow=, run=, agent= for each field set and
// tag.<k>=<v> for each tag in key order (an empty key or value is
// left out), joined by ";", every key and value percent-encoded. The
// zero Scope is "".
func (s Scope) String() string {
	var b strings.Builder
	b.WriteString(encode(s.PublicID))
	for _, f := range [...]struct{ k, v string }{{"session", s.SessionID}, {"flow", s.FlowID}, {"run", s.RunID}, {"agent", s.Agent}} {
		if f.v != "" {
			b.WriteString(";" + f.k + "=" + encode(f.v))
		}
	}
	for _, k := range slices.Sorted(maps.Keys(s.Tags)) {
		if v := s.Tags[k]; k != "" && v != "" {
			b.WriteString(";" + tagPrefix + encode(k) + "=" + encode(v))
		}
	}
	return b.String()
}

// tagPrefix starts a tag segment's key.
const tagPrefix = "tag."

// Parse reads a scope's string form back. The first ";"-segment is the
// public id — unless it is a known key=value (session, flow, run,
// agent or tag.<k>, a raw "=": String encodes a public id's), which is
// read as a later segment is and leaves the public id "". Each later
// segment is key=value. A key it does not know, a segment without "=",
// an empty value (or tag key) and a repeat of a key already read are
// ignored; a value that does not percent-decode is kept raw. Parse
// never fails.
func Parse(str string) Scope {
	segs := strings.Split(str, ";")
	out := Scope{}
	if k, _, ok := strings.Cut(segs[0], "="); ok && knownKey(trim(k)) {
		segs = append([]string{""}, segs...)
	}
	out.PublicID = decode(trim(segs[0]))
	for _, seg := range segs[1:] {
		k, v, ok := strings.Cut(seg, "=")
		if !ok {
			continue
		}
		k, v = trim(k), decode(trim(v))
		if name, ok := strings.CutPrefix(k, tagPrefix); ok {
			if name = decode(name); name != "" && v != "" {
				if _, dup := out.Tags[name]; !dup {
					if out.Tags == nil {
						out.Tags = map[string]string{}
					}
					out.Tags[name] = v
				}
			}
			continue
		}
		var dst *string
		switch k {
		case "session":
			dst = &out.SessionID
		case "flow":
			dst = &out.FlowID
		case "run":
			dst = &out.RunID
		case "agent":
			dst = &out.Agent
		default:
			continue
		}
		if v != "" && *dst == "" {
			*dst = v
		}
	}
	return out
}

// knownKey reports whether k names a field of the string form.
func knownKey(k string) bool {
	switch k {
	case "session", "flow", "run", "agent":
		return true
	}
	return strings.HasPrefix(k, tagPrefix) && len(k) > len(tagPrefix)
}

// WithFlow returns a copy of ctx whose runs carry flow as their
// weft.flow metadata (FlowKey; core.WithMetadata, so a run's own
// core.Metadata option wins). An empty flow returns ctx. It is what
// Header does for a scope with a flow, for code that starts runs
// without that middleware (a queue consumer, a test).
func WithFlow(ctx context.Context, flow string) context.Context {
	if flow == "" {
		return ctx
	}
	return core.WithMetadata(ctx, map[string]string{FlowKey: flow})
}

// Header sets the Weft-Scope response header to scopeOf(r) on every
// response next writes, unless that scope is zero, and exposes the
// header to cross-origin pages (Access-Control-Expose-Headers gains
// Weft-Scope; the app's CORS policy must still allow the page's
// origin). The header is set before next runs, so a streaming
// handler's first flush carries it; next may replace it with Set. A
// CORS layer inside Header that sets its own expose list replaces the
// entry, so the header is sent but a cross-origin page cannot read it:
// put scope.Header inside your CORS middleware, or list Weft-Scope in
// its exposed headers. The devtools panel reads a cross-origin
// response's header only when the page and the response are both on
// loopback (a dev server on :5173 calling the app on :8080); a
// cross-origin production app names its scope with data-scope or the
// DOM marker instead.
//
// Header also puts the request's page URL (PageURL) on the request's
// context, for PageURLFromContext and — through core.WithMetadata — as
// weft.page_url on every run next starts under that context; and the
// scope's flow — scopeOf's, else the request's own Weft-Scope header's
// (a client that forwards the page's scope) — as weft.flow (WithFlow).
// A run's own core.Metadata option wins over both.
func Header(next http.Handler, scopeOf func(*http.Request) Scope) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := scopeOf(r)
		if !s.IsZero() {
			Set(w, s)
		}
		flow := s.FlowID
		if flow == "" {
			flow = Parse(r.Header.Get(HeaderName)).FlowID
		}
		if flow != "" {
			r = r.WithContext(WithFlow(r.Context(), flow))
		}
		if page := PageURL(r); page != "" {
			ctx := context.WithValue(r.Context(), pageURLKey{}, page)
			ctx = core.WithMetadata(ctx, map[string]string{PageURLKey: page})
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

// pageURLKey is the context key Header puts the page URL under.
type pageURLKey struct{}

// PageURLFromContext returns the page URL Header put on a request's
// context, and whether there was one.
func PageURLFromContext(ctx context.Context) (string, bool) {
	page, ok := ctx.Value(pageURLKey{}).(string)
	return page, ok && page != ""
}

// PageURL is the page a request came from, sanitised: its Referer, else
// its Origin plus "/". Only an absolute http or https URL with a host
// counts; userinfo, the query and the fragment are dropped (a page, not
// a state: the panel re-adds #weft_scope=), and so is a URL over 1024
// bytes, whole. "" when the request names no such page — no Referer or
// Origin, an opaque Origin ("null"), another scheme. The browser's
// default Referrer-Policy sends a cross-origin request only the
// origin, so a page on another origin is often its origin's root.
func PageURL(r *http.Request) string {
	if page := cleanPageURL(r.Header.Get("Referer"), false); page != "" {
		return page
	}
	return cleanPageURL(r.Header.Get("Origin"), true)
}

// cleanPageURL applies PageURL's rule to one header's value; origin
// asks for a bare scheme://host[:port], which gains the root path.
func cleanPageURL(raw string, origin bool) string {
	if raw == "" || len(raw) > 4*maxPageURL {
		return ""
	}
	for i := 0; i < len(raw); i++ {
		if c := raw[i]; c < 0x21 || c == 0x7F {
			return "" // a header value with spaces or controls is no URL
		}
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Opaque != "" {
		return ""
	}
	if origin {
		if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return ""
		}
		u.Path = "/"
	}
	u.User = nil
	u.RawQuery, u.ForceQuery = "", false
	u.Fragment, u.RawFragment = "", ""
	if u.Path == "" {
		u.Path = "/"
	}
	page := u.String()
	if len(page) > maxPageURL {
		return ""
	}
	return page
}

// Set sets the Weft-Scope header on w to s, replacing any earlier
// value, and exposes it to cross-origin pages (Weft-Scope is appended
// to Access-Control-Expose-Headers unless already listed; a "*" there
// does not cover it for a credentialed request). A zero s removes the
// Weft-Scope header but leaves the expose entry an earlier Set added.
// Like any header it must be set before the handler's first Write or
// WriteHeader — the dynamic case: a handler that knows the run id once
// its turn starts; a Set after that changes nothing sent.
func Set(w http.ResponseWriter, s Scope) {
	h := w.Header()
	if s.IsZero() {
		h.Del(HeaderName)
		return
	}
	h.Set(HeaderName, s.String())
	for _, v := range h.Values("Access-Control-Expose-Headers") {
		for _, name := range strings.Split(v, ",") {
			if n := strings.TrimSpace(name); strings.EqualFold(n, HeaderName) {
				return
			}
		}
	}
	h.Add("Access-Control-Expose-Headers", HeaderName)
}

// encode is encodeURIComponent: every byte outside A–Z a–z 0–9 and
// -_.!~*'() becomes %XX. A string that is not valid UTF-8 — what
// encodeURIComponent refuses — has only the form's own delimiters
// (%, ;, =) and the control bytes (below 0x20, and 0x7F) encoded, as
// the web side's fallback does: a raw CR or LF never reaches a header,
// where net/http would rewrite it and an HTTP/2 server drop the field.
func encode(v string) string {
	valid := utf8.ValidString(v)
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		c := v[i]
		keep := !valid && c != '%' && c != ';' && c != '=' && c >= 0x20 && c != 0x7F ||
			valid && ('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0)
		if keep {
			b.WriteByte(c)
			continue
		}
		const hex = "0123456789ABCDEF"
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}

// decode is decodeURIComponent, keeping v raw where that throws: a
// "%" not followed by two hex digits, or a run of escapes that does
// not spell valid UTF-8 (the raw characters between escapes are kept
// as they are, valid or not). "+" stays "+".
func decode(v string) string {
	if !strings.Contains(v, "%") {
		return v
	}
	out := make([]byte, 0, len(v))
	run := -1 // where the current run of escapes starts in out
	for i := 0; i < len(v); i++ {
		if v[i] != '%' {
			if run >= 0 && !utf8.Valid(out[run:]) {
				return v
			}
			run = -1
			out = append(out, v[i])
			continue
		}
		if i+2 >= len(v) {
			return v
		}
		hi, ok1 := unhex(v[i+1])
		lo, ok2 := unhex(v[i+2])
		if !ok1 || !ok2 {
			return v
		}
		if run < 0 {
			run = len(out)
		}
		out = append(out, hi<<4|lo)
		i += 2
	}
	if run >= 0 && !utf8.Valid(out[run:]) {
		return v
	}
	return string(out)
}

func unhex(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// trim is String.prototype.trim: JavaScript's white space and line
// terminators, which differ from unicode.IsSpace at U+0085 and U+FEFF.
func trim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
			return true
		}
		return 0x2000 <= r && r <= 0x200A
	})
}
