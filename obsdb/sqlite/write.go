package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
)

// Write stores one batch in a single transaction: spans and the
// event/messages records first (INSERT OR IGNORE on the transport
// idempotency keys), then one runs upsert per run the batch touched.
//
// The rules (S3.4, D3): deltas are counted, never inserted unless
// KeepDeltas; heartbeats are never inserted, they only move last_seen;
// counts increment only for rows actually inserted, so retries don't
// inflate them; any record or span — heartbeats and deltas included —
// sets last_seen to its max, started_ns to its min (a span's start, the
// earliest winning: run_start's time, or the invoke_agent span's just
// before it) and fills empty identity columns; run_finish sets the
// terminal fields; the invoke_agent span sets trace, finish, usage,
// steps, root_ended, weft_version (its attribute, else its resource's;
// the larger beside run_start's, ClickHouse's max) and, on error, failed
// and err; any weft span fills an empty manifest_hash. A run first seen
// through a later
// record (a reordered batch) gets a provisional started_ns, corrected
// when the earlier rows land. A stock GenAI span (no weft.run.id,
// obsdb.StockCandidate) re-derives its trace's derived runs
// (deriveStock, docs/record-contract.md §9).
//
// After the commit, every frame the batch produced is published to the
// DB's hub before Write returns — an in-process subscriber sees a
// record within this call (setup A's live lane).
func (d *DB) Write(ctx context.Context, b obsdb.Batch) (err error) {
	if err := d.checkOpen(); err != nil {
		return err
	}
	defer d.closedErr(&err)
	if len(b.Spans) == 0 && len(b.Records) == 0 {
		return nil
	}
	runIDs := newRunUpdates()
	tx, err := d.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var stockTraces []string
	seenTrace := map[string]bool{}
	for _, s := range b.Spans {
		w := obsdb.DeriveSpan(s)
		if _, err := insertSpan(ctx, tx, s, w); err != nil {
			return err
		}
		if w.RunID != "" {
			// A summarizer span (weft/thread's compaction or branch-summary
			// call, weft.chat.role) is stored — the summarizer usage reads
			// sum the spans table — but is never the run's own fact: filed
			// under the last run, it must not move that run's last_seen
			// (a finished run stays as it ended, an interrupted one stays
			// interrupted, Retain's clock does not restart), nor recreate a
			// deleted run's row. ClickHouse's 0024 traces view skips it alike.
			if attrString(s.Attrs, obsdb.AttrChatRole) == "" {
				runIDs.touch(w.RunID).applySpan(s, w)
			}
		} else if obsdb.StockCandidate(s) && !seenTrace[s.TraceID] {
			seenTrace[s.TraceID] = true
			stockTraces = append(stockTraces, s.TraceID)
		}
	}
	for _, r := range b.Records {
		w := obsdb.DeriveRecord(r)
		// OTLP spells "unknown" as a zero time and has the receiver use
		// the observed one (a log bridge that never set Time). The hub
		// frames below still carry the record as ingested.
		if r.Time.IsZero() {
			r.Time = r.Observed
		}
		if w.RunID == "" {
			if obsdb.IsSessionEventKind(w.Record) && w.SessionID != "" {
				// A thread record filed under no run (held at Close, U12.2):
				// the session's, never an app log line — session_events
				// alone holds it.
				if err := insertSessionEvent(ctx, tx, r, w); err != nil {
					return err
				}
				continue
			}
			if err := insertOtherLog(ctx, tx, r); err != nil {
				return err
			}
			continue
		}
		if obsdb.IsSessionEventKind(w.Record) {
			// thread's state log and its compaction marker (record
			// contract §4): stored as plain records under the run, counted
			// under none, and indexed by session (session_events, U12.2).
			// Never the run's own facts: the run row is not touched — its
			// last_seen does not move (a finished run stays as it ended,
			// Retain's clock does not restart), its meta takes none of the
			// record's pairs, and one landing after DeleteRun recreates no
			// row (ClickHouse's runs view skips them alike, 0023).
			if _, err := insertRecord(ctx, tx, r, w); err != nil {
				return err
			}
			if w.SessionID != "" {
				if err := insertSessionEvent(ctx, tx, r, w); err != nil {
					return err
				}
			}
			continue
		}
		u := runIDs.touch(w.RunID)
		u.applyIdentity(w, r)
		switch w.Record {
		case "event", "messages":
			inserted, err := insertRecord(ctx, tx, r, w)
			if err != nil {
				return err
			}
			// A compaction view (ADR 0028 §8) is stored but never
			// counted: the run's messages count is its growth records —
			// and a messages record without its index (Pos -1, a
			// malformed producer's) is neither, on either backend.
			if inserted && w.Reason == "" && (w.Record != "messages" || w.Pos >= 0) {
				u.count(w.Record, 1)
				if err := u.applyDurable(ctx, tx, r, w); err != nil {
					return err
				}
				u.seeSearch(r, w)
			}
		case "delta":
			if d.keepDeltasOn() {
				if _, err := insertRecord(ctx, tx, r, w); err != nil {
					return err
				}
			}
			// Counted, never kept (Q4). The count is a high-water mark
			// over the delta counter — positions 0..max seen — so a
			// retried batch cannot inflate it, the same protection the
			// row counts get from INSERT OR IGNORE.
			u.markDelta(w.Pos)
		case "request", "prompt", "tools":
			// ADR 0028's kinds: stored under their own per-run index;
			// a request also feeds the run row (applyRequest).
			if _, err := insertRecord(ctx, tx, r, w); err != nil {
				return err
			}
			if w.Record == "request" {
				u.applyRequest(r, w)
			}
		case obsdb.RecordRaw:
			// ADR 0028 §12: stored under weft.raw.index, counted under
			// none; DB.Requests attaches it to its attempt.
			if _, err := insertRecord(ctx, tx, r, w); err != nil {
				return err
			}
		case "heartbeat":
			// Never a row: no position, so the (run, kind, pos) key
			// could not hold it anyway. It moves last_seen only, which
			// applyIdentity already did.
		default:
			// A weft-adjacent kind this build does not know: stored as a
			// record under its own kind, counted under none.
			if _, err := insertRecord(ctx, tx, r, w); err != nil {
				return err
			}
		}
	}
	for _, id := range runIDs.order {
		if err := upsertRun(ctx, tx, id, runIDs.upds[id]); err != nil {
			return err
		}
		if err := updateSearch(ctx, tx, id, runIDs.upds[id]); err != nil {
			return err
		}
	}
	derived, err := deriveStock(ctx, tx, stockTraces)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	d.publish(b, runIDs, derived)
	return nil
}

// deriveStock re-derives the runs of every trace the batch brought a
// stock GenAI span of (docs/record-contract.md §9): the trace's stored
// spans — this batch's included, read inside its transaction — through
// obsdb.DeriveStockRuns, each run's row written whole (upsertDerived)
// and its error indexed for search. Re-deriving the whole trace is
// what makes a run whose spans arrive over several batches converge: a
// later batch with more chat spans, or the root arriving after its
// children, rewrites the row from everything stored. It returns the
// derived run ids, for the hub.
func deriveStock(ctx context.Context, tx *sql.Tx, traces []string) ([]string, error) {
	var ids []string
	for _, trace := range traces {
		spans, err := querySpansOn(ctx, tx, `trace_id = ? ORDER BY start_ns, span_id`, trace)
		if err != nil {
			return nil, err
		}
		for _, r := range obsdb.DeriveStockRuns(spans) {
			if err := upsertDerived(ctx, tx, r.Row); err != nil {
				return nil, err
			}
			if err := updateSearch(ctx, tx, r.Row.ID, &runUpdate{errText: r.Row.Err}); err != nil {
				return nil, err
			}
			ids = append(ids, r.Row.ID)
		}
	}
	return ids, nil
}

// upsertDerived writes a derived run's row whole: every column
// DeriveStockRuns fills is replaced by the latest derivation (it reads
// the whole stored trace, so the latest is the most complete), and the
// columns it does not derive stay at their zero defaults.
func upsertDerived(ctx context.Context, tx *sql.Tx, r obsdb.RunRow) error {
	finished := sql.NullInt64{}
	if r.Finished != nil {
		finished = sql.NullInt64{Int64: unixNS(*r.Finished), Valid: true}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO runs
		(run_id, parent_run_id, parent_call_id, trace_id, agent, provider, model, service, session_id,
		 started_ns, finished_ns, last_seen_ns, finished_ok, failed, err, steps,
		 input_tokens, output_tokens, cached_input_tokens, cache_write_tokens, reasoning_tokens, derived, root_ended)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,1)
		ON CONFLICT (run_id) DO UPDATE SET
		  parent_run_id = excluded.parent_run_id, parent_call_id = excluded.parent_call_id,
		  trace_id = excluded.trace_id, agent = excluded.agent, provider = excluded.provider,
		  model = excluded.model, service = excluded.service, session_id = excluded.session_id,
		  started_ns = excluded.started_ns, finished_ns = excluded.finished_ns,
		  last_seen_ns = excluded.last_seen_ns, finished_ok = excluded.finished_ok,
		  failed = excluded.failed, err = excluded.err, steps = excluded.steps,
		  input_tokens = excluded.input_tokens, output_tokens = excluded.output_tokens,
		  cached_input_tokens = excluded.cached_input_tokens,
		  cache_write_tokens = excluded.cache_write_tokens,
		  reasoning_tokens = excluded.reasoning_tokens, derived = 1, root_ended = 1`,
		r.ID, r.ParentRunID, r.ParentCallID, r.TraceID, r.Agent, r.Provider, r.Model, r.Service, r.SessionID,
		unixNS(r.Started), finished, unixNS(r.LastSeen),
		boolInt(r.Status == obsdb.StatusSucceeded), boolInt(r.Status == obsdb.StatusFailed), r.Err, r.Steps,
		r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.CachedInputTokens,
		r.Usage.CacheWriteTokens, r.Usage.ReasoningTokens)
	return err
}

// publish sends the batch's frames to the hub before Write returns:
// every record as ingested (deltas included), the attempt spans
// (obsdb.AttemptFrame, O17), then the run rows that changed.
func (d *DB) publish(b obsdb.Batch, ru *runUpdates, derived []string) {
	if d.hub == nil {
		return
	}
	ctx := context.Background()
	for _, r := range b.Records {
		d.hub.Publish(ctx, obsdb.RecordFrame(r))
	}
	for _, s := range b.Spans {
		if f, ok := obsdb.AttemptFrame(s); ok {
			d.hub.Publish(ctx, f)
		}
	}
	ids := append(append([]string{}, ru.order...), derived...)
	// A resumer's batch changes its parked run's row too — its
	// resumed_by (read time, query.go) — so a live list relinks it
	// (UX-E-17); a parked run not stored here publishes nothing.
	for _, id := range ru.order {
		if from := ru.upds[id].resumedFrom; from != "" && ru.upds[from] == nil && !slices.Contains(ids, from) {
			ids = append(ids, from)
		}
	}
	for _, id := range ids {
		if row, err := d.runRowByID(ctx, id); err == nil {
			d.hub.Publish(ctx, obsdb.RunFrame(row))
		}
	}
}

func insertSpan(ctx context.Context, tx *sql.Tx, s obsdb.Span, w obsdb.Weft) (bool, error) {
	attrs, err := marshalColumn(s.Attrs)
	if err != nil {
		return false, err
	}
	resource, err := marshalColumn(s.Resource)
	if err != nil {
		return false, err
	}
	events := "[]"
	if len(s.Events) > 0 {
		b, err := marshalColumn(s.Events)
		if err != nil {
			return false, err
		}
		events = string(b)
	}
	res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO spans
		(trace_id, span_id, parent_span_id, name, kind, start_ns, end_ns,
		 status_code, status_message, service, run_id, step, tool_seq, session_id,
		 attrs, resource, events)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.TraceID, s.SpanID, s.ParentSpanID, s.Name, s.Kind,
		unixNS(s.Start), unixNS(s.End),
		s.StatusCode, s.StatusMessage, s.Service,
		w.RunID, w.Step, w.ToolSeq, w.SessionID,
		string(attrs), string(resource), events)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func insertRecord(ctx context.Context, tx *sql.Tx, r obsdb.Record, w obsdb.Weft) (bool, error) {
	attrs, err := marshalColumn(r.Attrs)
	if err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO records
		(run_id, kind, pos, time_ns, trace_id, span_id, event_type, step, body, attrs)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		w.RunID, w.Record, w.Pos, unixNS(r.Time), r.TraceID, r.SpanID,
		w.EventType, w.Step, r.Body, string(attrs))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// insertSessionEvent indexes a thread record or compaction marker by
// its session (migration 0022), idempotent on (session, run, kind,
// pos) like the records table.
func insertSessionEvent(ctx context.Context, tx *sql.Tx, r obsdb.Record, w obsdb.Weft) error {
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO session_events
		(session_id, run_id, kind, pos, time_ns, event_name, public_id, content, body)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		w.SessionID, w.RunID, w.Record, w.Pos, unixNS(r.Time),
		obsdb.SessionEventName(w.Record, r.EventName, []byte(r.Body)), w.PublicID,
		attrString(r.Attrs, "weft.content"), r.Body)
	return err
}

func insertOtherLog(ctx context.Context, tx *sql.Tx, r obsdb.Record) error {
	attrs, err := marshalColumn(r.Attrs)
	if err != nil {
		return err
	}
	resource, err := marshalColumn(r.Resource)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO other_logs
		(time_ns, trace_id, span_id, severity, event_name, body, service, attrs, resource)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		unixNS(r.Time), r.TraceID, r.SpanID, r.Severity, r.EventName,
		r.Body, r.Service, string(attrs), string(resource))
	return err
}

// unixNS is a time as the *_ns columns hold it. The zero time — OTLP's
// "unknown" — is stored as 0: it is outside UnixNano's range, and the
// undefined result read back as a date in 1754.
func unixNS(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

// marshalColumn encodes an attrs, resource or events column. JSON has
// no spelling for NaN or an infinite double, yet both are legal OTLP
// attribute values — and one of them must not fail the batch it rides
// in (every other span and record lost, the exporter retrying a batch
// that can never succeed). They are stored under the names the
// protobuf JSON mapping gives them.
func marshalColumn(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	var unsupported *json.UnsupportedValueError
	if errors.As(err, &unsupported) {
		return json.Marshal(finite(v))
	}
	return b, err
}

// finite returns v with every non-finite double replaced by its name;
// the attribute value shapes of the model (S3.3), copied, never
// modified in place — the batch is the caller's.
func finite(v any) any {
	switch x := v.(type) {
	case float64:
		switch {
		case math.IsNaN(x):
			return "NaN"
		case math.IsInf(x, 1):
			return "Infinity"
		case math.IsInf(x, -1):
			return "-Infinity"
		}
	case map[string]any:
		if x == nil {
			return x
		}
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = finite(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = finite(e)
		}
		return out
	case []obsdb.SpanEvent:
		out := make([]obsdb.SpanEvent, len(x))
		for i, e := range x {
			out[i] = e
			if e.Attrs != nil {
				out[i].Attrs, _ = finite(e.Attrs).(map[string]any)
			}
		}
		return out
	}
	return v
}

// runUpdates accumulates the runs a batch touched, in touch order.
type runUpdates struct {
	upds  map[string]*runUpdate
	order []string
}

func newRunUpdates() *runUpdates {
	return &runUpdates{upds: map[string]*runUpdate{}}
}

func (ru *runUpdates) touch(runID string) *runUpdate {
	if u, ok := ru.upds[runID]; ok {
		return u
	}
	u := &runUpdate{}
	ru.upds[runID] = u
	ru.order = append(ru.order, runID)
	return u
}

// runUpdate is what one batch learned about one run. Zero-value fields
// mean "not learned this batch"; the upsert merges them into the row
// with fill-if-empty for identity, min for started, max for last-seen
// and finished.
type runUpdate struct {
	lastSeen     int64
	earliest     int64 // the earliest time seen, 0 for none
	finished     int64
	setFinished  bool
	finishedOK   bool
	rootEnded    bool // the invoke_agent span arrived (runs.root_ended)
	failed       bool
	errText      string
	traceID      string
	parentRunID  string
	parentCallID string
	agent        string
	provider     string
	model        string
	manifestHash string
	weftVersion  string
	service      string
	sessionID    string
	publicID     string
	turn         int
	setTurn      bool
	playground   bool
	experimentID string
	forkedFrom   string
	resumedFrom  string // weft.resumed_from (contract §6, U13.1)
	callerRunID  string // weft.caller.run.id (contract §2, U14)
	callerCallID string // weft.caller.call.id
	meta         map[string]string
	usage        core.Usage
	setUsage     bool
	steps        int
	setSteps     bool
	pending      int
	setPending   bool
	stopReason   string
	// stoppedBy and cancelCause (contract §6, U3.2): what ended the run,
	// from the invoke_agent span or run_finish — the larger when they
	// differ, ClickHouse's max, so both backends agree.
	stoppedBy   string
	cancelCause string
	// eventsEmitted / messagesEmitted (contract §6, U5): the root
	// span's weft.run.events / weft.run.messages, merged by max like
	// ClickHouse's SimpleAggregateFunction(max).
	eventsEmitted   int64
	messagesEmitted int64
	// errorType (contract §6, U4): the root's error.type as the row
	// reads it (obsdb.RunErrorType), the larger when they differ;
	// problems the batch's problem tokens (obsdb.SpanProblems), merged
	// into runs.problems as a set.
	errorType    string
	problems     []string
	eventCount   int64
	deltaCount   int64
	messageCount int64
	// ADR 0028 §10: instructions_hash (run_start or the invoke_agent
	// span), catalog_hash (request index 0) and request_count (the
	// request high-water mark).
	instructionsHash string
	catalogHash      string
	requestCount     int64
	// srch is what the batch taught the run's search fields (plan A7,
	// search.go); nil when nothing.
	srch *runSearch
}

// seeInstructions keeps the larger weft.instructions.hash seen — run_start's
// or the invoke_agent span's — which is ClickHouse's max() over the two
// run views, so both backends store the same value when they disagree.
func (u *runUpdate) seeInstructions(attrs map[string]any) {
	if v, ok := attrs["weft.instructions.hash"].(string); ok && v > u.instructionsHash {
		u.instructionsHash = v
	}
}

// applyRequest folds a request record into the run row: the
// high-water mark max(weft.request.index) + 1 — retry-proof, like the
// delta count, and what ClickHouse's run view computes — and request
// index 0's weft.catalog.hash. Applied whether or not the row was new:
// a duplicate changes neither.
func (u *runUpdate) applyRequest(r obsdb.Record, w obsdb.Weft) {
	if _, ok := r.Attrs["weft.request.index"]; !ok {
		return // Pos -1: a malformed producer's record counts nothing
	}
	if hw := w.Pos + 1; hw > u.requestCount {
		u.requestCount = hw
	}
	if w.Pos == 0 {
		if v, ok := r.Attrs["weft.catalog.hash"].(string); ok && v != "" {
			u.catalogHash = v
		}
	}
}

func (u *runUpdate) count(kind string, n int64) {
	switch kind {
	case "event":
		u.eventCount += n
	case "delta":
		u.deltaCount += n
	case "messages":
		u.messageCount += n
	}
}

// markDelta raises the delta high-water mark: the count is one past the
// highest delta position seen, retry-proof.
func (u *runUpdate) markDelta(pos int64) {
	if hw := pos + 1; hw > u.deltaCount {
		u.deltaCount = hw
	}
}

// applySpan folds a weft span into the update: every span moves
// last-seen and fills identity; the invoke_agent span additionally
// sets trace, finish, usage, steps, pending, stop reason and, on error,
// failed and err.
func (u *runUpdate) applySpan(s obsdb.Span, w obsdb.Weft) {
	u.applyIdentity(w, obsdb.Record{})
	u.seeStart(s.Start)
	u.seeTime(s.End)
	// A summarizer span never reaches here (Write skips it): every span
	// folded is the run's own.
	for k, v := range obsdb.MetaOf(s.Attrs) {
		u.mergeMeta(k, v)
	}
	// The identity a span carries beside the records' (contract §6): the
	// manifest hash from its attributes — so a run whose records were
	// lost, or never emitted, still names it. Fill-if-empty like every
	// identity column: a record's value, equal by construction, is as
	// good.
	if v := attrString(s.Attrs, "weft.manifest.hash"); v != "" && u.manifestHash == "" {
		u.manifestHash = v
	}
	u.problems = append(u.problems, obsdb.SpanProblems(s)...)
	if !isInvokeAgent(s) {
		return
	}
	// The version from the root span alone (contract §6's precedence):
	// its weft.version attribute, else its resource's (otel.Install
	// stamps it there) — never another span's resource. Beside the
	// run_start record's value the larger string is kept, ClickHouse's
	// max(String) over the same two values, so both backends agree
	// whatever arrives first (equal by construction: one literal,
	// TestWeftVersionMatchesRoot).
	if v := attrString(s.Attrs, "weft.version"); v != "" {
		u.weftVersion = maxString(u.weftVersion, v)
	} else if v := attrString(s.Resource, "weft.version"); v != "" {
		u.weftVersion = maxString(u.weftVersion, v)
	}
	// The root is exported only once it ended: the run is over, whatever
	// records are missing (DeriveStatus's ended; an Error status is failed).
	u.rootEnded = true
	if s.TraceID != "" {
		u.traceID = s.TraceID
	}
	if s.Service != "" {
		u.service = s.Service
	}
	u.setFinishedAt(s.End)
	if in, ok := attrInt64(s.Attrs, "gen_ai.usage.input_tokens"); ok {
		u.usage.InputTokens = in
		u.setUsage = true
	}
	if out, ok := attrInt64(s.Attrs, "gen_ai.usage.output_tokens"); ok {
		u.usage.OutputTokens = out
		u.setUsage = true
	}
	if c, ok := attrInt64(s.Attrs, "gen_ai.usage.cache_read.input_tokens"); ok {
		u.usage.CachedInputTokens = c
	}
	if c, ok := attrInt64(s.Attrs, "gen_ai.usage.cache_creation.input_tokens"); ok {
		u.usage.CacheWriteTokens = c
	}
	if c, ok := attrInt64(s.Attrs, "gen_ai.usage.reasoning.output_tokens"); ok {
		u.usage.ReasoningTokens = c
	}
	if v, ok := attrInt64(s.Attrs, "weft.run.steps"); ok {
		u.steps, u.setSteps = int(v), true
	}
	if v, ok := attrInt64(s.Attrs, "weft.run.pending"); ok {
		u.pending, u.setPending = int(v), true
	}
	if v, ok := s.Attrs["weft.run.stop_reason"].(string); ok {
		u.stopReason = v
	}
	u.stoppedBy = maxString(u.stoppedBy, attrString(s.Attrs, "weft.run.stopped_by"))
	u.cancelCause = maxString(u.cancelCause, attrString(s.Attrs, "weft.cancel.cause"))
	if n, ok := attrInt64(s.Attrs, "weft.run.events"); ok {
		u.eventsEmitted = max(u.eventsEmitted, n)
	}
	if n, ok := attrInt64(s.Attrs, "weft.run.messages"); ok {
		u.messagesEmitted = max(u.messagesEmitted, n)
	}
	if s.StatusCode == 2 { // error
		u.failed = true
		if s.StatusMessage != "" {
			u.errText = s.StatusMessage
		}
	}
	u.errorType = maxString(u.errorType, obsdb.RunErrorType(attrString(s.Attrs, "error.type")))
	u.seeInstructions(s.Attrs)
	if p, ok := s.Attrs["gen_ai.provider.name"].(string); ok && p != "" {
		u.provider = p
	}
	if m, ok := s.Attrs["gen_ai.request.model"].(string); ok && m != "" {
		u.model = m
	}
}

// applyIdentity fills the identity columns from a derived Weft (empty
// values mean absent and fill nothing) and moves last-seen for records.
func (u *runUpdate) applyIdentity(w obsdb.Weft, r obsdb.Record) {
	if w.ParentRunID != "" {
		u.parentRunID = w.ParentRunID
	}
	if w.ParentCallID != "" {
		u.parentCallID = w.ParentCallID
	}
	if w.Agent != "" {
		u.agent = w.Agent
	}
	if w.SessionID != "" {
		u.sessionID = w.SessionID
	}
	if w.PublicID != "" {
		u.publicID = w.PublicID
	}
	if w.Turn != 0 {
		u.turn, u.setTurn = w.Turn, true
	}
	if w.Playground {
		u.playground = true
	}
	if w.ExperimentID != "" {
		u.experimentID = w.ExperimentID
	}
	if w.ForkedFrom != "" {
		u.forkedFrom = w.ForkedFrom
	}
	if w.ResumedFrom != "" {
		u.resumedFrom = w.ResumedFrom
	}
	if w.CallerRunID != "" {
		u.callerRunID = w.CallerRunID
	}
	if w.CallerCallID != "" {
		u.callerCallID = w.CallerCallID
	}
	if r.Attrs != nil {
		if v, ok := r.Attrs["weft.manifest.hash"].(string); ok && v != "" {
			u.manifestHash = v
		}
		if v, ok := r.Attrs["weft.version"].(string); ok && v != "" {
			u.weftVersion = maxString(u.weftVersion, v)
		}
	}
	if r.TraceID != "" {
		u.traceID = r.TraceID
	}
	if r.Service != "" {
		u.service = r.Service
	}
	for k, v := range obsdb.MetaOf(r.Attrs) {
		u.mergeMeta(k, v)
	}
	if !r.Time.IsZero() {
		u.seeTime(r.Time)
	}
}

func (u *runUpdate) mergeMeta(k, v string) {
	if u.meta == nil {
		u.meta = map[string]string{}
	}
	u.meta[k] = v
}

// seeTime folds one record or span time into the update: last-seen is
// the latest time seen, the start the earliest — of anything, heartbeats,
// deltas and spans included, which is the ClickHouse views' min/max
// rule, so both backends date a run alike whatever reached them first.
// The zero time is "unknown" and moves neither.
func (u *runUpdate) seeTime(t time.Time) {
	ns := unixNS(t)
	if ns == 0 {
		return
	}
	if ns > u.lastSeen {
		u.lastSeen = ns
	}
	u.seeStart(t)
}

// seeStart lowers the start alone: a span's start time, whose end is
// its last-seen.
func (u *runUpdate) seeStart(t time.Time) {
	if ns := unixNS(t); ns != 0 && (u.earliest == 0 || ns < u.earliest) {
		u.earliest = ns
	}
}

func (u *runUpdate) setFinishedAt(t time.Time) {
	if t.IsZero() {
		return // an unknown time finishes nothing
	}
	ns := t.UnixNano()
	if !u.setFinished || ns > u.finished {
		u.finished, u.setFinished = ns, true
	}
}

// applyDurable handles a newly inserted event or messages record: the
// two records that carry run-level meaning are run_start (the start
// time, the earliest winning, plus provider/model from the body) and
// run_finish (the terminal fields from the body).
func (u *runUpdate) applyDurable(ctx context.Context, tx *sql.Tx, r obsdb.Record, w obsdb.Weft) error {
	switch w.EventType {
	case "run_start":
		u.seeInstructions(r.Attrs)
		var body struct {
			Model struct {
				Provider string `json:"provider"`
				Name     string `json:"name"`
			} `json:"model"`
		}
		if err := json.Unmarshal([]byte(r.Body), &body); err == nil {
			if body.Model.Provider != "" {
				u.provider = body.Model.Provider
			}
			if body.Model.Name != "" {
				u.model = body.Model.Name
			}
		}
	case "run_finish":
		u.finishedOK = true
		u.setFinishedAt(r.Time)
		var body struct {
			Usage     core.Usage `json:"usage"`
			Steps     int        `json:"steps"`
			Pending   []struct{} `json:"pending"`
			StoppedBy string     `json:"stopped_by"`
		}
		if err := json.Unmarshal([]byte(r.Body), &body); err == nil {
			u.usage, u.setUsage = body.Usage, true
			u.steps, u.setSteps = body.Steps, true
			u.pending, u.setPending = len(body.Pending), true
			u.stoppedBy = maxString(u.stoppedBy, body.StoppedBy)
		}
	}
	return nil
}

// maxString is the larger of a and b: the stopped_by / cancel_cause
// merge, ClickHouse's max over the same values ("" loses to any).
func maxString(a, b string) string {
	if b > a {
		return b
	}
	return a
}

// attrString is m[k] when it is a non-empty string, else "".
func attrString(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

func attrInt64(m map[string]any, k string) (int64, bool) {
	switch v := m[k].(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		return int64(v), true
	}
	return 0, false
}

// isInvokeAgent reports whether a span is a run's invoke_agent span —
// by its semconv operation attribute when present (the core sets it),
// by name prefix otherwise (an imported span from a pipeline that
// dropped the attribute).
func isInvokeAgent(s obsdb.Span) bool {
	if v, ok := s.Attrs["gen_ai.operation.name"].(string); ok {
		return v == "invoke_agent"
	}
	return len(s.Name) >= len("invoke_agent") && s.Name[:len("invoke_agent")] == "invoke_agent"
}

// upsertRun merges one runUpdate into the runs row: INSERT on first
// sight, UPDATE after. Identity fills only empty columns (only one
// non-empty value exists per run); started takes the minimum;
// last_seen and finished take the maximum; the terminal flags are
// sticky.
func upsertRun(ctx context.Context, tx *sql.Tx, runID string, u *runUpdate) error {
	var ex struct {
		started, lastSeen, finished sql.NullInt64
		finishedOK, failed          bool
		rootEnded                   bool
		parentRun, parentC, traceID string
		agent, provider, model      string
		manifest, version, service  string
		sessionID, publicID         string
		turn                        int
		playground                  bool
		experiment, forked, meta    string
		resumedFrom                 string
		callerRun, callerC          string
		err, stopReason             string
		stoppedBy, cancelCause      string
		errorType, problems         string
		steps, pending              int
		inTok, outTok               int64
		cachedTok, cacheWrite       int64
		reasoningTok                int64
	}
	err := tx.QueryRowContext(ctx, `SELECT started_ns, last_seen_ns, finished_ns, finished_ok, failed, root_ended,
		parent_run_id, parent_call_id, trace_id, agent, provider, model, manifest_hash, weft_version,
		service, session_id, public_id, turn, playground, experiment_id, forked_from, resumed_from, caller_run_id, caller_call_id, meta, err,
		steps, pending, stop_reason, stopped_by, cancel_cause, error_type, problems,
		input_tokens, output_tokens, cached_input_tokens, cache_write_tokens, reasoning_tokens
		FROM runs WHERE run_id = ?`, runID).
		Scan(&ex.started, &ex.lastSeen, &ex.finished, &ex.finishedOK, &ex.failed, &ex.rootEnded,
			&ex.parentRun, &ex.parentC, &ex.traceID, &ex.agent, &ex.provider, &ex.model,
			&ex.manifest, &ex.version, &ex.service, &ex.sessionID, &ex.publicID, &ex.turn,
			&ex.playground, &ex.experiment, &ex.forked, &ex.resumedFrom, &ex.callerRun, &ex.callerC, &ex.meta, &ex.err,
			&ex.steps, &ex.pending, &ex.stopReason, &ex.stoppedBy, &ex.cancelCause,
			&ex.errorType, &ex.problems,
			&ex.inTok, &ex.outTok, &ex.cachedTok, &ex.cacheWrite, &ex.reasoningTok)
	switch {
	case err == sql.ErrNoRows:
		// The earliest time seen; a reordered batch's provisional start
		// is corrected by the min below when earlier records land.
		started := u.earliest
		lastSeen := maxNS(u.lastSeen, started)
		meta := "{}"
		if len(u.meta) > 0 {
			if b, err := json.Marshal(u.meta); err == nil {
				meta = string(b)
			}
		}
		finished := sql.NullInt64{}
		if u.setFinished {
			finished = sql.NullInt64{Int64: u.finished, Valid: true}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO runs
			(run_id, parent_run_id, parent_call_id, trace_id, agent, provider, model,
			 manifest_hash, weft_version, service, session_id, public_id, turn,
			 playground, experiment_id, forked_from, resumed_from, caller_run_id, caller_call_id, meta,
			 started_ns, finished_ns, last_seen_ns, finished_ok, failed, root_ended, err,
			 steps, pending, stop_reason, stopped_by, cancel_cause,
			 input_tokens, output_tokens, cached_input_tokens, cache_write_tokens, reasoning_tokens,
			 event_count, delta_count, message_count,
			 instructions_hash, catalog_hash, request_count, events_emitted, messages_emitted,
			 error_type, problems)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			runID, u.parentRunID, u.parentCallID, u.traceID, u.agent, u.provider, u.model,
			u.manifestHash, u.weftVersion, u.service, u.sessionID, u.publicID, u.turn,
			boolInt(u.playground), u.experimentID, u.forkedFrom, u.resumedFrom, u.callerRunID, u.callerCallID, meta,
			started, finished, lastSeen, boolInt(u.finishedOK), boolInt(u.failed), boolInt(u.rootEnded), u.errText,
			u.steps, u.pending, u.stopReason, u.stoppedBy, u.cancelCause,
			u.usage.InputTokens, u.usage.OutputTokens, u.usage.CachedInputTokens,
			u.usage.CacheWriteTokens, u.usage.ReasoningTokens,
			u.eventCount, u.deltaCount, u.messageCount,
			u.instructionsHash, u.catalogHash, u.requestCount, u.eventsEmitted, u.messagesEmitted,
			u.errorType, mergeProblems("", u.problems))
		return err
	case err != nil:
		return err
	}

	// UPDATE: merge the update into what the row holds.
	started := ex.started.Int64
	if u.earliest != 0 && (started == 0 || u.earliest < started) {
		started = u.earliest // 0 was "no time seen yet", never an instant
	}
	lastSeen := maxNS(u.lastSeen, ex.lastSeen.Int64)
	finished := ex.finished
	if u.setFinished && (!finished.Valid || u.finished > finished.Int64) {
		finished = sql.NullInt64{Int64: u.finished, Valid: true}
	}
	finishedOK := ex.finishedOK || u.finishedOK
	failed := ex.failed || u.failed
	rootEnded := ex.rootEnded || u.rootEnded
	usage := core.Usage{
		InputTokens: ex.inTok, OutputTokens: ex.outTok,
		CachedInputTokens: ex.cachedTok, CacheWriteTokens: ex.cacheWrite,
		ReasoningTokens: ex.reasoningTok,
	}
	if u.setUsage {
		usage = u.usage
	}
	steps, pending, stopReason := ex.steps, ex.pending, ex.stopReason
	if u.setSteps {
		steps = u.steps
	}
	if u.setPending {
		pending = u.pending
	}
	if u.stopReason != "" {
		stopReason = u.stopReason
	}
	_, err = tx.ExecContext(ctx, `UPDATE runs SET
		parent_run_id = ?, parent_call_id = ?, trace_id = ?, agent = ?, provider = ?, model = ?,
		manifest_hash = ?, weft_version = ?, service = ?, session_id = ?, public_id = ?, turn = ?,
		playground = ?, experiment_id = ?, forked_from = ?, resumed_from = ?,
		caller_run_id = ?, caller_call_id = ?, meta = ?,
		started_ns = ?, finished_ns = ?, last_seen_ns = ?, finished_ok = ?, failed = ?, root_ended = ?, err = ?,
		steps = ?, pending = ?, stop_reason = ?, stopped_by = ?, cancel_cause = ?,
		input_tokens = ?, output_tokens = ?, cached_input_tokens = ?, cache_write_tokens = ?, reasoning_tokens = ?,
		event_count = event_count + ?, delta_count = MAX(delta_count, ?), message_count = message_count + ?,
		instructions_hash = MAX(instructions_hash, ?),
		catalog_hash = CASE WHEN catalog_hash = '' THEN ? ELSE catalog_hash END,
		request_count = MAX(request_count, ?),
		events_emitted = MAX(events_emitted, ?), messages_emitted = MAX(messages_emitted, ?),
		error_type = ?, problems = ?
		WHERE run_id = ?`,
		coalesce(ex.parentRun, u.parentRunID), coalesce(ex.parentC, u.parentCallID),
		coalesce(ex.traceID, u.traceID), coalesce(ex.agent, u.agent),
		coalesce(ex.provider, u.provider), coalesce(ex.model, u.model),
		coalesce(ex.manifest, u.manifestHash), maxString(ex.version, u.weftVersion),
		coalesce(ex.service, u.service), coalesce(ex.sessionID, u.sessionID),
		coalesce(ex.publicID, u.publicID), turnOr(ex.turn, u.turn, u.setTurn),
		boolInt(ex.playground || u.playground),
		coalesce(ex.experiment, u.experimentID), coalesce(ex.forked, u.forkedFrom),
		maxString(ex.resumedFrom, u.resumedFrom),
		coalesce(ex.callerRun, u.callerRunID), coalesce(ex.callerC, u.callerCallID),
		mergeMetaJSON(ex.meta, u.meta),
		started, finished, lastSeen, boolInt(finishedOK), boolInt(failed), boolInt(rootEnded), coalesce(ex.err, u.errText),
		steps, pending, stopReason, maxString(ex.stoppedBy, u.stoppedBy), maxString(ex.cancelCause, u.cancelCause),
		usage.InputTokens, usage.OutputTokens, usage.CachedInputTokens,
		usage.CacheWriteTokens, usage.ReasoningTokens,
		u.eventCount, u.deltaCount, u.messageCount,
		u.instructionsHash, u.catalogHash, u.requestCount,
		u.eventsEmitted, u.messagesEmitted,
		maxString(ex.errorType, u.errorType), mergeProblems(ex.problems, u.problems),
		runID)
	return err
}

// mergeProblems is runs.problems after a batch (contract §6, U4): the
// stored JSON array's tokens and the batch's, as one sorted set — so a
// span stored twice, or a retried batch, adds nothing. A malformed
// stored value is replaced by the batch's set.
func mergeProblems(stored string, add []string) string {
	var set []string
	if stored != "" {
		_ = json.Unmarshal([]byte(stored), &set)
	}
	if len(add) == 0 && stored != "" && set != nil {
		return stored
	}
	set = append(set, add...)
	slices.Sort(set)
	set = slices.Compact(set)
	if set == nil {
		set = []string{}
	}
	b, _ := json.Marshal(set)
	return string(b)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func maxNS(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func coalesce(existing, update string) string {
	if existing != "" {
		return existing
	}
	return update
}

func turnOr(existing, update int, set bool) int {
	if set {
		return update
	}
	return existing
}

func mergeMetaJSON(existing string, add map[string]string) string {
	if len(add) == 0 {
		return existing
	}
	m := map[string]string{}
	if existing != "" && existing != "{}" {
		_ = json.Unmarshal([]byte(existing), &m)
	}
	for k, v := range add {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		return existing
	}
	return string(b)
}

// runRowByID reads one run row for hub frames (after the commit).
func (d *DB) runRowByID(ctx context.Context, runID string) (obsdb.RunRow, error) {
	rows, err := d.queryRunRows(ctx, `run_id = ?`, []any{runID}, time.Now())
	if err != nil {
		return obsdb.RunRow{}, err
	}
	if len(rows) == 0 {
		return obsdb.RunRow{}, fmt.Errorf("%w: %s", obsdb.ErrNotFound, runID)
	}
	return rows[0], nil
}
