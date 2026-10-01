package studio

import (
	"context"
	"encoding/json"
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

// liveKey is the dedup key S4.5 names: (run, kind, pos). A retried
// transport and the database's own publish both re-deliver a record;
// one delivery is enough.
type liveKey struct {
	run  string
	kind string
	pos  int64
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

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "internal",
			"streaming is not supported by this connection")
		return
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
	w.WriteHeader(http.StatusOK)
	sseFlush(w, flusher)

	sent := map[liveKey]struct{}{}
	if r.Header.Get("Last-Event-ID") != "" {
		s.backfill(w, flusher, sel, kinds, sent)
	}

	ticker := time.NewTicker(pingEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// The heartbeat frame (S4.5): keeps proxies from timing the
			// connection out; never a record. A failed write ends the
			// stream at the next select either way (ctx is done).
			_, _ = fmt.Fprint(w, "event: ping\ndata: {}\n\n")
			sseFlush(w, flusher)
		case f, open := <-frames:
			if !open {
				// The hub dropped this subscriber: its queue (obsdb's
				// QueueSize, 1,000 by default) overflowed. Say so and
				// close (S4.5): the client refetches pages and
				// reconnects.
				if ctx.Err() == nil {
					_, _ = fmt.Fprint(w, "event: overflow\ndata: {}\n\n")
					sseFlush(w, flusher)
				}
				return
			}
			if !liveWanted(kinds, f) {
				continue
			}
			if key, ok := liveDedupKey(f); ok {
				if _, dup := sent[key]; dup {
					continue
				}
				sent[key] = struct{}{}
			}
			writeFrame(w, flusher, f)
		}
	}
}

// liveWanted reports whether the kinds parameter admits this frame:
// record frames by their kind (heartbeats never — S4.5: they only
// move last-seen), run frames as one kind of their own.
func liveWanted(kinds map[string]bool, f obsdb.Frame) bool {
	switch f.Kind {
	case obsdb.FrameRecord:
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
func writeFrame(w http.ResponseWriter, flusher http.Flusher, f obsdb.Frame) {
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
		}
		data, err := json.Marshal(dto)
		if err != nil {
			return
		}
		_, _ = fmt.Fprintf(w, "id: %d\nevent: record\ndata: %s\n\n", f.Seq, data)
	case obsdb.FrameRun:
		if f.Run == nil {
			return
		}
		rowJSON, err := json.Marshal(row(*f.Run))
		if err != nil {
			return
		}
		data, _ := json.Marshal(runFrameDTO{Run: rowJSON})
		_, _ = fmt.Fprintf(w, "id: %d\nevent: run\ndata: %s\n\n", f.Seq, data)
	}
	sseFlush(w, flusher)
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
// it has seen.
func (s *Server) backfill(
	w http.ResponseWriter, flusher http.Flusher,
	sel obsdb.Selector, kinds map[string]bool, sent map[liveKey]struct{},
) {
	ctx := context.Background()
	query := obsdb.RunQuery{ParentRunID: "*", Limit: 500}
	switch {
	case sel.RunID != "":
		// One run: read it directly (its children are separate runs,
		// outside a run-scoped subscription).
		det, err := s.db.Run(ctx, sel.RunID)
		if err != nil {
			return // nothing stored (yet): the live stream is the whole story
		}
		s.backfillRun(w, flusher, det.RunRow, kinds, sent)
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
			s.backfillRun(w, flusher, rec, kinds, sent)
		}
		if page.NextBefore == nil || len(page.Runs) == 0 {
			return
		}
		query.Before = *page.NextBefore
	}
}

// backfillRun replays one run's stored durable records as record
// frames: its events (with their positions) and, when asked for, the
// transcript bodies (a messages record per body, positioned by
// index).
func (s *Server) backfillRun(
	w http.ResponseWriter, flusher http.Flusher,
	rec obsdb.RunRow, kinds map[string]bool, sent map[liveKey]struct{},
) {
	ctx := context.Background()
	if kinds["event"] {
		page, err := s.db.Events(ctx, rec.ID, -1, 1000)
		if err == nil {
			for {
				for _, pe := range page.Events {
					key := liveKey{run: rec.ID, kind: "event", pos: pe.Pos}
					if _, dup := sent[key]; dup {
						continue
					}
					sent[key] = struct{}{}
					dto := recordFrameDTO{
						RunID: rec.ID, SessionID: rec.SessionID, PublicID: rec.PublicID,
						Kind: "event", Pos: pe.Pos, Time: pe.Time,
						TraceID: rec.TraceID,
						Event:   rawOrNull(string(pe.Event)),
					}
					if data, err := json.Marshal(dto); err == nil {
						_, _ = fmt.Fprintf(w, "event: record\ndata: %s\n\n", data)
					}
				}
				if page.NextAfter == nil {
					break
				}
				page, err = s.db.Events(ctx, rec.ID, *page.NextAfter, 1000)
				if err != nil {
					break
				}
			}
			sseFlush(w, flusher)
		}
	}
	if kinds["messages"] {
		bodies, err := s.db.Transcript(ctx, rec.ID)
		if err == nil {
			for i, body := range bodies {
				key := liveKey{run: rec.ID, kind: "messages", pos: int64(i)}
				if _, dup := sent[key]; dup {
					continue
				}
				sent[key] = struct{}{}
				dto := recordFrameDTO{
					RunID: rec.ID, SessionID: rec.SessionID, PublicID: rec.PublicID,
					Kind: "messages", Pos: int64(i),
					Event: rawOrNull(string(body)),
				}
				if data, err := json.Marshal(dto); err == nil {
					_, _ = fmt.Fprintf(w, "event: record\ndata: %s\n\n", data)
				}
			}
			sseFlush(w, flusher)
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

// sseFlush flushes one SSE write.
func sseFlush(w http.ResponseWriter, f http.Flusher) {
	if f != nil {
		f.Flush()
	}
}
