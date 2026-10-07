package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/weftgo/weft"
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
// steps and, on error, failed and err. A run first seen through a later
// record (a reordered batch) gets a provisional started_ns, corrected
// when the earlier rows land.
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

	for _, s := range b.Spans {
		w := obsdb.DeriveSpan(s)
		if _, err := insertSpan(ctx, tx, s, w); err != nil {
			return err
		}
		if w.RunID != "" {
			runIDs.touch(w.RunID).applySpan(s, w)
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
			if err := insertOtherLog(ctx, tx, r); err != nil {
				return err
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
			if inserted {
				u.count(w.Record, 1)
				if err := u.applyDurable(ctx, tx, r, w); err != nil {
					return err
				}
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
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	d.publish(b, runIDs)
	return nil
}

// publish sends the batch's frames to the hub before Write returns:
// every record as ingested (deltas included), then the run rows that
// changed.
func (d *DB) publish(b obsdb.Batch, ru *runUpdates) {
	if d.hub == nil {
		return
	}
	ctx := context.Background()
	for _, r := range b.Records {
		d.hub.Publish(ctx, obsdb.RecordFrame(r))
	}
	for _, id := range ru.order {
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
	meta         map[string]string
	usage        weft.Usage
	setUsage     bool
	steps        int
	setSteps     bool
	pending      int
	setPending   bool
	stopReason   string
	eventCount   int64
	deltaCount   int64
	messageCount int64
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
	if s.Attrs != nil {
		for k, v := range obsdb.MetaOf(s.Attrs) {
			u.mergeMeta(k, v)
		}
	}
	if !isInvokeAgent(s) {
		return
	}
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
	if s.StatusCode == 2 { // error
		u.failed = true
		if s.StatusMessage != "" {
			u.errText = s.StatusMessage
		}
	}
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
	if r.Attrs != nil {
		if v, ok := r.Attrs["weft.manifest.hash"].(string); ok && v != "" {
			u.manifestHash = v
		}
		if v, ok := r.Attrs["weft.version"].(string); ok && v != "" {
			u.weftVersion = v
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
			Usage   weft.Usage `json:"usage"`
			Steps   int        `json:"steps"`
			Pending []struct{} `json:"pending"`
		}
		if err := json.Unmarshal([]byte(r.Body), &body); err == nil {
			u.usage, u.setUsage = body.Usage, true
			u.steps, u.setSteps = body.Steps, true
			u.pending, u.setPending = len(body.Pending), true
		}
	}
	return nil
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
		parentRun, parentC, traceID string
		agent, provider, model      string
		manifest, version, service  string
		sessionID, publicID         string
		turn                        int
		playground                  bool
		experiment, forked, meta    string
		err, stopReason             string
		steps, pending              int
		inTok, outTok               int64
		cachedTok, cacheWrite       int64
		reasoningTok                int64
	}
	err := tx.QueryRowContext(ctx, `SELECT started_ns, last_seen_ns, finished_ns, finished_ok, failed,
		parent_run_id, parent_call_id, trace_id, agent, provider, model, manifest_hash, weft_version,
		service, session_id, public_id, turn, playground, experiment_id, forked_from, meta, err,
		steps, pending, stop_reason,
		input_tokens, output_tokens, cached_input_tokens, cache_write_tokens, reasoning_tokens
		FROM runs WHERE run_id = ?`, runID).
		Scan(&ex.started, &ex.lastSeen, &ex.finished, &ex.finishedOK, &ex.failed,
			&ex.parentRun, &ex.parentC, &ex.traceID, &ex.agent, &ex.provider, &ex.model,
			&ex.manifest, &ex.version, &ex.service, &ex.sessionID, &ex.publicID, &ex.turn,
			&ex.playground, &ex.experiment, &ex.forked, &ex.meta, &ex.err,
			&ex.steps, &ex.pending, &ex.stopReason,
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
			 playground, experiment_id, forked_from, meta,
			 started_ns, finished_ns, last_seen_ns, finished_ok, failed, err,
			 steps, pending, stop_reason,
			 input_tokens, output_tokens, cached_input_tokens, cache_write_tokens, reasoning_tokens,
			 event_count, delta_count, message_count)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			runID, u.parentRunID, u.parentCallID, u.traceID, u.agent, u.provider, u.model,
			u.manifestHash, u.weftVersion, u.service, u.sessionID, u.publicID, u.turn,
			boolInt(u.playground), u.experimentID, u.forkedFrom, meta,
			started, finished, lastSeen, boolInt(u.finishedOK), boolInt(u.failed), u.errText,
			u.steps, u.pending, u.stopReason,
			u.usage.InputTokens, u.usage.OutputTokens, u.usage.CachedInputTokens,
			u.usage.CacheWriteTokens, u.usage.ReasoningTokens,
			u.eventCount, u.deltaCount, u.messageCount)
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
	usage := weft.Usage{
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
		playground = ?, experiment_id = ?, forked_from = ?, meta = ?,
		started_ns = ?, finished_ns = ?, last_seen_ns = ?, finished_ok = ?, failed = ?, err = ?,
		steps = ?, pending = ?, stop_reason = ?,
		input_tokens = ?, output_tokens = ?, cached_input_tokens = ?, cache_write_tokens = ?, reasoning_tokens = ?,
		event_count = event_count + ?, delta_count = MAX(delta_count, ?), message_count = message_count + ?
		WHERE run_id = ?`,
		coalesce(ex.parentRun, u.parentRunID), coalesce(ex.parentC, u.parentCallID),
		coalesce(ex.traceID, u.traceID), coalesce(ex.agent, u.agent),
		coalesce(ex.provider, u.provider), coalesce(ex.model, u.model),
		coalesce(ex.manifest, u.manifestHash), coalesce(ex.version, u.weftVersion),
		coalesce(ex.service, u.service), coalesce(ex.sessionID, u.sessionID),
		coalesce(ex.publicID, u.publicID), turnOr(ex.turn, u.turn, u.setTurn),
		boolInt(ex.playground || u.playground),
		coalesce(ex.experiment, u.experimentID), coalesce(ex.forked, u.forkedFrom),
		mergeMetaJSON(ex.meta, u.meta),
		started, finished, lastSeen, boolInt(finishedOK), boolInt(failed), coalesce(ex.err, u.errText),
		steps, pending, stopReason,
		usage.InputTokens, usage.OutputTokens, usage.CachedInputTokens,
		usage.CacheWriteTokens, usage.ReasoningTokens,
		u.eventCount, u.deltaCount, u.messageCount,
		runID)
	return err
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
