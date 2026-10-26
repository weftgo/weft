package studio

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The setup C hand-off (plan G3): "open in Studio" from a page that
// holds a playground-scoped panel token lands on the hosted Studio
// already authenticated to that one conversation, without anyone
// pasting a token. GET /api/panel-handoff?to=/runs/… mints a read-scoped
// panel token for the bearer's public id — the same HMAC claims and
// signing as POST /api/panel-tokens, five minutes — and answers 303 to
// <base><to>#token=<minted>: the fragment never reaches a server, the
// UI reads it (as it reads `weft open`'s).
//
// A read-scoped panel token cannot hand off — 403, even read-for-read:
// a hand-off mints, and a read token mints nothing (a scope never
// widens, and a fresh read token would only renew its holder's
// expiry); its page is already a read view of its conversation, so
// the UI links to Studio directly instead. The server token (setup B's
// dev token, which the panel holds as data-token) hands off too (U22,
// UX-D-18): its answer carries that same token in the fragment — what
// `weft studio` and `weft open --with-token` already open the browser
// with — so a tab in a browser that never stored it lands signed in,
// as setup C's does. No read token is minted for it: a read token is
// one public id's, and the dev token's page may be a batch of many.
//
// to is a path under Studio's base and nothing else — an open redirect
// is the failure this guards against: it must start with "/" (not
// "//" or "/\"), carry no scheme, host, fragment, backslash, control
// byte or white space, and no "." or ".." segment, raw or
// percent-encoded; anything else is 400.

// handoffTTL is a minted hand-off token's life (plan G3): long enough
// to open the page, short enough that a leaked URL is soon worthless.
const handoffTTL = 5 * time.Minute

// maxHandoffTo bounds the to parameter.
const maxHandoffTo = 2048

// handoffGroup registers GET /api/panel-handoff. New adds it exactly
// when a Token is configured: panel tokens exist only then.
func handoffGroup() routeGroup {
	return routeGroup{
		name:       "handoff",
		capability: "handoff",
		register: func(mux *http.ServeMux, s *Server) {
			mux.HandleFunc("GET /api/panel-handoff", s.servePanelHandoff)
		},
	}
}

// servePanelHandoff answers the hand-off. The identity is checked
// before to is read (a read-scoped token's bad to is 403, as a bad body
// is elsewhere). A request that accepts application/json — the panel's
// fetch, which cannot read a cross-origin redirect's Location — gets
// the same answer as 200 {"location", "exp"} instead of the 303.
func (s *Server) servePanelHandoff(w http.ResponseWriter, r *http.Request) {
	// Every answer, refusals included: a Location carries a token, and
	// no answer of this route may be cached or leak its URL onward.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	id := idFrom(r)
	if id.panel != nil && id.panel.Scope != scopePlayground {
		writeError(w, r, http.StatusForbidden, "forbidden",
			"a read-scoped token cannot widen: a hand-off mints a token, and only a playground-scoped "+
				"panel token may; this token's page links to Studio directly")
		return
	}
	to := r.URL.Query().Get("to")
	if !handoffPathOK(to) {
		badRequest(w, r, "to must be a path under Studio's base: it starts with a single \"/\" and carries "+
			"no scheme, host, fragment, backslash, white space or \".\"/\"..\" segment")
		return
	}
	location := s.base + strings.TrimPrefix(to, "/")
	var exp time.Time
	if id.panel != nil {
		// Never past the token that asked: the hand-off narrows the
		// scope and must not lengthen the access.
		exp = s.now().Add(handoffTTL).UTC()
		if id.panel.Exp.Before(exp) {
			exp = id.panel.Exp.UTC()
		}
		claims := panelClaims{PublicID: id.panel.PublicID, Scope: scopeRead, Exp: exp}
		tok, err := signPanelToken([]byte(s.token), claims)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		location += "#token=" + tok
		slog.Info("studio: panel hand-off", "public_id", claims.PublicID, "to", to, "exp", exp)
	} else if id.server {
		// The fragment never reaches a server; the answer is no-store
		// and no-referrer (above). The UI reads #token= as it reads
		// weft open's (url.QueryEscape: the CLI's runLink encoding).
		location += "#token=" + url.QueryEscape(s.token)
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		var expOut *time.Time
		if !exp.IsZero() {
			expOut = &exp
		}
		writeJSON(w, r, http.StatusOK, struct {
			Location string     `json:"location"`
			Exp      *time.Time `json:"exp,omitempty"`
		}{location, expOut})
		return
	}
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusSeeOther)
}

// handoffPathOK is the open-redirect guard: to names a path under
// Studio's base, a query allowed, and nothing a browser could resolve
// to another origin or above the base.
func handoffPathOK(to string) bool {
	if to == "" || len(to) > maxHandoffTo || to[0] != '/' || strings.HasPrefix(to, "//") {
		return false
	}
	for i := 0; i < len(to); i++ {
		// Browsers read "\" as "/" ("/\evil" is "//evil") and strip tabs
		// and newlines from a URL; neither has a place in a path here.
		if c := to[i]; c <= ' ' || c == 0x7F || c == '\\' || c == '#' {
			return false
		}
	}
	u, err := url.Parse(to)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" {
		return false
	}
	// "." and ".." segments, raw or percent-encoded (%2e): the URL
	// standard resolves both spellings.
	for _, seg := range strings.Split(u.EscapedPath(), "/") {
		dec, err := url.PathUnescape(seg)
		if err != nil || dec == "." || dec == ".." || strings.ContainsAny(dec, "/\\") && strings.Contains(dec, "..") {
			return false
		}
	}
	return true
}
