package thread_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/threadtest"
	"github.com/weftgo/weft/wefttest"
)

// The package's contract for failures is "sentinel errors, wrapped
// with context and matched with errors.Is" (doc.go, # Errors). This
// table holds every exported sentinel to it: each row provokes the
// error through the public API that documents it — thread.Memory, a
// scripted model, the IDs and Clock options — and the one assertion is
// errors.Is(err, sentinel). A sentinel returned as text, or wrapped
// with %v, fails its row.
//
// Every exported Err* of the package has at least one row; the two
// weft sentinels a Send documents (ErrInvalidRunOption,
// ErrInvalidSteer) ride along.
func TestSentinelsAreMatchable(t *testing.T) {
	rows := []struct {
		name     string
		sentinel error
		provoke  func(t *testing.T) error
	}{
		// --- Retry: ErrBusy, ErrLocked -------------------------------
		{"ErrBusy/Send under the Reject policy while a turn runs", thread.ErrBusy, func(t *testing.T) error {
			h := sentinelHeld(t, thread.BusyPolicy(thread.Reject))
			_, err := h.s.Send(sentinelCtx(t), weft.User("one more thing"))
			return err
		}},
		{"ErrBusy/Send As(Reject) while a turn runs", thread.ErrBusy, func(t *testing.T) error {
			h := sentinelHeld(t)
			_, err := h.s.Send(sentinelCtx(t), weft.User("one more thing"), thread.As(thread.Reject))
			return err
		}},
		{"ErrBusy/Branch while a turn runs", thread.ErrBusy, func(t *testing.T) error {
			h := sentinelHeld(t)
			return h.s.Branch(sentinelCtx(t), "")
		}},
		{"ErrBusy/Compact while a turn runs", thread.ErrBusy, func(t *testing.T) error {
			h := sentinelHeld(t)
			return h.s.Compact(sentinelCtx(t))
		}},
		{"ErrBusy/ApplyCompaction while a turn runs", thread.ErrBusy, func(t *testing.T) error {
			h := sentinelHeld(t)
			return h.s.ApplyCompaction(sentinelCtx(t), &thread.Compaction{Summary: "x", FirstKept: h.s.Leaf()})
		}},
		{"ErrBusy/Uncompact while a turn runs", thread.ErrBusy, func(t *testing.T) error {
			h := sentinelHeld(t)
			return h.s.Uncompact(sentinelCtx(t))
		}},
		{"ErrBusy/CustomMessage while a turn runs", thread.ErrBusy, func(t *testing.T) error {
			h := sentinelHeld(t)
			return h.s.CustomMessage(sentinelCtx(t), "app/note", weft.User("slipped in"))
		}},
		{"ErrLocked/a second Session writes while the first is open", thread.ErrLocked, func(t *testing.T) error {
			ctx := sentinelCtx(t)
			st, agent := thread.Memory(), weft.New(wefttest.Script())
			writer := sentinelCreate(t, st, agent)
			reader, err := thread.Open(ctx, st, writer.ID(), agent)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			return reader.SetInfo(ctx, "mine now", nil)
		}},

		// --- Reopen: ErrStale, ErrClosed ------------------------------
		{"ErrStale/a write after another writer appended and closed", thread.ErrStale, func(t *testing.T) error {
			ctx := sentinelCtx(t)
			st, agent := thread.Memory(), weft.New(wefttest.Script())
			writer := sentinelCreate(t, st, agent)
			reader, err := thread.Open(ctx, st, writer.ID(), agent)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if err := writer.SetInfo(ctx, "moved on", nil); err != nil {
				t.Fatalf("the writer's SetInfo: %v", err)
			}
			if err := writer.Close(ctx); err != nil {
				t.Fatalf("the writer's Close: %v", err)
			}
			return reader.SetInfo(ctx, "behind", nil)
		}},
		{"ErrClosed/Send after Close", thread.ErrClosed, func(t *testing.T) error {
			s := sentinelClosed(t)
			_, err := s.Send(sentinelCtx(t), weft.User("late"))
			return err
		}},
		{"ErrClosed/Continue after Close", thread.ErrClosed, func(t *testing.T) error {
			s := sentinelClosed(t)
			_, err := s.Continue(sentinelCtx(t))
			return err
		}},
		{"ErrClosed/SetInfo after Close", thread.ErrClosed, func(t *testing.T) error {
			return sentinelClosed(t).SetInfo(sentinelCtx(t), "late", nil)
		}},
		{"ErrClosed/Compact after Close", thread.ErrClosed, func(t *testing.T) error {
			// A session with something to compact: the closed check is
			// the write's, so a closed session with nothing to compact
			// is refused earlier, with ErrNothingToCompact.
			ctx := sentinelCtx(t)
			s, _ := sentinelLong(t, weft.New(wefttest.Script(wefttest.Say("a summary"))))
			if err := s.Close(ctx); err != nil {
				t.Fatalf("Close: %v", err)
			}
			return s.Compact(ctx)
		}},
		{"ErrClosed/Decide after Close", thread.ErrClosed, func(t *testing.T) error {
			ctx := sentinelCtx(t)
			p := sentinelParked(t, thread.AutoResume(false))
			if err := p.s.Close(ctx); err != nil {
				t.Fatalf("Close: %v", err)
			}
			_, err := p.s.Decide(ctx, thread.Approve(p.call))
			return err
		}},
		{"ErrClosed/Branch after Close", thread.ErrClosed, func(t *testing.T) error {
			return sentinelClosed(t).Branch(sentinelCtx(t), "")
		}},

		// --- Terminal for the stored data: ErrCorrupt, ErrNewerFormat --
		{"ErrCorrupt/Open over a malformed line", thread.ErrCorrupt, func(t *testing.T) error {
			st, id := sentinelInjected(t, "{not json}\n")
			_, err := thread.Open(sentinelCtx(t), st, id, weft.New(wefttest.Script()))
			return err
		}},
		{"ErrCorrupt/Load over a malformed line", thread.ErrCorrupt, func(t *testing.T) error {
			st, id := sentinelInjected(t, "{not json}\n")
			_, _, _, err := st.Load(sentinelCtx(t), id)
			return err
		}},
		{"ErrCorrupt/Open over an entry whose parent the session does not hold", thread.ErrCorrupt, func(t *testing.T) error {
			ctx := sentinelCtx(t)
			st := thread.Memory()
			h := thread.Header{ID: "s_orphan", Created: time.Now().UTC()}
			if err := st.Create(ctx, h); err != nil {
				t.Fatalf("Create: %v", err)
			}
			if err := st.Append(ctx, h.ID, thread.MessageEntry{
				ID: "e_1", ParentID: "e_ghost", Created: h.Created, Message: weft.User("hello"),
			}); err != nil {
				t.Fatalf("Append: %v", err)
			}
			_, err := thread.Open(ctx, st, h.ID, weft.New(wefttest.Script()))
			return err
		}},
		{"ErrNewerFormat/UnmarshalEntry of an unknown kind", thread.ErrNewerFormat, func(t *testing.T) error {
			_, err := thread.UnmarshalEntry([]byte(`{"type":"from_the_future","id":"e_1"}`))
			return err
		}},
		{"ErrNewerFormat/UnmarshalEntry of a newer v", thread.ErrNewerFormat, func(t *testing.T) error {
			_, err := thread.UnmarshalEntry([]byte(`{"type":"message","v":99,"id":"e_1"}`))
			return err
		}},
		{"ErrNewerFormat/a header from a newer weft", thread.ErrNewerFormat, func(t *testing.T) error {
			var h thread.Header
			return json.Unmarshal([]byte(`{"type":"session","weft":99,"id":"s_1"}`), &h)
		}},
		{"ErrNewerFormat/Open over an entry of an unknown kind", thread.ErrNewerFormat, func(t *testing.T) error {
			st, id := sentinelInjected(t, `{"type":"from_the_future","id":"e_1"}`+"\n")
			_, err := thread.Open(sentinelCtx(t), st, id, weft.New(wefttest.Script()))
			return err
		}},

		// --- A refused call: the storage's ---------------------------
		{"ErrNotFound/Open of an id the storage does not hold", thread.ErrNotFound, func(t *testing.T) error {
			_, err := thread.Open(sentinelCtx(t), thread.Memory(), "s_missing", weft.New(wefttest.Script()))
			return err
		}},
		{"ErrNotFound/Delete of an id the storage does not hold", thread.ErrNotFound, func(t *testing.T) error {
			return thread.Delete(sentinelCtx(t), thread.Memory(), "s_missing")
		}},
		{"ErrNotFound/Load of an id the storage does not hold", thread.ErrNotFound, func(t *testing.T) error {
			_, _, _, err := thread.Memory().Load(sentinelCtx(t), "s_missing")
			return err
		}},
		{"ErrNotFound/Append to an id the storage does not hold", thread.ErrNotFound, func(t *testing.T) error {
			return thread.Memory().Append(sentinelCtx(t), "s_missing",
				thread.MessageEntry{ID: "e_1", Created: time.Now().UTC(), Message: weft.User("hello")})
		}},
		{"ErrExists/Create under an id the storage already holds", thread.ErrExists, func(t *testing.T) error {
			ctx := sentinelCtx(t)
			st, agent := thread.Memory(), weft.New(wefttest.Script())
			same := thread.IDs(func() string { return "s_same" })
			if _, err := thread.Create(ctx, st, agent, same); err != nil {
				t.Fatalf("the first Create: %v", err)
			}
			_, err := thread.Create(ctx, st, agent, same)
			return err
		}},
		{"ErrExists/Storage.Create of a header twice", thread.ErrExists, func(t *testing.T) error {
			ctx := sentinelCtx(t)
			st := thread.Memory()
			h := thread.Header{ID: "s_twice", Created: time.Now().UTC()}
			if err := st.Create(ctx, h); err != nil {
				t.Fatalf("the first Create: %v", err)
			}
			return st.Create(ctx, h)
		}},
		{"ErrCreateOnly/Open with WithMeta", thread.ErrCreateOnly, func(t *testing.T) error {
			return sentinelOpenWith(t, thread.WithMeta(map[string]string{"app": "billing"}))
		}},
		{"ErrCreateOnly/Open with PublicID", thread.ErrCreateOnly, func(t *testing.T) error {
			return sentinelOpenWith(t, thread.PublicID("pub-1"))
		}},
		{"ErrCreateOnly/Open with WithLineage", thread.ErrCreateOnly, func(t *testing.T) error {
			return sentinelOpenWith(t, thread.WithLineage("s_parent", "call_1"))
		}},
		{"ErrReservedKey/SetInfo under the weft. prefix", thread.ErrReservedKey, func(t *testing.T) error {
			s := sentinelCreate(t, thread.Memory(), weft.New(wefttest.Script()))
			return s.SetInfo(sentinelCtx(t), "", map[string]string{"weft.public_id": "other"})
		}},

		// --- A refused call: compaction ------------------------------
		{"ErrNothingToCompact/Compact on an empty session", thread.ErrNothingToCompact, func(t *testing.T) error {
			s := sentinelCreate(t, thread.Memory(), weft.New(wefttest.Script()))
			return s.Compact(sentinelCtx(t))
		}},
		{"ErrNothingToCompact/ApplyCompaction of the boundary already left", thread.ErrNothingToCompact, func(t *testing.T) error {
			ctx := sentinelCtx(t)
			s, ids := sentinelLong(t, weft.New(wefttest.Script()))
			plan := &thread.Compaction{Summary: "Orders were reviewed.", FirstKept: ids[2]}
			if err := s.ApplyCompaction(ctx, plan); err != nil {
				t.Fatalf("the first ApplyCompaction: %v", err)
			}
			return s.ApplyCompaction(ctx, plan)
		}},
		{"ErrSummaryTruncated/Compact with a summary cut at the output cap", thread.ErrSummaryTruncated, func(t *testing.T) error {
			// Two cut summaries: the attempt and its one retry, with no
			// fallback left.
			cut := wefttest.Script(wefttest.MaxTokens("Goal: ship the"), wefttest.MaxTokens("Goal: ship the"))
			s, _ := sentinelLong(t, weft.New(cut))
			return s.Compact(sentinelCtx(t))
		}},
		{"ErrCompactConfig/Create with a window under the reserve", thread.ErrCompactConfig, func(t *testing.T) error {
			_, err := thread.Create(sentinelCtx(t), thread.Memory(), weft.New(wefttest.Script()),
				thread.ContextWindow(100_000), thread.Reserve(100_000))
			return err
		}},
		{"ErrCompactConfig/Open with a window under the reserve", thread.ErrCompactConfig, func(t *testing.T) error {
			return sentinelOpenWith(t, thread.ContextWindow(100_000), thread.Reserve(100_000))
		}},
		{"ErrCompactCanceled/Compact with a BeforeCompact hook answering Cancel", thread.ErrCompactCanceled, func(t *testing.T) error {
			s, _ := sentinelLong(t, weft.New(wefttest.Script(wefttest.Say("a summary"))),
				thread.BeforeCompact(func(context.Context, *thread.Preparation) (thread.Verdict, error) {
					return thread.Cancel(), nil
				}))
			return s.Compact(sentinelCtx(t))
		}},
		{"ErrInvalidCompaction/ApplyCompaction with no Compaction", thread.ErrInvalidCompaction, func(t *testing.T) error {
			s, _ := sentinelLong(t, weft.New(wefttest.Script()))
			return s.ApplyCompaction(sentinelCtx(t), nil)
		}},
		{"ErrInvalidCompaction/ApplyCompaction with no summary and no trim", thread.ErrInvalidCompaction, func(t *testing.T) error {
			s, ids := sentinelLong(t, weft.New(wefttest.Script()))
			return s.ApplyCompaction(sentinelCtx(t), &thread.Compaction{FirstKept: ids[2]})
		}},
		{"ErrAwaitingApproval/Compact while a request is pending", thread.ErrAwaitingApproval, func(t *testing.T) error {
			p := sentinelParked(t)
			return p.s.Compact(sentinelCtx(t))
		}},
		{"ErrAwaitingApproval/ApplyCompaction while a request is pending", thread.ErrAwaitingApproval, func(t *testing.T) error {
			p := sentinelParked(t)
			return p.s.ApplyCompaction(sentinelCtx(t), &thread.Compaction{Summary: "x", FirstKept: p.s.Leaf()})
		}},
		{"ErrNotPinnable/Pin of a custom entry", thread.ErrNotPinnable, func(t *testing.T) error {
			ctx := sentinelCtx(t)
			s := sentinelCreate(t, thread.Memory(), weft.New(wefttest.Script()))
			if err := s.Custom(ctx, "app/state", json.RawMessage(`{"k":1}`)); err != nil {
				t.Fatalf("Custom: %v", err)
			}
			for _, e := range s.Entries() {
				if c, ok := e.(thread.CustomEntry); ok {
					return s.Pin(ctx, c.ID)
				}
			}
			t.Fatal("the session holds no custom entry")
			return nil
		}},
		{"ErrNoEntry/Pin of an entry the session does not hold", thread.ErrNoEntry, func(t *testing.T) error {
			s := sentinelCreate(t, thread.Memory(), weft.New(wefttest.Script()))
			return s.Pin(sentinelCtx(t), "e_missing")
		}},
		{"ErrNoEntry/ApplyCompaction keeping from an entry the session does not hold", thread.ErrNoEntry, func(t *testing.T) error {
			s, _ := sentinelLong(t, weft.New(wefttest.Script()))
			return s.ApplyCompaction(sentinelCtx(t), &thread.Compaction{Summary: "x", FirstKept: "e_missing"})
		}},
		{"ErrNoEntry/Uncompact with no compaction to undo", thread.ErrNoEntry, func(t *testing.T) error {
			s := sentinelCreate(t, thread.Memory(), weft.New(wefttest.Script()))
			return s.Uncompact(sentinelCtx(t))
		}},

		// --- A refused call: approvals -------------------------------
		{"ErrExpired/Decide past the request's expiry", thread.ErrExpired, func(t *testing.T) error {
			clock := newSentinelClock()
			p := sentinelParked(t, thread.Clock(clock.Now), thread.RequestExpiry(time.Minute), thread.AutoResume(false))
			clock.Advance(2 * time.Minute)
			_, err := p.s.Decide(sentinelCtx(t), thread.Approve(p.call))
			return err
		}},
		{"ErrExpired/Request past the request's expiry", thread.ErrExpired, func(t *testing.T) error {
			clock := newSentinelClock()
			ring, _ := sentinelRing(t)
			p := sentinelParked(t, thread.WithKeyring(ring), thread.Clock(clock.Now),
				thread.RequestExpiry(time.Minute), thread.AutoResume(false))
			clock.Advance(2 * time.Minute)
			_, err := p.s.Request(p.call)
			return err
		}},
		{"ErrExpired/DecideSigned past the request's expiry", thread.ErrExpired, func(t *testing.T) error {
			clock := newSentinelClock()
			ring, secret := sentinelRing(t)
			p := sentinelParked(t, thread.WithKeyring(ring), thread.Clock(clock.Now),
				thread.RequestExpiry(time.Minute), thread.AutoResume(false))
			challenge, err := p.s.Request(p.call)
			if err != nil {
				t.Fatalf("Request: %v", err)
			}
			clock.Advance(2 * time.Minute)
			_, err = p.s.DecideSigned(sentinelCtx(t), thread.SignDecision(secret, challenge, thread.Approve(p.call)))
			return err
		}},
		{"ErrBadSignature/DecideSigned under a key the ring does not hold", thread.ErrBadSignature, func(t *testing.T) error {
			ring, _ := sentinelRing(t)
			p := sentinelParked(t, thread.WithKeyring(ring), thread.AutoResume(false))
			challenge, err := p.s.Request(p.call)
			if err != nil {
				t.Fatalf("Request: %v", err)
			}
			_, err = p.s.DecideSigned(sentinelCtx(t),
				thread.SignDecision([]byte("not the ring's secret"), challenge, thread.Approve(p.call)))
			return err
		}},
		{"ErrBadSignature/DecideSigned with a tampered MAC", thread.ErrBadSignature, func(t *testing.T) error {
			ring, secret := sentinelRing(t)
			p := sentinelParked(t, thread.WithKeyring(ring), thread.AutoResume(false))
			challenge, err := p.s.Request(p.call)
			if err != nil {
				t.Fatalf("Request: %v", err)
			}
			signed := thread.SignDecision(secret, challenge, thread.Approve(p.call))
			signed.Kind = thread.OutcomeDeny // swapped after signing
			_, err = p.s.DecideSigned(sentinelCtx(t), signed)
			return err
		}},
		{"ErrReplay/DecideSigned with a signature that already decided", thread.ErrReplay, func(t *testing.T) error {
			ctx := sentinelCtx(t)
			ring, secret := sentinelRing(t)
			p := sentinelParked(t, thread.WithKeyring(ring), thread.AutoResume(false))
			challenge, err := p.s.Request(p.call)
			if err != nil {
				t.Fatalf("Request: %v", err)
			}
			signed := thread.SignDecision(secret, challenge, thread.Approve(p.call))
			if _, err := p.s.DecideSigned(ctx, signed); err != nil {
				t.Fatalf("the first DecideSigned: %v", err)
			}
			_, err = p.s.DecideSigned(ctx, signed)
			return err
		}},
		{"ErrArgsChanged/DecideSigned over another arguments hash", thread.ErrArgsChanged, func(t *testing.T) error {
			ring, secret := sentinelRing(t)
			p := sentinelParked(t, thread.WithKeyring(ring), thread.AutoResume(false))
			challenge, err := p.s.Request(p.call)
			if err != nil {
				t.Fatalf("Request: %v", err)
			}
			challenge.ArgsSHA256 = strings.Repeat("0", 64)
			_, err = p.s.DecideSigned(sentinelCtx(t), thread.SignDecision(secret, challenge, thread.Approve(p.call)))
			return err
		}},
		{"ErrUnknownKey/Keyring.Sign over a challenge under another ring's key", thread.ErrUnknownKey, func(t *testing.T) error {
			ring, _ := sentinelRing(t)
			p := sentinelParked(t, thread.WithKeyring(ring), thread.AutoResume(false))
			challenge, err := p.s.Request(p.call)
			if err != nil {
				t.Fatalf("Request: %v", err)
			}
			other, err := thread.NewKeyring(thread.Key{ID: "another", Secret: []byte("another ring's secret"), Active: true})
			if err != nil {
				t.Fatalf("NewKeyring: %v", err)
			}
			_, err = other.Sign(challenge, thread.Approve(p.call))
			return err
		}},
		{"ErrNotPending/Decide on a call that never parked", thread.ErrNotPending, func(t *testing.T) error {
			p := sentinelParked(t, thread.AutoResume(false))
			_, err := p.s.Decide(sentinelCtx(t), thread.Approve("call_never_parked"))
			return err
		}},
		{"ErrNotPending/Decide on a call decided already", thread.ErrNotPending, func(t *testing.T) error {
			ctx := sentinelCtx(t)
			p := sentinelParked(t, thread.AutoResume(false))
			if _, err := p.s.Decide(ctx, thread.Approve(p.call)); err != nil {
				t.Fatalf("the first Decide: %v", err)
			}
			_, err := p.s.Decide(ctx, thread.Approve(p.call))
			return err
		}},
		{"ErrNotPending/Request for a call that never parked", thread.ErrNotPending, func(t *testing.T) error {
			ring, _ := sentinelRing(t)
			p := sentinelParked(t, thread.WithKeyring(ring), thread.AutoResume(false))
			_, err := p.s.Request("call_never_parked")
			return err
		}},
		{"ErrNotPending/Resume with no open boundary", thread.ErrNotPending, func(t *testing.T) error {
			s := sentinelCreate(t, thread.Memory(), weft.New(wefttest.Script()))
			_, err := s.Resume(sentinelCtx(t))
			return err
		}},
		{"ErrNotPending/ResolveDelegation on a call that delegates to no child", thread.ErrNotPending, func(t *testing.T) error {
			p := sentinelParked(t, thread.AutoResume(false))
			_, err := p.s.ResolveDelegation(sentinelCtx(t), p.call, "s_child", "forged", false)
			return err
		}},
		{"ErrSignatureRequired/Decide on a RequireSigned session", thread.ErrSignatureRequired, func(t *testing.T) error {
			ring, _ := sentinelRing(t)
			p := sentinelParked(t, thread.WithKeyring(ring), thread.RequireSigned(), thread.AutoResume(false))
			_, err := p.s.Decide(sentinelCtx(t), thread.Approve(p.call))
			return err
		}},
		{"ErrInvalidDecision/Decide with no decisions", thread.ErrInvalidDecision, func(t *testing.T) error {
			p := sentinelParked(t, thread.AutoResume(false))
			_, err := p.s.Decide(sentinelCtx(t))
			return err
		}},
		{"ErrInvalidDecision/Decide naming one call twice", thread.ErrInvalidDecision, func(t *testing.T) error {
			p := sentinelParked(t, thread.AutoResume(false))
			_, err := p.s.Decide(sentinelCtx(t), thread.Approve(p.call), thread.Deny(p.call, "no"))
			return err
		}},
		{"ErrInvalidDecision/Decide with no outcome", thread.ErrInvalidDecision, func(t *testing.T) error {
			p := sentinelParked(t, thread.AutoResume(false))
			_, err := p.s.Decide(sentinelCtx(t), thread.Decision{CallID: p.call})
			return err
		}},
		{"ErrDelegated/Decide on a call that delegates to a child", thread.ErrDelegated, func(t *testing.T) error {
			p := sentinelDelegating(t)
			_, err := p.s.Decide(sentinelCtx(t), thread.Approve(p.call))
			return err
		}},
		{"ErrDelegated/Request for a call that delegates to a child", thread.ErrDelegated, func(t *testing.T) error {
			ring, _ := sentinelRing(t)
			p := sentinelDelegating(t, thread.WithKeyring(ring))
			_, err := p.s.Request(p.call)
			return err
		}},

		// --- How a turn ended: Turn.Wait ------------------------------
		{"ErrDropped/a queued send ClearQueue removed", thread.ErrDropped, func(t *testing.T) error {
			ctx := sentinelCtx(t)
			h := sentinelHeld(t)
			queued, err := h.s.Send(ctx, weft.User("then email me the result"))
			if err != nil {
				t.Fatalf("the queued Send: %v", err)
			}
			if n, err := h.s.ClearQueue(ctx); err != nil || n != 1 {
				t.Fatalf("ClearQueue = %d, %v; want 1 dropped", n, err)
			}
			h.finish(t)
			_, err = queued.WaitContext(ctx)
			return err
		}},
		{"ErrDropped/a queued steer ClearQueue removed", thread.ErrDropped, func(t *testing.T) error {
			ctx := sentinelCtx(t)
			h := sentinelHeld(t)
			steer, err := h.s.Send(ctx, weft.User("skip the appendix"), thread.As(thread.Steer))
			if err != nil {
				t.Fatalf("the steer: %v", err)
			}
			if n, err := h.s.ClearQueue(ctx); err != nil || n != 1 {
				t.Fatalf("ClearQueue = %d, %v; want 1 dropped", n, err)
			}
			h.finish(t)
			_, err = steer.WaitContext(ctx)
			return err
		}},
		{"ErrNotRun/a queued send whose context ended before its turn", thread.ErrNotRun, func(t *testing.T) error {
			ctx := sentinelCtx(t)
			h := sentinelHeld(t)
			qctx, cancel := context.WithCancel(ctx)
			queued, err := h.s.Send(qctx, weft.User("never mind"))
			if err != nil {
				t.Fatalf("the queued Send: %v", err)
			}
			cancel()
			h.finish(t)
			_, err = queued.WaitContext(ctx)
			if !errors.Is(err, context.Canceled) {
				t.Errorf("the not-run turn's error does not wrap context.Canceled: %v", err)
			}
			return err
		}},
		{"ErrNotPersisted/a turn whose end the storage refused", thread.ErrNotPersisted, func(t *testing.T) error {
			ctx := sentinelCtx(t)
			st := &sentinelNoTurnEnd{Storage: thread.Memory()}
			s := sentinelCreate(t, st, weft.New(wefttest.Script(wefttest.Say("answered"))))
			turn, err := s.Send(ctx, weft.User("hello"))
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			res, err := turn.WaitContext(ctx)
			if res == nil {
				t.Errorf("the run's result does not ride beside the error")
			}
			return err
		}},
		{"ErrTurnPanicked/a Clock that panics while the runner writes", thread.ErrTurnPanicked, func(t *testing.T) error {
			var armed atomic.Bool
			h := sentinelHeld(t, thread.Clock(func() time.Time {
				if armed.Load() {
					panic("the clock blew up")
				}
				return time.Now()
			}))
			armed.Store(true)
			h.release()
			_, err := h.turn.WaitContext(sentinelCtx(t))
			armed.Store(false) // the session works again once the clock does
			return err
		}},

		// --- weft's sentinels a Send documents ------------------------
		{"weft.ErrInvalidRunOption/RunOptions carrying weft.Approve", weft.ErrInvalidRunOption, func(t *testing.T) error {
			s := sentinelCreate(t, thread.Memory(), weft.New(wefttest.Script(wefttest.Say("x"))))
			_, err := s.Send(sentinelCtx(t), weft.User("go"), thread.RunOptions(weft.Approve("call_1")))
			return err
		}},
		{"weft.ErrInvalidRunOption/RunOptions carrying weft.Deny", weft.ErrInvalidRunOption, func(t *testing.T) error {
			s := sentinelCreate(t, thread.Memory(), weft.New(wefttest.Script(wefttest.Say("x"))))
			_, err := s.Send(sentinelCtx(t), weft.User("go"), thread.RunOptions(weft.Deny("call_1", "no")))
			return err
		}},
		{"weft.ErrInvalidRunOption/RunOptions carrying weft.Prompt", weft.ErrInvalidRunOption, func(t *testing.T) error {
			s := sentinelCreate(t, thread.Memory(), weft.New(wefttest.Script(wefttest.Say("x"))))
			_, err := s.Send(sentinelCtx(t), weft.User("go"), thread.RunOptions(weft.Prompt("something else")))
			return err
		}},
		{"weft.ErrInvalidRunOption/RunOptions carrying weft.Steering", weft.ErrInvalidRunOption, func(t *testing.T) error {
			s := sentinelCreate(t, thread.Memory(), weft.New(wefttest.Script(wefttest.Say("x"))))
			_, err := s.Send(sentinelCtx(t), weft.User("go"), thread.RunOptions(
				weft.Steering(func(context.Context, weft.SteerPoint) []weft.Message { return nil })))
			return err
		}},
		{"weft.ErrInvalidSteer/a steer that is not a user message", weft.ErrInvalidSteer, func(t *testing.T) error {
			h := sentinelHeld(t)
			_, err := h.s.Send(sentinelCtx(t),
				weft.Message{Role: weft.RoleAssistant, Content: []weft.Part{weft.TextPart{Text: "I speak for the model"}}},
				thread.As(thread.Steer))
			return err
		}},
	}

	// Every exported sentinel of the package is named by a row: a new
	// Err* that joins errors.go without one fails here, by name.
	covered := map[error]bool{}
	for _, row := range rows {
		covered[row.sentinel] = true
	}
	for name, sentinel := range map[string]error{
		"ErrBusy": thread.ErrBusy, "ErrNotFound": thread.ErrNotFound, "ErrExists": thread.ErrExists,
		"ErrLocked": thread.ErrLocked, "ErrStale": thread.ErrStale, "ErrCorrupt": thread.ErrCorrupt,
		"ErrNewerFormat": thread.ErrNewerFormat, "ErrNotPending": thread.ErrNotPending,
		"ErrUnknownKey": thread.ErrUnknownKey, "ErrBadSignature": thread.ErrBadSignature,
		"ErrExpired": thread.ErrExpired, "ErrReplay": thread.ErrReplay, "ErrArgsChanged": thread.ErrArgsChanged,
		"ErrSignatureRequired": thread.ErrSignatureRequired, "ErrClosed": thread.ErrClosed,
		"ErrNotPersisted": thread.ErrNotPersisted, "ErrNotRun": thread.ErrNotRun, "ErrDropped": thread.ErrDropped,
		"ErrTurnPanicked": thread.ErrTurnPanicked, "ErrCreateOnly": thread.ErrCreateOnly,
		"ErrReservedKey": thread.ErrReservedKey, "ErrInvalidDecision": thread.ErrInvalidDecision,
		"ErrDelegated": thread.ErrDelegated, "ErrNothingToCompact": thread.ErrNothingToCompact,
		"ErrCompactCanceled": thread.ErrCompactCanceled, "ErrNoEntry": thread.ErrNoEntry,
		"ErrSummaryTruncated": thread.ErrSummaryTruncated, "ErrInvalidCompaction": thread.ErrInvalidCompaction,
		"ErrCompactConfig": thread.ErrCompactConfig, "ErrNotPinnable": thread.ErrNotPinnable,
		"ErrAwaitingApproval": thread.ErrAwaitingApproval,
	} {
		if !covered[sentinel] {
			t.Errorf("thread.%s has no row", name)
		}
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			err := row.provoke(t)
			if err == nil {
				t.Fatalf("no error; want one matching %q", row.sentinel)
			}
			if !errors.Is(err, row.sentinel) {
				t.Fatalf("errors.Is(err, %q) = false\n  err:  %v\n  type: %T", row.sentinel, err, err)
			}
		})
	}
}

// sentinelCtx is a row's context: bounded, so a row that would
// deadlock fails instead of hanging the binary.
func sentinelCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// sentinelCreate creates a session on st, failing the row on error.
func sentinelCreate(t *testing.T, st thread.Storage, agent *weft.Agent, opts ...thread.SessionOption) *thread.Session {
	t.Helper()
	s, err := thread.Create(sentinelCtx(t), st, agent, opts...)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return s
}

// sentinelClosed returns a Session whose Close has run.
func sentinelClosed(t *testing.T) *thread.Session {
	t.Helper()
	s := sentinelCreate(t, thread.Memory(), weft.New(wefttest.Script(wefttest.Say("x"))))
	if err := s.Close(sentinelCtx(t)); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return s
}

// sentinelOpenWith creates a plain session, closes it, and returns the
// error of opening it again with opts.
func sentinelOpenWith(t *testing.T, opts ...thread.SessionOption) error {
	t.Helper()
	ctx := sentinelCtx(t)
	st, agent := thread.Memory(), weft.New(wefttest.Script())
	s := sentinelCreate(t, st, agent)
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err := thread.Open(ctx, st, s.ID(), agent, opts...)
	return err
}

// sentinelInjected returns a Memory storage holding one session whose
// stored data ends in raw, verbatim — what a broken or a newer writer
// would have left (Memory holds raw bytes: threadtest.RawInjector).
func sentinelInjected(t *testing.T, raw string) (thread.Storage, string) {
	t.Helper()
	ctx := sentinelCtx(t)
	st := thread.Memory()
	h := thread.Header{ID: "s_injected", Created: time.Now().UTC()}
	if err := st.Create(ctx, h); err != nil {
		t.Fatalf("Create: %v", err)
	}
	inj, ok := st.(threadtest.RawInjector)
	if !ok {
		t.Fatalf("%T does not implement threadtest.RawInjector", st)
	}
	if err := inj.Inject(ctx, h.ID, []byte(raw)); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	return st, h.ID
}

// sentinelLong returns a session whose history is three long user
// messages — more than the default KeepRecent keeps raw, so one
// compaction has something to summarize — opened on agent with opts,
// and the three entry ids.
func sentinelLong(t *testing.T, agent *weft.Agent, opts ...thread.SessionOption) (*thread.Session, []string) {
	t.Helper()
	ctx := sentinelCtx(t)
	st := thread.Memory()
	s := sentinelCreate(t, st, agent, opts...)
	ids := []string{"e_0", "e_1", "e_2"}
	parent := ""
	var batch []thread.Entry
	for i, topic := range []string{"order ", "invoice ", "refund "} {
		batch = append(batch, thread.MessageEntry{
			ID: ids[i], ParentID: parent, Created: time.Now().UTC(),
			Message: weft.User(strings.Repeat(topic, 30_000/len(topic))),
		})
		parent = ids[i]
	}
	if err := st.Append(ctx, s.ID(), batch...); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// The entries went in behind the Session's back: close it and open
	// one that sees them.
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	long, err := thread.Open(ctx, st, s.ID(), agent, opts...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return long, ids
}

// sentinelRing is a one-key ring and its secret.
func sentinelRing(t *testing.T) (*thread.Keyring, []byte) {
	t.Helper()
	secret := []byte("a secret the session file never sees")
	ring, err := thread.NewKeyring(thread.Key{ID: "ops", Secret: secret, Active: true})
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	return ring, secret
}

// sentinelClock is a settable session clock: the expiry rows move time
// instead of sleeping through it.
type sentinelClock struct {
	mu  sync.Mutex
	now time.Time
}

func newSentinelClock() *sentinelClock {
	return &sentinelClock{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *sentinelClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *sentinelClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// sentinelPark is a session parked on one approval request, and the
// pending call's id.
type sentinelPark struct {
	s    *thread.Session
	call string
}

// sentinelParked runs one turn whose only tool call needs a decision,
// and returns the session parked on it.
func sentinelParked(t *testing.T, opts ...thread.SessionOption) sentinelPark {
	t.Helper()
	ctx := sentinelCtx(t)
	agent := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "deploy", Args: `{"env":"prod"}`}),
			wefttest.Say("Deployed."),
		),
		weft.Tool("deploy", "Deploy the service.",
			func(_ context.Context, in struct {
				Env string `json:"env"`
			}) (string, error) {
				return "deployed to " + in.Env, nil
			},
			weft.RequireApproval()))
	s := sentinelCreate(t, thread.Memory(), agent, opts...)
	turn, err := s.Send(ctx, weft.User("Deploy to prod."))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := turn.WaitContext(ctx); err != nil {
		t.Fatalf("the parking turn: %v", err)
	}
	pending := s.Pending()
	if len(pending) != 1 {
		t.Fatalf("Pending = %+v, want one request", pending)
	}
	return sentinelPark{s: s, call: pending[0].CallID}
}

// sentinelDelegating parks a call and mirrors a child's request under
// it, the way the pool does: the parked call now delegates, and takes
// no decision of its own.
func sentinelDelegating(t *testing.T, opts ...thread.SessionOption) sentinelPark {
	t.Helper()
	p := sentinelParked(t, append(opts, thread.AutoResume(false))...)
	if _, err := p.s.AppendApprovalRequests(sentinelCtx(t), thread.ApprovalRequestEntry{
		CallID: "s_child/c-inner", Tool: "wire", ArgsSHA256: "h", RunID: "s_child-t1",
		Child: "s_child", Wrapper: p.call,
	}); err != nil {
		t.Fatalf("AppendApprovalRequests: %v", err)
	}
	return p
}

// sentinelHold is a session whose first turn is held inside a tool:
// the session is busy until release is called.
type sentinelHold struct {
	s       *thread.Session
	turn    *thread.Turn
	release func()
}

// finish releases the held turn and waits for it to end well.
func (h sentinelHold) finish(t *testing.T) {
	t.Helper()
	h.release()
	if _, err := h.turn.WaitContext(sentinelCtx(t)); err != nil {
		t.Fatalf("the held turn: %v", err)
	}
}

// sentinelHeld starts a turn that blocks in its tool and returns once
// the tool has been entered. The row's cleanup releases it.
func sentinelHeld(t *testing.T, opts ...thread.SessionOption) sentinelHold {
	t.Helper()
	ctx := sentinelCtx(t)
	started, released := make(chan struct{}), make(chan struct{})
	var startOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(released) }) }
	t.Cleanup(release)
	agent := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "work"}),
			wefttest.Say("Done."),
			wefttest.Say("And the follow-up."),
		),
		weft.Tool("work", "Takes a while.", func(ctx context.Context, _ struct{}) (string, error) {
			startOnce.Do(func() { close(started) })
			select {
			case <-released:
				return "ok", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}))
	s := sentinelCreate(t, thread.Memory(), agent, opts...)
	turn, err := s.Send(ctx, weft.User("Do the long job."))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("the held turn never reached its tool")
	}
	return sentinelHold{s: s, turn: turn, release: release}
}

// sentinelNoTurnEnd is a Storage that refuses the batch carrying a
// turn entry — the failing disk ErrNotPersisted is about.
type sentinelNoTurnEnd struct {
	thread.Storage
}

func (f *sentinelNoTurnEnd) Append(ctx context.Context, session string, entries ...thread.Entry) error {
	for _, e := range entries {
		if _, ok := e.(thread.TurnEntry); ok {
			return errors.New("disk on fire")
		}
	}
	return f.Storage.Append(ctx, session, entries...)
}
