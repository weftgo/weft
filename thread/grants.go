package thread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/weftgo/weft"
)

// A Grant approves — or, with Deny, refuses — future calls without
// asking (ADR 0021 §4): one tool, and argument predicates every one
// of which the call's arguments must satisfy, so "allow run_command
// for go test …" is expressible, not just "allow run_command". Scope,
// expiry, uses and revocation are the grant's lifetime: a
// session-scoped grant is an entry (s.Grant), a shared grant lives
// behind the GrantStore interface; an expiry passes; MaxUses bounds
// the times a session-scoped grant may match (counted from its audit
// trail); s.Revoke ends one with an entry.
type Grant struct {
	// Tool is the tool name the grant matches, exactly.
	Tool string `json:"tool"`
	// Args are the argument predicates; every one must match. Empty
	// matches any arguments, which is the tool-wide grant Crush
	// taught the field to want more than (ADR 0021 §4's rejected).
	Args []Arg `json:"args,omitempty"`
	// Deny makes the grant a standing refusal: the chain denies the
	// call at once, with Reason the model sees.
	Deny bool `json:"deny,omitempty"`
	// Reason is the refusal's text on a deny-grant — the model-visible
	// bytes, "denied by grant" when empty — or the record's note on an
	// approval grant.
	Reason string `json:"reason,omitempty"`
	// Expiry is when the grant lapses; zero means never. Expired
	// strictly after, the requests' rule.
	Expiry time.Time `json:"expiry,omitzero"`
	// MaxUses bounds how many times a session-scoped grant may match
	// (its audit entries count the uses); 0 means unlimited. A shared
	// grant's uses are the store's own business — the session cannot
	// see other sessions' matches.
	MaxUses int `json:"max_uses,omitempty"`
}

// Arg is one argument predicate (ADR 0021 §4): a JSON pointer into
// the call's arguments and the test the value there must pass —
// equality with ArgEquals, a string prefix with ArgPrefix, a glob
// with ArgGlob. Build one with its constructor; a hand-made Arg with
// several tests set uses Equals first, then Prefix, then Glob.
type Arg struct {
	// Pointer is the RFC 6901 JSON pointer to the argument: "/order_id",
	// "/file/path", "/command".
	Pointer string `json:"pointer"`
	// Equals is the raw JSON the value must equal (ArgEquals).
	Equals json.RawMessage `json:"equals,omitempty"`
	// Prefix is the string prefix the value must have (ArgPrefix).
	Prefix string `json:"prefix,omitempty"`
	// Glob is the glob the string value must match (ArgGlob) — *
	// spans separators, a command is not a path.
	Glob string `json:"glob,omitempty"`
}

// ArgEquals returns the predicate requiring args[pointer] to equal
// value as JSON: numbers compare numerically, strings and booleans
// exactly, arrays and objects deeply.
func ArgEquals(pointer string, value json.RawMessage) Arg {
	return Arg{Pointer: pointer, Equals: slices.Clone(value)}
}

// ArgPrefix returns the predicate requiring args[pointer] to be a
// string with prefix — the "path stays under the workspace" shape. A
// prefix is a plain string prefix, nothing more: end it with the
// separator or it matches siblings too ("/ws" matches "/ws-evil") —
// the prefix's one sharp edge, documented rather than hidden. An
// empty prefix matches nothing (an Arg with no test never matches,
// the same rule a hand-made Arg follows): the tool-wide grant is no
// Args at all, not an empty prefix.
func ArgPrefix(pointer, prefix string) Arg {
	return Arg{Pointer: pointer, Prefix: prefix}
}

// ArgGlob returns the predicate requiring args[pointer] to be a
// string matching glob — the "go test …" command shape. A command is
// not a path: * spans separators, so "go test*" matches
// "go test ./..." (and anything after it on the line — anchor the
// glob's tail when that matters).
func ArgGlob(pointer, glob string) Arg {
	return Arg{Pointer: pointer, Glob: glob}
}

// grantStoreOption carries the shared scope into the configuration.
type grantStoreOption struct{ gs GrantStore }

func (o grantStoreOption) applySession(c *sessionConfig) {
	if o.gs != nil {
		c.grantStore = o.gs
	}
}

// WithGrantStore returns the SessionOption adding the shared grant
// scope (ADR 0021 §4): grants many sessions consult, application-wide,
// behind the interface the application owns. The chain consults it
// after the session's own grants. A nil store is ignored.
func WithGrantStore(gs GrantStore) SessionOption { return grantStoreOption{gs} }

// A GrantStore is the application-wide grant scope (ADR 0021 §4):
// grants that outlive any one session, consulted by every session
// handed the store. The store owns their lifetime — expiry, revocation,
// use counts — because only it can see every session's matches; the
// session's entry-scoped grants keep their own rules.
type GrantStore interface {
	// Grants returns the store's live grants. The chain consults it
	// once per parked call, before parking; an error reads as no
	// match, with a warning through the agent's logger — a broken
	// store never parks nothing silently, and never decides either.
	Grants(ctx context.Context) ([]SharedGrant, error)
}

// A SharedGrant is one grant of a GrantStore, with the store's id for
// the audit trail.
type SharedGrant struct {
	ID string
	Grant
}

// Grant appends a session-scoped grant (ADR 0021 §4): the decision
// chain consults it — newest grant first — for every call about to
// park, from the next boundary on. The tool name must not be empty:
// a grant that matches every tool is a policy this shape does not
// have.
func (s *Session) Grant(ctx context.Context, g Grant) error {
	if g.Tool == "" {
		return fmt.Errorf("thread: Grant with no tool name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendLocked(ctx, func(id, parent string, created time.Time) Entry {
		return GrantEntry{ID: id, ParentID: parent, Created: created, Grant: g}
	})
}

// Revoke ends a session-scoped grant with an entry (ADR 0021 §4):
// revocation is an append — the grant entry stays, the walk reads the
// revocation after it. grantID must name a grant entry the session
// holds; a shared grant's revocation is its store's own operation.
func (s *Session) Revoke(ctx context.Context, grantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[grantID]; !ok {
		return fmt.Errorf("thread: session %s holds no grant %q", s.header.ID, grantID)
	}
	if _, ok := s.order[s.byID[grantID]].(GrantEntry); !ok {
		return fmt.Errorf("thread: entry %q is not a grant", grantID)
	}
	return s.appendLocked(ctx, func(id, parent string, created time.Time) Entry {
		return GrantRevokedEntry{ID: id, ParentID: parent, Created: created, GrantID: grantID}
	})
}

// grantRef names a matched grant for the audit entry: the entry id
// for a session grant, the store's id for a shared one — and shared
// is what keeps the two apart where ids could collide: the audit
// detail namespaces a shared match ("shared grant …"), so the
// session's own use counting never counts a shared match against a
// session grant that happens to hold the same id.
type grantRef struct {
	id     string
	shared bool
}

// matchGrant is the decision chain's first step, filled in (ADR 0021
// §2, §4): the session's live grants, newest first, then the shared
// store's. A matching grant decides at once — an approval runs the
// call, a deny-grant refuses it with its reason — and the chain
// writes the audit entry that counts the match as a use.
//
// batch holds the uses this chain has already matched but not yet
// recorded — its audit entries land with the chain's one append, after
// every call is decided — so a MaxUses grant counts the calls of its
// own batch too: MaxUses 1 over three parallel calls approves one.
func (s *Session) matchGrant(ctx context.Context, c weft.ToolCallPart, batch map[string]int) (Decision, grantRef, bool) {
	now := time.Now().UTC()
	s.mu.Lock()
	live := s.liveGrantsLocked(now, batch)
	s.mu.Unlock()
	for _, g := range live {
		if grantMatches(g.Grant, c) {
			return grantDecision(g.Grant), grantRef{id: g.ID}, true
		}
	}
	if s.cfg.grantStore != nil {
		shared, err := s.cfg.grantStore.Grants(ctx)
		if err != nil {
			s.agent.Logger().Warn("thread: grant store unreadable; no shared grants consulted",
				"session", s.header.ID, "call", c.ID, "err", err)
		} else {
			for _, g := range shared {
				if grantMatches(g.Grant, c) {
					return grantDecision(g.Grant), grantRef{id: g.ID, shared: true}, true
				}
			}
		}
	}
	return Decision{}, grantRef{}, false
}

// grantDecision is what a matched grant decides (ADR 0021 §4): an
// approval via the grant, or the refusal with its reason — the
// model-visible default pinned ("denied by grant").
func grantDecision(g Grant) Decision {
	if g.Deny {
		reason := g.Reason
		if reason == "" {
			reason = deniedByGrant
		}
		return Deny("", reason) // CallID set by the chain
	}
	d := Approve("")
	d.Via = "grant"
	if g.Reason != "" {
		d.Reason = g.Reason
	}
	return d
}

// deniedByGrant is the model-visible default refusal text of a
// deny-grant with no reason of its own (ADR 0021 §5: pinned bytes).
const deniedByGrant = "denied by grant"

// liveGrantsLocked returns the session's live grants, newest first:
// not revoked, not expired, and under their MaxUses where the audit
// trail can count — a use is a match, whichever way the grant
// decided: a deny-grant that matched counts like an approval grant,
// or its standing refusal would outlive its MaxUses. pending adds uses
// not yet in the trail (the chain's own batch, keyed by grant id); nil
// adds none. Callers hold s.mu.
func (s *Session) liveGrantsLocked(now time.Time, pending map[string]int) []GrantEntry {
	revoked := map[string]bool{}
	uses := map[string]int{}
	for _, e := range s.order {
		switch e := e.(type) {
		case GrantRevokedEntry:
			revoked[e.GrantID] = true
		case ApprovalAuditEntry:
			if e.Step == StepGrant && (e.Outcome == "approved" || e.Outcome == "denied") && strings.HasPrefix(e.Detail, "grant ") {
				uses[strings.TrimPrefix(e.Detail, "grant ")]++
			}
		}
	}
	var out []GrantEntry
	for i := len(s.order) - 1; i >= 0; i-- {
		g, ok := s.order[i].(GrantEntry)
		if !ok || revoked[g.ID] {
			continue
		}
		if !g.Expiry.IsZero() && now.After(g.Expiry) {
			continue
		}
		if g.MaxUses > 0 && uses[g.ID]+pending[g.ID] >= g.MaxUses {
			continue
		}
		out = append(out, g)
	}
	return out
}

// grantMatches reports whether the call satisfies the grant: the tool
// name exactly, then every argument predicate.
func grantMatches(g Grant, c weft.ToolCallPart) bool {
	if g.Tool != c.Name {
		return false
	}
	for _, a := range g.Args {
		if !argMatches(a, c.Args) {
			return false
		}
	}
	return true
}

// argMatches evaluates one predicate against the call's arguments. A
// pointer the arguments do not reach is no match — loudly absent, not
// silently null.
func argMatches(a Arg, args json.RawMessage) bool {
	v, ok := pointerValue(args, a.Pointer)
	if !ok {
		return false
	}
	switch {
	case len(a.Equals) > 0:
		want, err := decodeExact(a.Equals)
		if err != nil {
			return false
		}
		got, err := decodeExact(v)
		if err != nil {
			return false
		}
		return jsonEqual(want, got)
	case a.Prefix != "":
		s, ok := jsonString(v)
		return ok && strings.HasPrefix(s, a.Prefix)
	case a.Glob != "":
		s, ok := jsonString(v)
		if !ok {
			return false
		}
		return wildcardMatch(a.Glob, s)
	}
	return false
}

// wildcardMatch is the ArgGlob matcher: * matches any run of
// characters including separators (a command is not a path — "go
// test*" must span "./..."), ? one character, everything else itself.
// The classic two-pointer scan, no backtracking beyond a starred
// restart.
func wildcardMatch(pattern, s string) bool {
	px, sx, star, mark := 0, 0, -1, -1
	for sx < len(s) {
		switch {
		// The star is tested first: a '*' in s must not consume the
		// pattern's star as a literal, or the star never records a
		// restart ("*" over "*0").
		case px < len(pattern) && pattern[px] == '*':
			star = px
			mark = sx
			px++
		case px < len(pattern) && (pattern[px] == '?' || pattern[px] == s[sx]):
			px++
			sx++
		case star >= 0:
			px = star + 1
			mark++
			sx = mark
		default:
			return false
		}
	}
	for px < len(pattern) && pattern[px] == '*' {
		px++
	}
	return px == len(pattern)
}

// pointerValue resolves an RFC 6901 JSON pointer in raw bytes,
// returning the raw JSON at the tip. ~0 and ~1 unescape; the whole
// document is "/" — pointer "" is the document itself.
//
// An object on the path holding a second key that equals the token
// under case folding is no match: encoding/json binds struct fields
// case-insensitively, last key winning, so {"command":"go test",
// "Command":"rm -rf /"} would show the grant one value and hand the
// tool the other. The grant cannot know which key the tool reads, so
// it fails closed.
func pointerValue(raw json.RawMessage, pointer string) (json.RawMessage, bool) {
	if pointer == "" {
		return raw, true
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, false
	}
	cur, err := decodeExact(raw)
	if err != nil {
		return nil, false
	}
	for _, tok := range strings.Split(pointer[1:], "/") {
		tok = strings.ReplaceAll(tok, "~1", "/")
		tok = strings.ReplaceAll(tok, "~0", "~")
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[tok]
			if !ok {
				return nil, false
			}
			for k := range node {
				if k != tok && strings.EqualFold(k, tok) {
					return nil, false
				}
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			cur = node[i]
		default:
			return nil, false
		}
	}
	return json.RawMessage(mustJSON(cur)), true
}

// jsonString extracts a string value from raw JSON.
func jsonString(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// decodeExact decodes raw JSON with its numbers kept as written
// (json.Number) — never rounded through float64, where two ids past
// 2^53 read equal and one id's grant would approve the other. Trailing
// data is an error, as json.Unmarshal has it.
func decodeExact(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("thread: trailing data after the JSON value")
	}
	return v, nil
}

// numberKey renders a JSON number's exact value canonically — sign,
// significant digits, exponent — so 1, 1.0, 0.1e1 and 10e-1 share a
// key while 9007199254740993 and 9007199254740992 do not. Pure string
// work: no float rounding, and no big arithmetic an exponent like
// 1e999999999 could inflate.
func numberKey(n json.Number) string {
	s := string(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	mant, expPart, hasExp := strings.Cut(strings.ToLower(s), "e")
	exp := int64(0)
	if hasExp {
		e, err := strconv.ParseInt(expPart, 10, 64)
		if err != nil || e > 1<<40 || e < -(1<<40) {
			return string(n) // out of any sane range: compare as written
		}
		exp = e
	}
	intPart, frac, _ := strings.Cut(mant, ".")
	digits := intPart + frac
	exp -= int64(len(frac))
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return "0" // every zero, -0 included
	}
	trimmed := strings.TrimRight(digits, "0")
	exp += int64(len(digits) - len(trimmed))
	sign := ""
	if neg {
		sign = "-"
	}
	return sign + trimmed + "e" + strconv.FormatInt(exp, 10)
}

// jsonEqual compares two decoded JSON values deeply. Numbers compare
// by exact value — 1 and 1.0 equal, the encoding's own equality — and
// never through float64's rounding.
func jsonEqual(a, b any) bool {
	switch a := a.(type) {
	case nil:
		return b == nil
	case bool:
		bb, ok := b.(bool)
		return ok && a == bb
	case json.Number:
		bb, ok := b.(json.Number)
		return ok && numberKey(a) == numberKey(bb)
	case string:
		bb, ok := b.(string)
		return ok && a == bb
	case []any:
		bb, ok := b.([]any)
		if !ok || len(a) != len(bb) {
			return false
		}
		for i := range a {
			if !jsonEqual(a[i], bb[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		bb, ok := b.(map[string]any)
		if !ok || len(a) != len(bb) {
			return false
		}
		for k, v := range a {
			bv, ok := bb[k]
			if !ok || !jsonEqual(v, bv) {
				return false
			}
		}
		return true
	}
	return false
}
