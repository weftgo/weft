package thread

import (
	"context"
	"encoding/json"
	"fmt"
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
// string with prefix — the "path stays under the workspace" shape.
func ArgPrefix(pointer, prefix string) Arg {
	return Arg{Pointer: pointer, Prefix: prefix}
}

// ArgGlob returns the predicate requiring args[pointer] to be a
// string matching glob — the "go test …" command shape. A command is
// not a path: * spans separators, so "go test*" matches
// "go test ./...". End a path prefix with its separator or it matches
// siblings too ("/ws" matches "/ws-evil") — the prefix's one sharp
// edge, documented rather than hidden.
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
// for a session grant, the store's id for a shared one.
type grantRef struct {
	id     string
	shared bool
}

// matchGrant is the decision chain's first step, filled in (ADR 0021
// §2, §4): the session's live grants, newest first, then the shared
// store's. A matching grant decides at once — an approval runs the
// call, a deny-grant refuses it with its reason — and the chain
// writes the audit entry that counts the match as a use.
func (s *Session) matchGrant(ctx context.Context, c weft.ToolCallPart) (Decision, grantRef, bool) {
	now := time.Now().UTC()
	s.mu.Lock()
	live := s.liveGrantsLocked(now)
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

// liveGrants returns the session's live grants, newest first: not
// revoked, not expired, and under their MaxUses where the audit trail
// can count. Callers hold s.mu.
func (s *Session) liveGrantsLocked(now time.Time) []GrantEntry {
	revoked := map[string]bool{}
	uses := map[string]int{}
	for _, e := range s.order {
		switch e := e.(type) {
		case GrantRevokedEntry:
			revoked[e.GrantID] = true
		case ApprovalAuditEntry:
			if e.Step == StepGrant && e.Outcome == "approved" && strings.HasPrefix(e.Detail, "grant ") {
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
		if g.MaxUses > 0 && uses[g.ID] >= g.MaxUses {
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
		var want, got any
		if err := json.Unmarshal(a.Equals, &want); err != nil {
			return false
		}
		if err := json.Unmarshal(v, &got); err != nil {
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
		case px < len(pattern) && (pattern[px] == '?' || pattern[px] == s[sx]):
			px++
			sx++
		case px < len(pattern) && pattern[px] == '*':
			star = px
			mark = sx
			px++
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
func pointerValue(raw json.RawMessage, pointer string) (json.RawMessage, bool) {
	if pointer == "" {
		return raw, true
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, false
	}
	var cur any
	if err := json.Unmarshal(raw, &cur); err != nil {
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

// jsonEqual compares two decoded JSON values deeply. Numbers compare
// as float64 — 1 and 1.0 equal — the encoding's own equality.
func jsonEqual(a, b any) bool {
	switch a := a.(type) {
	case nil:
		return b == nil
	case bool:
		bb, ok := b.(bool)
		return ok && a == bb
	case float64:
		bb, ok := b.(float64)
		return ok && a == bb
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
