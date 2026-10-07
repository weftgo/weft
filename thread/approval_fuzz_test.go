package thread_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// FuzzDecideSigned: the signed-decision verifier over arbitrary input
// never panics, answers only from its error catalogue, and records no
// decision unless every check passed — a fuzzed decision is a client
// that turned hostile, and the door is the MAC. The seed set holds one
// properly signed decision (the mutator starts a byte away from
// acceptance) and the near misses a hostile signer would try — a
// blank nonce, a made-up one, a zeroed expiry, another occurrence —
// each properly MAC'd, so the checks behind the MAC are reachable.
// The one input that verifies closes the boundary, and everything
// after it answers ErrNotPending or ErrReplay — both catalogued.
// ErrUnknownKey is not in DecideSigned's catalogue: an unknown key id
// reads as a bad signature.
func FuzzDecideSigned(f *testing.F) {
	ctx := context.Background()
	key := []byte("fuzz-key-material")
	ring, err := thread.NewKeyring(thread.Key{ID: "k1", Secret: key, Active: true})
	if err != nil {
		f.Fatal(err)
	}
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring), thread.AutoResume(false))
	if err != nil {
		f.Fatal(err)
	}
	turn, err := s.Send(ctx, core.User("refund it"))
	if err != nil {
		f.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		f.Fatal(err)
	}
	pend := s.Pending()
	if len(pend) != 1 {
		f.Fatalf("the parked call never parked: %+v", pend)
	}
	req, err := s.Request(pend[0].CallID)
	if err != nil {
		f.Fatal(err)
	}
	seed := func(edit func(*thread.Request)) {
		r := req
		if edit != nil {
			edit(&r)
		}
		b, err := json.Marshal(thread.SignDecision(key, r, thread.Approve(r.CallID)))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	seed(nil)
	seed(func(r *thread.Request) { r.Nonce = "" })
	seed(func(r *thread.Request) { r.Nonce = "00000000000000000000000000000000.00000000000000000000000000000000" })
	seed(func(r *thread.Request) { r.Expiry = time.Unix(1, 0) })
	seed(func(r *thread.Request) { r.ID, r.RunID = "e_another", r.Session+"-t99" })
	seed(func(r *thread.Request) { r.KeyID = "k9" })
	seed(func(r *thread.Request) { r.ArgsSHA256 = "00" })
	f.Add([]byte(`{}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(`{"KeyID":"nope","MAC":"AAAA"}`))
	f.Add([]byte(`{"KeyID":"k1","Kind":"approve","Expiry":"2030-01-01T00:00:00Z","MAC":"AAAA"}`))

	decisions := func() int {
		n := 0
		for _, e := range s.Entries() {
			if _, ok := e.(thread.ApprovalDecisionEntry); ok {
				n++
			}
		}
		return n
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var sd thread.SignedDecision
		if err := json.Unmarshal(data, &sd); err != nil {
			return // malformed JSON is the decoder's loud, not the verifier's
		}
		before := decisions()
		_, err := s.DecideSigned(ctx, sd)
		switch {
		case err == nil:
			if p := s.Pending(); len(p) != 0 {
				t.Fatalf("a verified decision left the boundary open: %+v", p)
			}
			if sd.Nonce == "" {
				t.Fatal("a decision with no nonce verified")
			}
			if got := decisions(); got != before+1 {
				t.Fatalf("a verified decision recorded %d decision entries, want 1", got-before)
			}
		case errors.Is(err, thread.ErrUnknownKey):
			t.Fatalf("DecideSigned named an unknown key: %v", err)
		case errors.Is(err, thread.ErrBadSignature),
			errors.Is(err, thread.ErrExpired),
			errors.Is(err, thread.ErrReplay),
			errors.Is(err, thread.ErrNotPending),
			errors.Is(err, thread.ErrArgsChanged):
			// the catalogue — and fail-closed: a refusal records no
			// decision (its audit step is not one)
			if got := decisions(); got != before {
				t.Fatalf("a refused signature recorded %d decision entries: %v", got-before, err)
			}
		default:
			t.Fatalf("an error outside the catalogue: %v", err)
		}
	})
}
