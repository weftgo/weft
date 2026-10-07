package studio

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// Auth and CORS (S4.6). Three setups, one handler:
//
//   - A embedded: no Token configured — the API is open to the
//     page's own origin (same origin, in-process), ingest answers
//     loopback. "Same origin" is the browser's word, and DNS
//     rebinding makes any site same-origin to a loopback Studio
//     (http://evil.example:7331 resolving to 127.0.0.1), so the open
//     API answers only a loopback Host or one AllowOrigins names
//     (hostAllowed below).
//   - B local binary: a dev token (printed at start;
//     WEFT_STUDIO_TOKEN fixes it) protects the API; AllowOrigins
//     defaults to localhost and 127.0.0.1 on any port.
//   - C hosted: the same Token is the signing key for panel tokens —
//     HMAC-signed {public_id, scope, exp} scoped to one public id,
//     minted by your backend through POST /api/panel-tokens, read-only
//     unless "playground": true. Every data route refuses anything
//     outside the token's public id, so the token is safe to put in a
//     page.

// identityCtxKey carries the request's identity (server token, panel
// token, or none) from the middleware to the handlers.
type identityCtxKey struct{}

// identity is who is asking.
type identity struct {
	server bool         // the server token (Token's value)
	panel  *panelClaims // a panel token, scoped to one public id
}

// idFrom returns the request's identity; the zero identity when no
// token is configured (setup A: open).
func idFrom(r *http.Request) identity {
	id, _ := r.Context().Value(identityCtxKey{}).(identity)
	return id
}

// panelClaims is a panel token's signed payload (S4.6, Q3/Q8 closed).
type panelClaims struct {
	PublicID string    `json:"public_id"`
	Scope    string    `json:"scope"` // "read" | "playground"
	Exp      time.Time `json:"exp"`
}

const (
	panelTokenPrefix = "weft_pt."
	scopeRead        = "read"
	scopePlayground  = "playground"
)

var errBadToken = errors.New("studio: bad panel token")

// signPanelToken signs claims with key (Token's value in setup C):
// weft_pt.<payload>.<hmac>, both base64url, HMAC-SHA256 over the
// payload bytes.
func signPanelToken(key []byte, claims panelClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	return panelTokenPrefix +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// parsePanelToken verifies a panel token's signature (constant time)
// and expiry.
func parsePanelToken(key []byte, tok string) (panelClaims, error) {
	var claims panelClaims
	rest, ok := strings.CutPrefix(tok, panelTokenPrefix)
	if !ok {
		return claims, errBadToken
	}
	dot := strings.LastIndex(rest, ".")
	if dot <= 0 {
		return claims, errBadToken
	}
	// Strict: unused trailing bits must be zero, so one token has exactly
	// one spelling (the default decoder accepts several per signature).
	payload, err := base64.RawURLEncoding.Strict().DecodeString(rest[:dot])
	if err != nil {
		return claims, errBadToken
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(rest[dot+1:])
	if err != nil {
		return claims, errBadToken
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	if subtle.ConstantTimeCompare(sig, mac.Sum(nil)) != 1 {
		return claims, errBadToken
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return claims, errBadToken
	}
	if claims.Exp.Before(time.Now()) {
		return claims, errBadToken
	}
	if claims.PublicID == "" {
		return claims, errBadToken
	}
	return claims, nil
}

// bearerToken reads the request's token: the Authorization header, or
// the token query parameter — EventSource cannot send headers, and
// the live stream is the panel's main artery.
func bearerToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if tok, ok := strings.CutPrefix(h, "Bearer "); ok {
			return tok
		}
		return ""
	}
	return r.URL.Query().Get("token")
}

// auth wraps the API tree with S4.6's token check. Without a
// configured Token (setup A) every request whose Host passes
// hostAllowed goes through — reads and writes alike, the live stream
// and the runtime link included: transcripts are as sensitive as the
// playground's verbs are dangerous — and any other Host is a 403 that
// names what to configure. With a Token the bearer must be the server
// token or a valid panel token, and the Host does not matter. Ingest
// carries its own rule (S4.4) and is not wrapped; neither are the UI
// shell and panel.js, which carry no data.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identify := func(id identity) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityCtxKey{}, id)))
		}
		if s.token == "" {
			if !hostAllowed(s.origins, r) {
				writeError(w, r, http.StatusForbidden, "forbidden",
					"no studio.Token is configured, so the API answers only a loopback Host "+
						"(localhost, *.localhost, 127.0.0.1, [::1]); this request's Host is "+
						strconv.Quote(r.Host)+": list its origin in studio.AllowOrigins "+
						"(e.g. \"http://"+r.Host+"\") or configure studio.Token")
				return
			}
			identify(identity{})
			return
		}
		tok := bearerToken(r)
		if tok == "" {
			writeError(w, r, http.StatusUnauthorized, "unauthorized",
				"the API requires a token: Authorization: Bearer <token>")
			return
		}
		if tokenEqual(tok, s.token) {
			identify(identity{server: true})
			return
		}
		claims, err := parsePanelToken([]byte(s.token), tok)
		if err != nil {
			writeError(w, r, http.StatusUnauthorized, "unauthorized", "bad or expired token")
			return
		}
		identify(identity{panel: &claims})
	})
}

// tokenEqual compares a presented token with the configured one in
// constant time (the length aside): the comparison must not tell a
// remote caller how much of its guess was right.
func tokenEqual(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// ── Panel-token scoping (S4.6) ─────────────────────────────────────
//
// The API refuses any route that reaches data outside the token's
// public id — the session's turns and the ephemeral experiments that
// carry the public id without a session id alike, which is why the
// scope is the public id and never the session id.

// forbidden is the scoping refusal: it names the boundary without
// confirming what lies beyond it.
func forbidden(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusForbidden, "forbidden",
		"this token is scoped to public id "+idFrom(r).panel.PublicID)
}

// scopeRunsQuery forces a runs query onto the token's public id and
// refuses one that asked for another. Sessions scope the same way.
func scopeRunsQuery(w http.ResponseWriter, r *http.Request, query *obsdb.RunQuery, publicParam string) bool {
	id := idFrom(r)
	if id.panel == nil {
		return true
	}
	if publicParam != "" && publicParam != id.panel.PublicID {
		forbidden(w, r)
		return false
	}
	query.PublicID = id.panel.PublicID
	return true
}

// mayAct refuses a read-scoped panel token on a write verb (starting a
// run, deciding a parked call, steering): S4.6 — a panel token is
// read-only unless it was minted with "playground": true. The server
// token and setup A's open API pass.
func mayAct(w http.ResponseWriter, r *http.Request) bool {
	if p := idFrom(r).panel; p != nil && p.Scope != scopePlayground {
		writeError(w, r, http.StatusForbidden, "forbidden",
			"this token is read-only: mint it with \"playground\": true to act")
		return false
	}
	return true
}

// scopeRunID checks a run id lands inside the token's public id. An
// unknown id passes here so the read that follows answers 404 —
// scoping must not reveal whether another public id's run exists. Any
// other read failure is the request's 500: a scope that could not be
// checked is not a scope that passed.
func (s *Server) scopeRunID(w http.ResponseWriter, r *http.Request, runID string) bool {
	id := idFrom(r)
	if id.panel == nil {
		return true
	}
	det, err := s.db.Run(r.Context(), runID)
	if err != nil {
		if errors.Is(err, obsdb.ErrNotFound) {
			return true
		}
		dbError(w, r, "run", runID, err)
		return false
	}
	if det.PublicID != id.panel.PublicID {
		forbidden(w, r)
		return false
	}
	return true
}

// scopeSessionID checks a session's public id, resolving it through
// the database.
func (s *Server) scopeSessionID(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	id := idFrom(r)
	if id.panel == nil {
		return true
	}
	det, err := s.db.Session(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, obsdb.ErrNotFound) {
			return true
		}
		dbError(w, r, "session", sessionID, err)
		return false
	}
	if det.PublicID != id.panel.PublicID {
		forbidden(w, r)
		return false
	}
	return true
}

// scopeSpans checks a whole trace: a trace that mixes public ids (or
// holds none) is outside the token.
func scopeSpans(w http.ResponseWriter, r *http.Request, spans []spanDTO) bool {
	id := idFrom(r)
	if id.panel == nil {
		return true
	}
	for _, sp := range spans {
		if pub, _ := sp.Attrs["weft.public_id"].(string); pub != id.panel.PublicID {
			forbidden(w, r)
			return false
		}
	}
	return true
}

// scopeLive checks the live stream's selector: a public_id selector
// must be the token's; run and session selectors resolve through the
// database first; an agent selector cannot be scoped, so a panel
// token gets a 403 rather than a leak. A run or session nothing has
// stored yet passes — there is no row to read a public id from — which
// is why serveLive also checks every frame against the token's public
// id before forwarding it.
func (s *Server) scopeLive(w http.ResponseWriter, r *http.Request, sel obsdb.Selector) bool {
	id := idFrom(r)
	if id.panel == nil {
		return true
	}
	switch {
	case sel.PublicID != "":
		if sel.PublicID != id.panel.PublicID {
			forbidden(w, r)
			return false
		}
		return true
	case sel.RunID != "":
		return s.scopeRunID(w, r, sel.RunID)
	case sel.SessionID != "":
		return s.scopeSessionID(w, r, sel.SessionID)
	default: // agent: not a public-id-shaped scope
		forbidden(w, r)
		return false
	}
}

// serverOnly refuses panel tokens. It wraps the routes that speak
// server-to-server — the runtime link's register/commands/acks, the
// debugger's breakpoints — where S4.6's per-public-id scoping cannot
// apply because the effect is not public-id-shaped (a runtime's whole
// feed, every future run's parking). The server token passes, and so
// does setup A's open API (no Token configured).
func (s *Server) serverOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if idFrom(r).panel != nil {
			writeError(w, r, http.StatusForbidden, "forbidden",
				"this route speaks server-to-server: it needs the server token, not a panel token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// servePanelTokens mints a panel token (S4.6 setup C): your backend
// calls POST /api/panel-tokens {"public_id":"pub_…","ttl":"1h"}
// with its server token and hands the signed token to the page —
// read-only unless "playground": true.
func (s *Server) servePanelTokens(w http.ResponseWriter, r *http.Request) {
	if !idFrom(r).server {
		writeError(w, r, http.StatusUnauthorized, "unauthorized",
			"minting a panel token requires the server token")
		return
	}
	var req struct {
		PublicID   string `json:"public_id"`
		TTL        string `json:"ttl"`
		Playground bool   `json:"playground"`
	}
	if !decodeBody(w, r, "body must be JSON", &req) {
		return
	}
	if req.PublicID == "" {
		badRequest(w, r, "public_id is required")
		return
	}
	ttl := time.Hour
	if req.TTL != "" {
		d, err := time.ParseDuration(req.TTL)
		if err != nil || d <= 0 {
			badRequest(w, r, "ttl must be a positive duration (e.g. \"1h\")")
			return
		}
		ttl = d
	}
	scope := scopeRead
	if req.Playground {
		scope = scopePlayground
	}
	claims := panelClaims{PublicID: req.PublicID, Scope: scope, Exp: time.Now().Add(ttl).UTC()}
	tok, err := signPanelToken([]byte(s.token), claims)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, r, http.StatusOK, struct {
		Token    string    `json:"token"`
		PublicID string    `json:"public_id"`
		Scope    string    `json:"scope"`
		Exp      time.Time `json:"exp"`
	}{tok, claims.PublicID, claims.Scope, claims.Exp})
}

// ── CORS (S4.6) ────────────────────────────────────────────────────

// cors wraps the API and ingest trees: a panel served from another
// origin (setup B's cross-origin dev token, setup C's hosted token)
// needs the headers, and an EventSource needs them on the stream.
// Without a configured Token (setup A, same origin) no CORS headers
// are sent; explicit AllowOrigins always apply.
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if s.token != "" || len(s.origins) > 0 {
			// The answer depends on the Origin whenever CORS is in play
			// — the refusals too: a shared cache must not hand an answer
			// made for a refused origin to an allowed one.
			w.Header().Add("Vary", "Origin")
		}
		if origin != "" && originAllowed(s.origins, s.token != "", origin) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			// PUT carries the breakpoints control (the panel's
			// panelPut); GET/POST/OPTIONS cover the rest.
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
			h.Set("Access-Control-Allow-Headers",
				"Authorization, Content-Type, Last-Event-ID")
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// originAllowed matches an origin against the configured list; with
// none configured and a token in play, the default allows localhost
// and 127.0.0.1 on any port, http or https (S4.6 setup B).
func originAllowed(origins []string, defaults bool, origin string) bool {
	if len(origins) > 0 {
		for _, o := range origins {
			if o == "*" || o == origin {
				return true
			}
		}
		return false
	}
	if !defaults {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1"
}

// hostAllowed is setup A's DNS-rebinding guard: the request's Host
// (the browser's idea of the origin's host, which a rebound page
// cannot fake) must be a loopback name — localhost, *.localhost
// (browsers resolve those to loopback themselves), 127.0.0.0/8 or
// [::1], any port — or the host[:port] of an AllowOrigins entry
// ("*" lets every Host through, as it lets every origin). Proxy
// headers (X-Forwarded-Host, Forwarded) are never read: anyone can
// send them. No request is exempt: weft/runtime's in-process
// runtime.Local transport addresses the handler as localhost.
func hostAllowed(origins []string, r *http.Request) bool {
	if isLoopbackName(hostOnly(r.Host)) {
		return true
	}
	for _, o := range origins {
		if o == "*" {
			return true
		}
		if u, err := url.Parse(o); err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host) {
			return true
		}
	}
	return false
}

// hostOnly strips the port (and an IPv6 literal's brackets) from a
// host[:port].
func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return strings.Trim(hostport, "[]")
}

// isLoopbackName reports whether host names this machine: localhost,
// a *.localhost name, or a loopback IP (127.0.0.0/8, ::1, the
// IPv4-mapped form).
func isLoopbackName(host string) bool {
	host = strings.ToLower(host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// DevToken generates a random dev token for setup B's binary: printed
// at start, fixed by WEFT_STUDIO_TOKEN. Exported because the binary
// lives in another package (studio/cmd).
func DevToken() string {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		// Crypto randomness refusing is not survivable; a fixed token
		// would be worse than loud.
		panic("studio: dev token: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
