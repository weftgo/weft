package studio

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The live grant (plan C5): a token never travels in a URL, and
// EventSource cannot set headers, so the live stream's credential is a
// short-lived signature over one exact stream. The page asks
//
//	POST /api/live-grant {"run":"r_1","kinds":"event,run"}
//
// with its bearer (or setup A's open loopback), gets {sig, exp} back,
// and opens
//
//	GET /api/live?run=r_1&kinds=event,run&sig=<sig>
//
// The sig binds the identity that asked (the server token, a panel
// token's public id and scope, or setup A's open API), the selector,
// the kinds set and the expiry — liveGrantTTL (60 s) from the grant,
// never later than the panel token's own expiry. A leaked URL opens
// one stream for a minute; the token itself is never in it. One code
// path for every setup: without a Token the key is a per-process
// random one (no cookie, no second mechanism).

// liveGrantTTL is how long a grant stays valid (plan C5: 60 s). A
// variable only so tests can shorten it.
var liveGrantTTL = 60 * time.Second

const (
	liveGrantPrefix = "weft_lg."
	// liveGrantDomain separates the grant MAC from every other use of
	// the key: the key itself is derived (liveGrantKey), and the MAC
	// input starts with this label and version.
	liveGrantDomain = "weft-live-grant/1"

	grantServer = "server" // the server token
	grantPanel  = "panel"  // a panel token: public_id and scope
	grantOpen   = "open"   // setup A: no Token configured
)

// liveGrantClaims is a grant's signed payload: who asked and until
// when. The stream it opens (selector and kinds) is not in the payload
// — the request names it — but the MAC covers it (liveGrantMAC).
type liveGrantClaims struct {
	ID       string    `json:"id"` // grantServer | grantPanel | grantOpen
	PublicID string    `json:"public_id,omitempty"`
	Scope    string    `json:"scope,omitempty"`
	Exp      time.Time `json:"exp"`
}

var errBadGrant = errors.New("studio: bad live grant")

// liveGrantKey derives the grant key: from the configured Token (the
// panel tokens' signing key) through HMAC under a fixed label, so a
// grant MAC can never be a panel token's and vice versa; a random
// per-process key without one (setup A).
func liveGrantKey(token string) []byte {
	if token == "" {
		k := make([]byte, 32)
		if _, err := rand.Read(k); err != nil {
			panic("studio: live grant key: " + err.Error())
		}
		return k
	}
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(liveGrantDomain + " key"))
	return mac.Sum(nil)
}

// liveStream is one /api/live stream's identity, canonical: the
// selector's name and value, and the kinds set sorted and
// comma-joined — so "run,event" and "event,run" (and the omitted
// default) name the same stream, and anything else does not.
func liveStream(sel obsdb.Selector, kinds map[string]bool) []byte {
	name, value := "agent", sel.Agent
	switch {
	case sel.RunID != "":
		name, value = "run", sel.RunID
	case sel.SessionID != "":
		name, value = "session", sel.SessionID
	case sel.PublicID != "":
		name, value = "public_id", sel.PublicID
	}
	ks := make([]string, 0, len(kinds))
	for k := range kinds {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	b, _ := json.Marshal([]string{name, value, strings.Join(ks, ",")})
	return b
}

// liveGrantMAC is HMAC-SHA256(key, domain NUL payload NUL stream): the
// payload bytes as signed (JSON, so no NUL inside) and the canonical
// stream (a JSON array, likewise).
func liveGrantMAC(key, payload, stream []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(liveGrantDomain))
	mac.Write([]byte{0})
	mac.Write(payload)
	mac.Write([]byte{0})
	mac.Write(stream)
	return mac.Sum(nil)
}

// signLiveGrant signs claims for one stream:
// weft_lg.<payload>.<mac>, both base64url.
func signLiveGrant(key []byte, claims liveGrantClaims, stream []byte) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return liveGrantPrefix +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(liveGrantMAC(key, payload, stream)), nil
}

// parseLiveGrant verifies a sig against the stream the request names
// (constant time) and its expiry.
func parseLiveGrant(key []byte, sig string, stream []byte, now time.Time) (liveGrantClaims, error) {
	var claims liveGrantClaims
	rest, ok := strings.CutPrefix(sig, liveGrantPrefix)
	if !ok {
		return claims, errBadGrant
	}
	dot := strings.LastIndex(rest, ".")
	if dot <= 0 {
		return claims, errBadGrant
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(rest[:dot])
	if err != nil {
		return claims, errBadGrant
	}
	mac, err := base64.RawURLEncoding.Strict().DecodeString(rest[dot+1:])
	if err != nil {
		return claims, errBadGrant
	}
	if subtle.ConstantTimeCompare(mac, liveGrantMAC(key, payload, stream)) != 1 {
		return claims, errBadGrant
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return claims, errBadGrant
	}
	if !claims.Exp.After(now) {
		return claims, errBadGrant
	}
	switch claims.ID {
	case grantServer, grantOpen:
	case grantPanel:
		if claims.PublicID == "" || (claims.Scope != scopeRead && claims.Scope != scopePlayground) {
			return claims, errBadGrant
		}
	default:
		return claims, errBadGrant
	}
	return claims, nil
}

// grantIdentity is the identity a verified grant stands for: the one
// the bearer that asked for it had, so serveLive's selector and
// per-frame checks see exactly what they would have seen.
func (c liveGrantClaims) identity() identity {
	switch c.ID {
	case grantServer:
		return identity{server: true}
	case grantPanel:
		return identity{panel: &panelClaims{PublicID: c.PublicID, Scope: c.Scope, Exp: c.Exp}}
	}
	return identity{}
}

// liveGrantRequest reports whether r is a live stream opened with a
// sig — the one place a credential rides the query string.
func liveGrantRequest(r *http.Request) (string, bool) {
	if r.URL.Path != "/api/live" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return "", false
	}
	q := r.URL.Query()
	if !q.Has("sig") {
		return "", false
	}
	return q.Get("sig"), true
}

// verifyLiveGrant checks the request's sig against the stream its
// query names. A malformed selector or kinds set is not a stream any
// grant was issued for: the same 401.
func (s *Server) verifyLiveGrant(r *http.Request, sig string) (identity, error) {
	q := map[string][]string(r.URL.Query())
	sel, err := liveSelector(q)
	if err != nil {
		return identity{}, errBadGrant
	}
	kinds, err := liveKinds(q)
	if err != nil {
		return identity{}, errBadGrant
	}
	claims, err := parseLiveGrant(s.grantKey, sig, liveStream(sel, kinds), s.now())
	if err != nil {
		return identity{}, err
	}
	return claims.identity(), nil
}

// badGrant is the refusal of a sig: it never echoes the sig, and says
// what to do instead.
func badGrant(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusUnauthorized, "unauthorized",
		"bad or expired live grant for this stream: request a new one with POST /api/live-grant")
}

// serveLiveGrant mints a grant (plan C5): the selector and kinds as
// /api/live takes them, in a JSON body ({"run":"r_1","kinds":
// "event,run"}) or the query string — one or the other. The caller's
// identity is the bearer's (or setup A's open API), and the scope
// rules are /api/live's own, checked now: a panel token is granted
// only a stream inside its public id, never an agent selector.
func (s *Server) serveLiveGrant(w http.ResponseWriter, r *http.Request) {
	q := map[string][]string(r.URL.Query())
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeError(w, r, http.StatusRequestEntityTooLarge, "bad_request", "body: larger than the 4 MiB limit")
		return
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		for _, k := range []string{"run", "session", "public_id", "agent", "kinds"} {
			if _, ok := q[k]; ok {
				badRequest(w, r, "name the stream in the body or in the query, not both")
				return
			}
		}
		var req struct {
			Run      string `json:"run"`
			Session  string `json:"session"`
			PublicID string `json:"public_id"`
			Agent    string `json:"agent"`
			Kinds    string `json:"kinds"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			badRequest(w, r, "body must be JSON: {\"run\"|\"session\"|\"public_id\"|\"agent\": \"…\", \"kinds\": \"event,run\"}")
			return
		}
		q = url.Values{}
		for k, v := range map[string]string{
			"run": req.Run, "session": req.Session, "public_id": req.PublicID,
			"agent": req.Agent, "kinds": req.Kinds,
		} {
			if v != "" {
				q[k] = []string{v}
			}
		}
	}
	sel, err := liveSelector(q)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	kinds, err := liveKinds(q)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	if !s.scopeLive(w, r, sel) {
		return
	}
	id := idFrom(r)
	exp := s.now().Add(liveGrantTTL).UTC()
	claims := liveGrantClaims{ID: grantOpen}
	switch {
	case id.server:
		claims.ID = grantServer
	case id.panel != nil:
		claims = liveGrantClaims{ID: grantPanel, PublicID: id.panel.PublicID, Scope: id.panel.Scope}
		if id.panel.Exp.Before(exp) {
			exp = id.panel.Exp.UTC() // never outlives the token it came from
		}
	}
	claims.Exp = exp
	sig, err := signLiveGrant(s.grantKey, claims, liveStream(sel, kinds))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "sign the live grant")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, r, http.StatusOK, struct {
		Sig string    `json:"sig"`
		Exp time.Time `json:"exp"`
	}{sig, exp})
}
