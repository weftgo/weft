package obsdb

import (
	"context"
	"sync"
)

// Frame is one live-lane message: a record as ingested (deltas
// included), or a run row that changed.
type Frame struct {
	Kind   string  // "record" | "run"
	Record *Record // Kind == record; Weft derived
	Weft   Weft
	Run    *RunRow // Kind == run
	Seq    uint64  // hub-wide, monotonic: the SSE id and the resume cursor
}

// Frame kinds.
const (
	FrameRecord = "record"
	FrameRun    = "run"
)

// Selector scopes a subscription: exactly one field set. A run-scoped
// subscription follows one run; a session- or public-id-scoped one spans
// the many runs of one conversation, which is why the cursor is
// hub-wide rather than per run.
type Selector struct{ RunID, SessionID, PublicID, Agent string }

// matches reports whether a frame's derived identity falls in the
// selector. An empty selector (or an over-set one, treated as empty)
// matches nothing, never everything: a live tail must be scoped.
func (s Selector) matches(w Weft) bool {
	set := 0
	for _, v := range []string{s.RunID, s.SessionID, s.PublicID, s.Agent} {
		if v != "" {
			set++
		}
	}
	if set != 1 {
		return false
	}
	switch {
	case s.RunID != "":
		return w.RunID == s.RunID
	case s.SessionID != "":
		return w.SessionID == s.SessionID
	case s.PublicID != "":
		return w.PublicID == s.PublicID
	default:
		return w.Agent == s.Agent
	}
}

// Hub is the live lane. Publish never blocks the caller beyond a
// bounded per-subscriber queue; a slow subscriber is dropped with
// Overflow.
type Hub interface {
	Publish(ctx context.Context, f Frame)
	Subscribe(ctx context.Context, sel Selector, after uint64) (<-chan Frame, error)
}

// A subscription's channel closes when the hub drops it (overflow) or
// the context ends; a receiver distinguishes the two by ctx.Err.
type hubSub struct {
	sel   Selector
	ch    chan Frame
	after uint64
	ctx   context.Context
	// dropped closes when the hub drops the subscription on overflow,
	// so its context watcher ends with it instead of waiting on a
	// context that may never end.
	dropped chan struct{}
}

type inProcessHub struct {
	mu    sync.Mutex
	seq   uint64
	queue int
	subs  map[*hubSub]struct{}
}

// NewHub returns the in-process hub: one Seq counter for every frame,
// one bounded queue per subscriber (QueueSize, default 1000). Hosted
// Studio swaps in a Redis/NATS implementation of the same interface;
// this one is what setup A's live lane runs on.
func NewHub(opts ...HubOption) Hub {
	h := &inProcessHub{subs: map[*hubSub]struct{}{}}
	for _, o := range opts {
		o.applyHub(h)
	}
	if h.queue == 0 {
		h.queue = 1000
	}
	return h
}

// HubOption configures NewHub.
type HubOption interface{ applyHub(*inProcessHub) }

// QueueSize sets the per-subscriber queue depth. Publish to a full
// queue drops the subscriber (its channel closes) rather than blocking
// the writer: the durable lane is the database's job, and a live tail
// can always resume from it.
func QueueSize(n int) HubOption { return queueSize(n) }

type queueSize int

func (q queueSize) applyHub(h *inProcessHub) {
	if q > 0 {
		h.queue = int(q)
	}
}

func (h *inProcessHub) Publish(ctx context.Context, f Frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	f.Seq = h.seq
	for sub := range h.subs {
		if f.Seq <= sub.after || !sub.sel.matches(f.Weft) {
			continue
		}
		// Non-blocking send: a full queue means the subscriber is too
		// slow for the live lane — drop it, loudly (the closed channel).
		select {
		case sub.ch <- f:
		default:
			delete(h.subs, sub)
			close(sub.ch)
			close(sub.dropped)
		}
	}
}

func (h *inProcessHub) Subscribe(ctx context.Context, sel Selector, after uint64) (<-chan Frame, error) {
	sub := &hubSub{sel: sel, ch: make(chan Frame, h.queue), after: after, ctx: ctx, dropped: make(chan struct{})}
	h.mu.Lock()
	// Seq is this hub's own counter and is not persisted: a cursor ahead
	// of it was minted by another hub — a Last-Event-ID a client kept
	// across a restart. Nothing this hub publishes is a repeat for that
	// client, so the cursor reads as none; honouring it would mute the
	// subscription until Seq caught up with the old process's.
	if sub.after > h.seq {
		sub.after = 0
	}
	h.subs[sub] = struct{}{}
	h.mu.Unlock()
	go func() {
		// Context end also drops the subscription; the channel close is
		// the same signal either way. An overflow drop got there first
		// and leaves nothing to do.
		select {
		case <-ctx.Done():
		case <-sub.dropped:
			return
		}
		h.mu.Lock()
		if _, ok := h.subs[sub]; ok {
			delete(h.subs, sub)
			close(sub.ch)
		}
		h.mu.Unlock()
	}()
	return sub.ch, nil
}

// RecordFrame builds the frame a Write publishes for one record: the
// record as ingested with its derived identity, deltas included.
func RecordFrame(r Record) Frame {
	return Frame{Kind: FrameRecord, Record: &r, Weft: DeriveRecord(r)}
}

// RunFrame builds the frame a Write publishes for a run row that
// changed.
func RunFrame(r RunRow) Frame {
	w := Weft{
		RunID: r.ID, ParentRunID: r.ParentRunID, ParentCallID: r.ParentCallID,
		SessionID: r.SessionID, PublicID: r.PublicID, Turn: r.Turn, Agent: r.Agent,
		Playground: r.Playground, ExperimentID: r.ExperimentID, ForkedFrom: r.ForkedFrom,
	}
	return Frame{Kind: FrameRun, Run: &r, Weft: w}
}
