package thread_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// FuzzDecideSigned (step 7.1): the signed-decision verifier over
// arbitrary input never panics, answers only from its error catalogue,
// and records nothing unless every check passed — a fuzzed decision is
// a client that turned hostile, and the door is the MAC. The seed set
// holds one properly signed decision (the mutator starts a byte away
// from acceptance), so every error path is reachable and the accepted
// path is exercised too: the one input that verifies closes the
// boundary, and everything after it answers ErrNotPending or ErrReplay
// — both catalogued.
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
	turn, err := s.Send(ctx, weft.User("refund it"))
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
	valid, err := json.Marshal(thread.SignDecision(key, req, thread.Approve(req.CallID)))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte(`{}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(`{"key_id":"nope","mac":"AAAA"}`))
	f.Add([]byte(`{"key_id":"k1","kind":"approve","expiry":"2030-01-01T00:00:00Z","mac":"AAAA"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var sd thread.SignedDecision
		if err := json.Unmarshal(data, &sd); err != nil {
			return // malformed JSON is the decoder's loud, not the verifier's
		}
		_, err := s.DecideSigned(ctx, sd)
		switch {
		case err == nil:
			if p := s.Pending(); len(p) != 0 {
				t.Fatalf("a verified decision left the boundary open: %+v", p)
			}
		case errors.Is(err, thread.ErrUnknownKey),
			errors.Is(err, thread.ErrBadSignature),
			errors.Is(err, thread.ErrExpired),
			errors.Is(err, thread.ErrReplay),
			errors.Is(err, thread.ErrNotPending),
			errors.Is(err, thread.ErrArgsChanged):
			// the catalogue, in the order DecideSigned checks
		default:
			t.Fatalf("an error outside the catalogue: %v", err)
		}
	})
}

var _ = weft.User // the parked walk's shape, kept honest
