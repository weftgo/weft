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
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/studio/internal/reqlog"
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
//     HMAC-signed {public_id, tags, scope, exp} scoped to one public id,
//     to the runs carrying a set of metadata tags (I4: a batch), or
//     both, minted by your backend through POST /api/panel-tokens,
//     read-only unless "playground": true (a public id's alone). Every
//     data route refuses anything outside the token's scope, so the
//     token is safe to put in a page.
//
// In every setup a token rides the Authorization header only: a
// ?token= query parameter is refused on every /api route, in every
// setup (plan C5), and the
// live stream — which EventSource opens without headers — takes a
// 60-second grant for one exact stream from POST /api/live-grant
// instead (livegrant.go). A #token= fragment, which `weft open` and
// `weft studio --open` hand the browser, never reaches the server: the
// UI reads it.

// identityCtxKey carries the request's identity (server token, panel
// token, or none) from the middleware to the handlers.
type identityCtxKey struct{}

// identity is who is asking.
type identity struct {
	server bool         // the server token (Token's value)
	panel  *panelClaims // a panel token, scoped to a public id, tags or both
}

// idFrom returns the request's identity; the zero identity when no
// token is configured (setup A: open).
func idFrom(r *http.Request) identity {
	id, _ := r.Context().Value(identityCtxKey{}).(identity)
	return id
}

// panelClaims is a panel token's signed payload (S4.6, Q3/Q8 closed):
// its public id, its tags (I4: run metadata pairs, every one of which
// a run it reads carries), or both — at least one.
type panelClaims struct {
	PublicID string            `json:"public_id,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
	Scope    string            `json:"scope"` // "read" | "playground"
	Exp      time.Time         `json:"exp"`
}

// readScope is the runs a panel token reads (S4.6, I4): its public
// id's, those whose metadata carries every tag pair, or both. The zero
// value — the server token's, setup A's — reads every run.
type readScope struct {
	PublicID string
	Tags     map[string]string
}

// scopeOfClaims is a panel token's read scope.
func scopeOfClaims(c *panelClaims) readScope {
	if c == nil {
		return readScope{}
	}
	return readScope{PublicID: c.PublicID, Tags: c.Tags}
}

// none reports whether the scope reads every run (no panel token).
func (sc readScope) none() bool { return sc.PublicID == "" && len(sc.Tags) == 0 }

// admits reports whether a run lies in the scope: its row carries the
// public id (when the scope names one) and every tag pair.
func (sc readScope) admits(rr obsdb.RunRow) bool {
	return (sc.PublicID == "" || rr.PublicID == sc.PublicID) && metaHas(rr.Meta, sc.Tags)
}

// admitsAttrs reports whether a span's or record's attributes lie in
// the scope: weft.public_id and every tag pair (run metadata rides
// every weft span and record verbatim).
func (sc readScope) admitsAttrs(attrs map[string]any) bool {
	if sc.PublicID != "" {
		if pub, _ := attrs["weft.public_id"].(string); pub != sc.PublicID {
			return false
		}
	}
	for k, v := range sc.Tags {
		if got, ok := attrs[k].(string); !ok || got != v {
			return false
		}
	}
	return true
}

// describe names the scope for a refusal: "public id pub_1", "tags
// arena.batch=b1", or both.
func (sc readScope) describe() string {
	var parts []string
	if sc.PublicID != "" {
		parts = append(parts, "public id "+sc.PublicID)
	}
	if len(sc.Tags) > 0 {
		tags := make([]string, 0, len(sc.Tags))
		for _, k := range slices.Sorted(maps.Keys(sc.Tags)) {
			tags = append(tags, k+"="+sc.Tags[k])
		}
		parts = append(parts, "tags "+strings.Join(tags, ", "))
	}
	return strings.Join(parts, " and ")
}

// narrowTags is a request's metadata filter under a token's tags: the
// filter with every tag pair added (a fresh map; meta is not changed).
// ok is false when the request names a token key with another value —
// a widening, refused (the 403 another public id gets) — never
// silently replaced.
func narrowTags(meta, tags map[string]string) (map[string]string, bool) {
	if len(tags) == 0 {
		return meta, true
	}
	out := make(map[string]string, len(meta)+len(tags))
	maps.Copy(out, meta)
	for k, v := range tags {
		if got, ok := out[k]; ok && got != v {
			return nil, false
		}
		out[k] = v
	}
	return out, true
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
	if claims.PublicID == "" && len(claims.Tags) == 0 {
		return claims, errBadToken
	}
	if len(claims.Tags) > 0 && claims.Scope == scopePlayground {
		return claims, errBadToken // a tag token is read-only (the mint's rule)
	}
	return claims, nil
}

// bearerToken reads the request's token from the Authorization header
// — never from the URL (plan C5): a URL lands in logs, history and
// Referer headers. The live stream, which EventSource opens without
// headers, takes a short-lived grant instead (livegrant.go).
func bearerToken(r *http.Request) string {
	if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return tok
	}
	return ""
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
		if r.URL.Query().Has("token") {
			// Never accepted on the /api tree, in any setup, whatever it
			// holds — and never echoed.
			writeError(w, r, http.StatusUnauthorized, "unauthorized",
				"a token in the URL is never accepted: send Authorization: Bearer <token> "+
					"(the live stream: POST /api/live-grant, then /api/live?…&sig=)")
			return
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
			if crossOriginWrite(s.origins, r) {
				writeError(w, r, http.StatusForbidden, "forbidden",
					"cross-origin write refused (setup A): with no studio.Token the API takes an unsafe "+
						"method only from its own origin; this request's Origin is "+strconv.Quote(r.Header.Get("Origin"))+
						" (Sec-Fetch-Site "+strconv.Quote(r.Header.Get("Sec-Fetch-Site"))+"): list the origin in "+
						"studio.AllowOrigins or configure studio.Token")
				return
			}
			if sig, ok := liveGrantRequest(r); ok {
				s.identifyGrant(w, r, sig, identify)
				return
			}
			identify(identity{})
			return
		}
		if sig, ok := liveGrantRequest(r); ok {
			s.identifyGrant(w, r, sig, identify)
			return
		}
		tok := bearerToken(r)
		if tok == "" {
			msg := "the API requires a token: Authorization: Bearer <token>"
			if r.URL.Path == "/api/live" {
				msg += " — or, where headers cannot be sent (EventSource), a sig from POST /api/live-grant: /api/live?…&sig="
			}
			writeError(w, r, http.StatusUnauthorized, "unauthorized", msg)
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

// identifyGrant admits a live stream opened with a grant as the
// identity the grant was issued to, or refuses it (401): a presented
// sig must be valid, whatever else the request carries.
func (s *Server) identifyGrant(w http.ResponseWriter, r *http.Request, sig string, identify func(identity)) {
	id, err := s.verifyLiveGrant(r, sig)
	if err != nil {
		badGrant(w, r)
		return
	}
	identify(id)
}

// tokenEqual compares a presented token with the configured one in
// constant time (the length aside): the comparison must not tell a
// remote caller how much of its guess was right.
func tokenEqual(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// ── Panel-token scoping (S4.6, I4) ─────────────────────────────────
//
// The API refuses any route that reaches data outside the token's
// scope — its public id (the session's turns and the ephemeral
// experiments that carry the public id without a session id alike,
// which is why the scope is the public id and never the session id),
// its tags (I4: the runs whose metadata carries every pair — a batch),
// or both. A tag scope is only as tight as the app's own tagging:
// metadata is caller-written, which is why minting stays the server
// token's.

// forbidden is the scoping refusal: it names the boundary without
// confirming what lies beyond it.
func forbidden(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusForbidden, "forbidden",
		"this token is scoped to "+scopeOfClaims(idFrom(r).panel).describe())
}

// scopeOf is the panel token's read scope the request runs under, the
// zero scope for any other identity: panelScope for code that holds the
// request's context only (the coster, which every route prices with).
func scopeOf(ctx context.Context) readScope {
	id, _ := ctx.Value(identityCtxKey{}).(identity)
	return scopeOfClaims(id.panel)
}

// inScope is the one rule for a run a reader reaches through another
// (a run document's children and its export's, a step's children, a
// row's children counts and delegated cost, the tree export's
// subagents): under a panel token's scope sc, a run is in scope when
// its row carries the scope's public id and tags (readScope.admits) —
// withScope's rule, and obsdb's tree scope (scopeTree) for a row. The
// zero scope admits every run. A child is read by its own row, never
// its parent's: a Subagent child inherits its parent's metadata (core
// WithMetadata) and is in a tag scope; a thread/pool child session's
// turn carries only the tags its app gave it.
func inScope(sc readScope, rr obsdb.RunRow) bool {
	return sc.admits(rr)
}

// scopeKids splits a run's children by inScope: the ones sc reaches,
// and the count of the rest — which go out as one hidden hole
// (hiddenChildrenHole), never by id or public id.
func scopeKids(sc readScope, kids []obsdb.RunRow) ([]obsdb.RunRow, int) {
	if sc.none() {
		return kids, 0
	}
	out := make([]obsdb.RunRow, 0, len(kids))
	for _, k := range kids {
		if inScope(sc, k) {
			out = append(out, k)
		}
	}
	return out, len(kids) - len(out)
}

// hiddenChildrenHole is the hidden hole (ADR 0028 §11's table) of a
// run document or a step whose n child runs a panel token's scope left
// out: counted, never named — their ids and public ids are another
// app's.
func hiddenChildrenHole(n int, sc readScope) stepHole {
	return stepHole{
		Hole:   string(obsdb.HoleHidden),
		Reason: hiddenChildrenReason(n, sc),
		Fix:    "open the run with the server (dev) token",
		Count:  n,
	}
}

// hiddenChildrenReason is the hidden children hole's reason, its count
// in the right number: "1 child run carries …, so it is left out";
// under a token with tags (I4-fix), the runs without them.
func hiddenChildrenReason(n int, sc readScope) string {
	if len(sc.Tags) > 0 {
		if n == 1 {
			return "1 child run is without your token's tags (or of another public id): your token's access does not reach it, so it is left out"
		}
		return fmt.Sprintf("%d child runs are without your token's tags (or of another public id): your token's access does not reach them, so they are left out", n)
	}
	if n == 1 {
		return "1 child run carries another public id, or none: a panel token reads its own public id's runs, so it is left out"
	}
	return fmt.Sprintf("%d child runs carry another public id, or none: a panel token reads its own public id's runs, so they are left out", n)
}

// hiddenTreeReason is the span tree's hidden hole's reason
// (runs/{id}/spans?tree=1): how many runs of the tree the scope left
// out, their subtrees' runs counted, never named; under a token with
// tags (I4-fix), the runs without them.
func hiddenTreeReason(n int, sc readScope) string {
	if len(sc.Tags) > 0 {
		if n == 1 {
			return "1 run of this tree is without your token's tags (or of another public id): your token's access does not reach it, so its spans are left out"
		}
		return fmt.Sprintf("%d runs of this tree are without your token's tags, or of another public id (or sit under one that is): your token's access does not reach them, so their spans are left out", n)
	}
	if n == 1 {
		return "1 run of this tree carries another public id, or none: a panel token reads its own public id's runs, so its spans are left out"
	}
	return fmt.Sprintf("%d runs of this tree carry another public id, or none (or sit under one that does): a panel token reads its own public id's runs, so their spans are left out", n)
}

// scopeRunsQuery forces a runs query onto the token's scope — its
// public id, its tags ANDed into the query's metadata filter — and
// refuses one that asked for another public id or another value of a
// token's tag key (403: a request narrows, never widens). Under a
// tag-only token a public_id parameter narrows. Sessions scope the
// same way (scopeSessionQuery).
func scopeRunsQuery(w http.ResponseWriter, r *http.Request, query *obsdb.RunQuery, publicParam string) bool {
	pub, meta, ok := scopeQuery(w, r, query.PublicID, query.Meta, publicParam)
	query.PublicID, query.Meta = pub, meta
	return ok
}

// scopeQuery is scopeRunsQuery's rule over a query's public id and
// metadata filter: the scoped pair, or false after the 403.
func scopeQuery(w http.ResponseWriter, r *http.Request, pub string, meta map[string]string, publicParam string) (string, map[string]string, bool) {
	p := idFrom(r).panel
	if p == nil {
		return pub, meta, true
	}
	if p.PublicID != "" {
		if publicParam != "" && publicParam != p.PublicID {
			forbidden(w, r)
			return pub, meta, false
		}
		pub = p.PublicID
	}
	scoped, ok := narrowTags(meta, p.Tags)
	if !ok {
		forbidden(w, r)
		return pub, meta, false
	}
	return pub, scoped, true
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

// scopeRunID checks a run id lands inside the token's scope (its
// public id, its tags: readScope.admits) — 403 otherwise, a run without
// the tags as another public id's. An unknown id passes here so the
// read that follows answers 404 — scoping must not reveal whether
// another public id's run exists. Any
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
	if !scopeOfClaims(id.panel).admits(det.RunRow) {
		forbidden(w, r)
		return false
	}
	return true
}

// scopeSessionID checks a session lands inside the token's scope,
// resolving it through the database: the session passes when any of its
// top-level turns lies in the scope (readScope.admits) — the sessions
// list's rule (a
// WHERE before the grouping), so a session the list shows is one the
// token may open. Its readers then serve the token's turns alone (the
// session view filters its runs and re-scopes its row; the live stream
// checks every frame).
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
	if len(withScope(det.Runs, scopeOfClaims(id.panel))) == 0 {
		forbidden(w, r)
		return false
	}
	return true
}

// sessionReadError answers a session read route's failed read
// (sessions/{id} and its timeline, entries and export): for a panel
// token an unknown session is the 403 another public id's session gets
// (scopeSessionID's), so the answer never says whether a session id
// exists outside its scope; every other failure, and every other
// identity, is dbError's. scopeSessionID itself lets an unknown id pass
// — the live stream subscribes to a session nothing has stored yet.
func sessionReadError(w http.ResponseWriter, r *http.Request, sessionID string, err error) {
	if errors.Is(err, obsdb.ErrNotFound) && idFrom(r).panel != nil {
		forbidden(w, r)
		return
	}
	dbError(w, r, "session", sessionID, err)
}

// scopeSpans checks a whole trace: a trace that mixes public ids (or
// holds none), or one a span of which lacks a token's tag, is outside
// the token.
func scopeSpans(w http.ResponseWriter, r *http.Request, spans []spanDTO) bool {
	id := idFrom(r)
	if id.panel == nil {
		return true
	}
	sc := scopeOfClaims(id.panel)
	for _, sp := range spans {
		if !sc.admitsAttrs(sp.Attrs) {
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
// id before forwarding it. The all selector is no identity's but the
// server token's and setup A's loopback (mayReadDBPath): 403 for every
// panel token, whatever its scope.
func (s *Server) scopeLive(w http.ResponseWriter, r *http.Request, sel obsdb.Selector) bool {
	id := idFrom(r)
	if sel.All {
		// Every run (plan A7, the Live page): the dev token's and setup
		// A's loopback alone — api/meta's live_all, db.path's rule.
		if !mayReadDBPath(s, r) {
			writeError(w, r, http.StatusForbidden, "forbidden",
				"the all selector streams every run: it needs the server token, or setup A on a loopback Host")
			return false
		}
		return true
	}
	if id.panel == nil {
		return true
	}
	switch {
	case sel.PublicID != "":
		// A tag-only token's stream of a public id is narrowed by its
		// tags (serveLive's filter), frame by frame.
		if id.panel.PublicID != "" && sel.PublicID != id.panel.PublicID {
			forbidden(w, r)
			return false
		}
		return true
	case sel.RunID != "":
		return s.scopeRunID(w, r, sel.RunID)
	case sel.SessionID != "":
		return s.scopeSessionID(w, r, sel.SessionID)
	default: // agent: not a public-id-shaped scope
		// A tag-only token's agent= stream (data-scope
		// "tag.arena.batch=b1;agent=director", I4-fix) is every run of
		// the agent narrowed by its tags: serveLive's filter carries
		// them (liveTokenMeta), frame by frame, so agent= narrows a tag
		// scope as it narrows any other.
		if id.panel.PublicID == "" && len(id.panel.Tags) > 0 {
			return true
		}
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
// read-only unless "playground": true. "tags": {"arena.batch": "…"}
// (I4) scopes it to the runs carrying every pair — a batch, with or
// without a public id (one of the two is required); a token with tags
// is read-only. A tag scope is only as tight as the app's own tagging:
// metadata is caller-written, which is why minting stays the server
// token's.
func (s *Server) servePanelTokens(w http.ResponseWriter, r *http.Request) {
	if !idFrom(r).server {
		writeError(w, r, http.StatusUnauthorized, "unauthorized",
			"minting a panel token requires the server token")
		return
	}
	var req struct {
		PublicID   string            `json:"public_id"`
		Tags       map[string]string `json:"tags"`
		TTL        string            `json:"ttl"`
		Playground bool              `json:"playground"`
	}
	// An unknown key ({"scope":"playground"}) is a 400 naming the
	// fields, never a read token minted in silence (UX-D-27).
	if !decodeStrict(w, r, `body must be JSON with public_id, tags, ttl and playground ("playground": true mints a playground token)`, &req) {
		return
	}
	if req.PublicID == "" && len(req.Tags) == 0 {
		badRequest(w, r, "public_id or tags is required")
		return
	}
	if msg := checkMintTags(req.Tags); msg != "" {
		badRequest(w, r, msg)
		return
	}
	if len(req.Tags) > 0 && req.Playground {
		badRequest(w, r, "a token with tags is read-only: \"playground\": true takes a public id alone")
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
	tags := req.Tags
	if len(tags) == 0 {
		tags = nil
	}
	claims := panelClaims{PublicID: req.PublicID, Tags: tags, Scope: scope, Exp: time.Now().Add(ttl).UTC()}
	tok, err := signPanelToken([]byte(s.token), claims)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, r, http.StatusOK, struct {
		Token    string            `json:"token"`
		PublicID string            `json:"public_id"`
		Tags     map[string]string `json:"tags,omitempty"`
		Scope    string            `json:"scope"`
		Exp      time.Time         `json:"exp"`
	}{tok, claims.PublicID, claims.Tags, claims.Scope, claims.Exp})
}

// Bounds on a minted token's tags (I4-fix): the token rides every
// request's Authorization header, so its claims stay small.
const (
	maxMintTags     = 16
	maxMintTagBytes = 256
)

// checkMintTags is the mint's rule for tags, "" when they pass, else
// the 400's reason: every key and value non-empty and at most
// maxMintTagBytes, at most maxMintTags pairs, and no weft.* key but
// weft.flow — the others are the record contract's own attributes
// (weft.public_id, weft.session.id, …), whose scope is the public id's
// or none: a tag token never stands in for one.
func checkMintTags(tags map[string]string) string {
	if len(tags) > maxMintTags {
		return fmt.Sprintf("tags: at most %d pairs (%d given)", maxMintTags, len(tags))
	}
	for _, k := range slices.Sorted(maps.Keys(tags)) {
		v := tags[k]
		switch {
		case k == "" || v == "":
			return "tags: every key and value must be non-empty (run metadata pairs, e.g. {\"arena.batch\": \"b1\"})"
		case len(k) > maxMintTagBytes || len(v) > maxMintTagBytes:
			return fmt.Sprintf("tags: %q: a key or value is at most %d bytes", k, maxMintTagBytes)
		case strings.HasPrefix(k, "weft.") && k != flowKey:
			return fmt.Sprintf("tags: %q is a weft record attribute, not run metadata: of the weft.* keys a tag token takes weft.flow alone (scope a token to a public id with \"public_id\")", k)
		}
	}
	return ""
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
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers",
				"Authorization, Content-Type, Last-Event-ID")
			// The request id (reqlog) is readable cross-origin: a panel's
			// error report quotes it to meet its server log line.
			h.Set("Access-Control-Expose-Headers", reqlog.Header)
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

// crossOriginWrite is setup A's CSRF guard: the loopback Host proves
// the request reached this machine, not that this origin's page sent it
// — any site's page can POST text/plain with mode "no-cors" and no
// preflight. An unsafe method (anything but GET, HEAD, OPTIONS) is
// cross-origin when its Origin is neither scheme://Host nor an
// AllowOrigins entry, or, with no Origin, when Sec-Fetch-Site says
// cross-site. A client sending neither header (curl, the runtime link)
// is not a browser page and passes.
func crossOriginWrite(origins []string, r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site")
	}
	for _, o := range origins {
		if o == "*" || o == origin {
			return false
		}
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return true // "null" (a sandboxed frame, file://) included
	}
	return !strings.EqualFold(u.Host, r.Host)
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
// lives in another package (cmd/weft).
func DevToken() string {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		// Crypto randomness refusing is not survivable; a fixed token
		// would be worse than loud.
		panic("studio: dev token: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
