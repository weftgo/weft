package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The live stream (S4.5): GET /api/live, SSE. Frames carry the hub's
// Seq as the SSE id — the resume cursor — and the client dedups on
// (run, kind, pos), which is what makes at-least-once publishing safe.

// pingEvery is the SSE heartbeat cadence (S4.5: every 15 s). A field,
// not a constant, only so tests can tighten it.
var pingEvery = 15 * time.Second

// liveWriteTimeout bounds one frame's write: a client that stopped
// reading without closing (a suspended tab, a stalled proxy) fails the
// write instead of holding the handler — its goroutine and its hub
// subscription — in Write forever. A variable only so tests can
// tighten it.
var liveWriteTimeout = 30 * time.Second

// liveDedupSize bounds a connection's dedup set (below).
const liveDedupSize = 1 << 16

// liveKey is the dedup key S4.5 names: (run, kind, pos). A retried
// transport and the database's own publish both re-deliver a record;
// one delivery is enough.
type liveKey struct {
	run  string
	kind string
	pos  int64
}

// liveDedup is one connection's set of forwarded keys, bounded: the
// duplicates it exists for — a retried transport, the database's own
// publish beside ingest's, the resume backfill overlapping the live
// frames queued behind it — arrive close to the original, so the
// newest liveDedupSize keys are enough, and a stream left open for
// days does not grow by one key per record (deltas included). The
// client dedups on the same key (S4.5), so an eviction can cost a
// repeated frame at worst, never a wrong one.
type liveDedup struct {
	max   int
	set   map[liveKey]struct{}
	order []liveKey // insertion order; order[head:] is live
	head  int
	// hold suspends eviction: the resume backfill may write more than
	// max keys, and each must still dedup the live frames queued
	// behind it.
	hold bool
}

func newLiveDedup(max int) *liveDedup {
	return &liveDedup{max: max, set: map[liveKey]struct{}{}}
}

// seen reports whether k was already forwarded, recording it if not.
func (d *liveDedup) seen(k liveKey) bool {
	if _, dup := d.set[k]; dup {
		return true
	}
	d.set[k] = struct{}{}
	d.order = append(d.order, k)
	for !d.hold && len(d.set) > d.max {
		delete(d.set, d.order[d.head])
		d.head++
	}
	if d.head > len(d.order)/2 && d.head >= 1024 {
		d.order = append([]liveKey(nil), d.order[d.head:]...)
		d.head = 0
	}
	return false
}

func (d *liveDedup) len() int { return len(d.set) }

// sseWriter writes one connection's frames, each under
// liveWriteTimeout; the first failed write ends the stream (err).
type sseWriter struct {
	w   http.ResponseWriter
	rc  *http.ResponseController
	err error
}

// frame writes one formatted frame; flush sends what is buffered.
func (sw *sseWriter) frame(format string, args ...any) {
	if sw.err != nil {
		return
	}
	sw.deadline()
	_, sw.err = fmt.Fprintf(sw.w, format, args...)
}

func (sw *sseWriter) flush() {
	if sw.err != nil {
		return
	}
	sw.deadline()
	if err := sw.rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		sw.err = err
	}
}

// deadline arms the write deadline. A ResponseWriter that has none
// (an in-process transport) is not a socket a peer can stall.
func (sw *sseWriter) deadline() {
	if liveWriteTimeout > 0 {
		_ = sw.rc.SetWriteDeadline(time.Now().Add(liveWriteTimeout))
	}
}

// recordFrameDTO is the data of an `event: record` frame: the record
// as ingested, its derived identity, and its body verbatim under
// "event" (S4.5's example shape).
type recordFrameDTO struct {
	RunID     string          `json:"run_id"`
	SessionID string          `json:"session_id"`
	PublicID  string          `json:"public_id"`
	Kind      string          `json:"kind"` // event | delta | messages
	Pos       int64           `json:"pos"`
	Time      time.Time       `json:"time"`
	TraceID   string          `json:"trace_id"`
	SpanID    string          `json:"span_id"`
	Event     json.RawMessage `json:"event"`
	// Attrs is the record's weft.content.* attributes, as the events
	// route's rows carry them (posEvent); absent when there are none.
	Attrs map[string]any `json:"attrs,omitempty"`
}

// runFrameDTO is the data of an `event: run` frame.
type runFrameDTO struct {
	Run json.RawMessage `json:"run"`
}

// liveSelector parses S4.5's selector: exactly one of run, session,
// public_id or agent, or the request is a 400.
func liveSelector(q map[string][]string) (obsdb.Selector, error) {
	var sel obsdb.Selector
	set := 0
	for _, f := range []struct {
		name string
		dst  *string
	}{
		{"run", &sel.RunID},
		{"session", &sel.SessionID},
		{"public_id", &sel.PublicID},
		{"agent", &sel.Agent},
	} {
		vs, ok := q[f.name]
		if !ok || len(vs) == 0 || vs[0] == "" {
			continue
		}
		*f.dst, set = vs[0], set+1
	}
	if set != 1 {
		return sel, fmt.Errorf(
			"exactly one of run, session, public_id or agent is required (%d given)", set)
	}
	return sel, nil
}

// liveKinds parses the optional kinds parameter (S4.5): a
// comma-separated list over event, delta, messages and run; the
// default is event,run. Heartbeats are never a kind and never
// forwarded.
func liveKinds(q map[string][]string) (map[string]bool, error) {
	vs, ok := q["kinds"]
	if !ok || len(vs) == 0 || vs[0] == "" {
		return map[string]bool{"event": true, "run": true}, nil
	}
	kinds := map[string]bool{}
	for _, k := range strings.Split(vs[0], ",") {
		switch strings.TrimSpace(k) {
		case "event", "delta", "messages", "run":
			kinds[strings.TrimSpace(k)] = true
		case "":
			// a trailing comma
		default:
			return nil, fmt.Errorf("kind %q is not one of event, delta, messages, run", k)
		}
	}
	if len(kinds) == 0 {
		return nil, fmt.Errorf("kinds must name at least one of event, delta, messages, run")
	}
	return kinds, nil
}

// serveLive streams the live lane over SSE (S4.5).
func (s *Server) serveLive(w http.ResponseWriter, r *http.Request) {
	sel, err := liveSelector(map[string][]string(r.URL.Query()))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	kinds, err := liveKinds(map[string][]string(r.URL.Query()))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	// The resume cursor: the hub-wide frame Seq of the last frame the
	// client saw (S3.4b). Live frames newer than it are delivered by
	// the hub; the gap below it is backfilled from the database.
	var after uint64
	if id := r.Header.Get("Last-Event-ID"); id != "" {
		n, err := strconv.ParseUint(id, 10, 64)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "bad_request",
				"Last-Event-ID must be a frame id (a non-negative integer)")
			return
		}
		after = n
	}

	if !s.scopeLive(w, r, sel) {
		return
	}

	if _, ok := w.(http.Flusher); !ok {
		writeError(w, r, http.StatusInternalServerError, "internal",
			"streaming is not supported by this connection")
		return
	}
	// ServeMux routes HEAD to this GET pattern: answer the headers and
	// stop — a stream cannot follow a HEAD, and subscribing for one
	// would hold a hub subscription until the client hung up.
	if r.Method == http.MethodHead {
		h := w.Header()
		h.Set("Content-Type", "text/event-stream; charset=utf-8")
		h.Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		return
	}
	// A panel token's frames are checked one by one against its public
	// id (S4.6): the selector's own check passes a run or session that
	// nothing has stored yet, and what later runs under that id may be
	// anyone's.
	scope := ""
	if p := idFrom(r).panel; p != nil {
		scope = p.PublicID
	}
	// Subscribe before the backfill reads the database: records that
	// arrive during the backfill arrive on both paths, and the
	// (run, kind, pos) dedup keeps each to one delivery.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	frames, err := s.live.Subscribe(ctx, sel, after)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	// A buffering reverse proxy (nginx's default) would hold the tail
	// back; this is the header it honours.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	sw := &sseWriter{w: w, rc: http.NewResponseController(w)}
	sw.flush()

	sent := newLiveDedup(liveDedupSize)
	if r.Header.Get("Last-Event-ID") != "" {
		// Nothing is evicted while the backfill writes, nor until the
		// live frames queued behind it have drained (the first ping).
		sent.hold = true
		s.backfill(ctx, sw, sel, scope, kinds, sent)
	}

	ticker := time.NewTicker(pingEvery)
	defer ticker.Stop()
	for sw.err == nil {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// The heartbeat frame (S4.5): keeps proxies from timing the
			// connection out; never a record.
			sent.hold = false
			sw.frame("event: ping\ndata: {}\n\n")
			sw.flush()
		case f, open := <-frames:
			if !open {
				// The hub dropped this subscriber: its queue (obsdb's
				// QueueSize, 1,000 by default) overflowed. Say so and
				// close (S4.5): the client refetches pages and
				// reconnects.
				if ctx.Err() == nil {
					sw.frame("event: overflow\ndata: {}\n\n")
					sw.flush()
				}
				return
			}
			if !liveWanted(kinds, f) || !liveInScope(scope, f) {
				continue
			}
			if key, ok := liveDedupKey(f); ok && sent.seen(key) {
				continue
			}
			writeFrame(sw, f)
		}
	}
}

// liveInScope reports whether a panel token scoped to the public id
// scope may receive f; "" is the server token or setup A — everything.
func liveInScope(scope string, f obsdb.Frame) bool {
	if scope == "" {
		return true
	}
	if f.Kind == obsdb.FrameRun && f.Run != nil {
		return f.Run.PublicID == scope
	}
	return f.Weft.PublicID == scope
}

// liveWanted reports whether the kinds parameter admits this frame:
// record frames by their kind (heartbeats never — S4.5: they only
// move last-seen), run frames as one kind of their own.
func liveWanted(kinds map[string]bool, f obsdb.Frame) bool {
	switch f.Kind {
	case obsdb.FrameRecord:
		// A compaction view (a messages record whose weft.messages.reason
		// is set, ADR 0028 §8) is not transcript: forwarded as a messages
		// frame it would fold into the live transcript, and the catch-up
		// (which reads growth records only) never replays it.
		if f.Weft.Record == "messages" && f.Weft.Reason != "" {
			return false
		}
		return f.Weft.Record != "heartbeat" && kinds[f.Weft.Record]
	case obsdb.FrameRun:
		return kinds["run"]
	default:
		return false
	}
}

// liveDedupKey is the (run, kind, pos) of a record frame. Run frames
// and non-weft records carry no position: never deduped, always
// forwarded (a run row may legitimately change).
func liveDedupKey(f obsdb.Frame) (liveKey, bool) {
	if f.Kind != obsdb.FrameRecord || f.Record == nil || f.Weft.RunID == "" {
		return liveKey{}, false
	}
	return liveKey{run: f.Weft.RunID, kind: f.Weft.Record, pos: f.Weft.Pos}, true
}

// writeFrame writes one SSE frame with the hub's Seq as its id.
func writeFrame(sw *sseWriter, f obsdb.Frame) {
	switch f.Kind {
	case obsdb.FrameRecord:
		if f.Record == nil {
			return
		}
		dto := recordFrameDTO{
			RunID:     f.Weft.RunID,
			SessionID: f.Weft.SessionID,
			PublicID:  f.Weft.PublicID,
			Kind:      f.Weft.Record,
			Pos:       f.Weft.Pos,
			Time:      f.Record.Time,
			TraceID:   f.Record.TraceID,
			SpanID:    f.Record.SpanID,
			Event:     rawOrNull(f.Record.Body),
			Attrs:     contentAttrsOf(f.Record.Attrs),
		}
		data, err := json.Marshal(dto)
		if err != nil {
			return
		}
		sw.frame("id: %d\nevent: record\ndata: %s\n\n", f.Seq, data)
	case obsdb.FrameRun:
		if f.Run == nil {
			return
		}
		rowJSON, err := json.Marshal(row(*f.Run))
		if err != nil {
			return
		}
		data, _ := json.Marshal(runFrameDTO{Run: rowJSON})
		sw.frame("id: %d\nevent: run\ndata: %s\n\n", f.Seq, data)
	}
	sw.flush()
}

// backfill replays the stored durable records a resume needs (S4.5):
// every event and messages record the selector covers, deltas
// excluded (they are not stored). The hub's Seq is not persisted
// (obsdb is frozen this lane), so "newer than the seq" is implemented
// as the full selector backfill with the same (run, kind, pos) dedup
// S4.5 gives the client — every durable record is delivered exactly
// once per connection, live frames included, and a client that folds
// what it receives is complete. The frames carry no id: a browser
// keeps its own Last-Event-ID, and our live.ts tracks the newest seq
// it has seen. The reads ride the request's context and the walk stops
// at the first failed write: a client that left costs nothing more.
func (s *Server) backfill(
	ctx context.Context, sw *sseWriter,
	sel obsdb.Selector, scope string, kinds map[string]bool, sent *liveDedup,
) {
	query := obsdb.RunQuery{ParentRunID: "*", Limit: 500}
	switch {
	case sel.RunID != "":
		// One run: read it directly (its children are separate runs,
		// outside a run-scoped subscription).
		det, err := s.db.Run(ctx, sel.RunID)
		if err != nil {
			return // nothing stored (yet): the live stream is the whole story
		}
		s.backfillRun(ctx, sw, det.RunRow, scope, kinds, sent)
		return
	case sel.SessionID != "":
		query.SessionID = sel.SessionID
	case sel.PublicID != "":
		query.PublicID = sel.PublicID
	case sel.Agent != "":
		query.Agent = sel.Agent
	}
	for { // the runs list pages
		page, err := s.db.Runs(ctx, query)
		if err != nil {
			return
		}
		for _, rec := range page.Runs {
			if ctx.Err() != nil || sw.err != nil {
				return
			}
			s.backfillRun(ctx, sw, rec, scope, kinds, sent)
		}
		if page.NextBefore == nil || len(page.Runs) == 0 {
			return
		}
		query.Before, query.BeforeID = *page.NextBefore, page.NextBeforeID
	}
}

// backfillRun replays one run's stored durable records as record
// frames: its events (with their positions) and, when asked for, the
// transcript bodies (a messages record per body, positioned by
// index).
func (s *Server) backfillRun(
	ctx context.Context, sw *sseWriter,
	rec obsdb.RunRow, scope string, kinds map[string]bool, sent *liveDedup,
) {
	if scope != "" && rec.PublicID != scope {
		return // outside the panel token's public id (S4.6)
	}
	if kinds["event"] {
		page, err := s.db.Events(ctx, rec.ID, -1, 1000)
		if err == nil {
			for {
				for _, pe := range page.Events {
					if sent.seen(liveKey{run: rec.ID, kind: "event", pos: pe.Pos}) {
						continue
					}
					dto := recordFrameDTO{
						RunID: rec.ID, SessionID: rec.SessionID, PublicID: rec.PublicID,
						Kind: "event", Pos: pe.Pos, Time: pe.Time,
						TraceID: rec.TraceID,
						Event:   rawOrNull(string(pe.Event)),
						Attrs:   contentAttrs(pe.Content, pe.TruncatedBytes),
					}
					if data, err := json.Marshal(dto); err == nil {
						sw.frame("event: record\ndata: %s\n\n", data)
					}
				}
				if page.NextAfter == nil || sw.err != nil {
					break
				}
				page, err = s.db.Events(ctx, rec.ID, *page.NextAfter, 1000)
				if err != nil {
					break
				}
			}
			sw.flush()
		}
	}
	if kinds["messages"] {
		// TODO(A2 debt: stored step (F2/H6)): carry the stored step and input on messages frames.
		// The growth records with their stored index as the position — the
		// live lane's (run, messages, weft.messages.index) key. A slot
		// number would shift after a compaction view (ADR 0028 §8), which
		// takes an index but is not transcript, and collide with the live
		// frames of the records after it.
		batches, err := s.db.TranscriptBatches(ctx, rec.ID)
		if err == nil {
			for _, b := range batches {
				if sent.seen(liveKey{run: rec.ID, kind: "messages", pos: b.Index}) {
					continue
				}
				dto := recordFrameDTO{
					RunID: rec.ID, SessionID: rec.SessionID, PublicID: rec.PublicID,
					Kind: "messages", Pos: b.Index,
					Event: rawOrNull(string(b.Messages)),
				}
				if data, err := json.Marshal(dto); err == nil {
					sw.frame("event: record\ndata: %s\n\n", data)
				}
			}
			sw.flush()
		}
	}
}

// rawOrNull embeds a record body verbatim when it is JSON, else as a
// JSON string (a non-weft body can be any text; it never matches a
// selector, but the encoding must not fail).
func rawOrNull(body string) json.RawMessage {
	if json.Valid([]byte(body)) {
		return json.RawMessage(body)
	}
	b, _ := json.Marshal(body)
	return json.RawMessage(b)
}
