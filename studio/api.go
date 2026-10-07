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

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/version"
)

// The JSON API (S4.2/S4.3). Every response is application/json; errors
// are {"error": {"code", "message"}} with the codes not_found,
// bad_request, unauthorized, forbidden, conflict, unsupported,
// unavailable and internal. The DTOs below are the contract, written
// by hand (ADR 0018: obsdb's rows carry no tags of their own; events,
// usage and model info marshal through the core's own codecs).
// web/src/lib/api.ts mirrors these types, and testdata/api/*.golden.json
// pins the bytes on both sides.

// Events paging (ADR 0018 §8): constants for the endpoint.
const (
	eventsDefaultLimit = 500
	eventsMaxLimit     = 5000
)

func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	writeJSON(w, r, status, struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{code, msg}})
}

// maxBody bounds a JSON request body on the API (the playground,
// debugger, fixture, experiment and panel-token routes): 4 MiB — a
// run command carries an input and transcript edits, nothing larger.
// Above it the route answers 413. Ingest has its own limit (S4.4).
const maxBody = 4 << 20

// decodeBody reads one JSON body into v under maxBody, answering the
// client itself on failure: 413 for a body over the limit, 400 for
// anything that does not decode. what names the body in the message.
func decodeBody(w http.ResponseWriter, r *http.Request, what string, v any) bool {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(v)
	if err == nil {
		return true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, r, http.StatusRequestEntityTooLarge, "bad_request",
			what+": larger than the 4 MiB limit")
		return false
	}
	badRequest(w, r, what+": "+err.Error())
	return false
}

// notFound and badRequest keep call sites to one line each.
func notFound(w http.ResponseWriter, r *http.Request, msg string) {
	writeError(w, r, http.StatusNotFound, "not_found", msg)
}

func badRequest(w http.ResponseWriter, r *http.Request, msg string) {
	writeError(w, r, http.StatusBadRequest, "bad_request", msg)
}

// dbError maps an obsdb error onto the API's error codes: unknown ids
// are 404, everything else is a 500 that names the failure (loud over
// silent, ADR 0010 §2.5's rule, carried over). A panel token lives in
// a page, so its holder gets the code and the operation, never the
// database's own error text (paths, SQL, hosts).
func dbError(w http.ResponseWriter, r *http.Request, noun, id string, err error) {
	switch {
	case errors.Is(err, obsdb.ErrNotFound):
		notFound(w, r, "no "+noun+" "+id)
	case idFrom(r).panel != nil:
		writeError(w, r, http.StatusInternalServerError, "internal",
			"read "+noun+" "+id+" failed")
	default:
		writeError(w, r, http.StatusInternalServerError, "internal",
			"read "+noun+" "+id+": "+err.Error())
	}
}

// ── DTOs (S4.3) ────────────────────────────────────────────────────

// runRow is every list and detail: the identity chain, timing, the
// derived status, usage and the counts (S4.3's RunRow).
type runRow struct {
	ID           string            `json:"id"`
	ParentRunID  string            `json:"parent_run_id"`
	ParentCallID string            `json:"parent_call_id"`
	TraceID      string            `json:"trace_id"`
	Agent        string            `json:"agent"`
	Model        core.ModelInfo    `json:"model"`
	ManifestHash string            `json:"manifest_hash"`
	WeftVersion  string            `json:"weft_version"`
	Service      string            `json:"service"`
	SessionID    string            `json:"session_id"`
	PublicID     string            `json:"public_id"`
	Turn         int               `json:"turn"`
	Playground   bool              `json:"playground"`
	ExperimentID string            `json:"experiment_id"`
	ForkedFrom   string            `json:"forked_from"`
	Meta         map[string]string `json:"meta"`
	Started      time.Time         `json:"started"`
	Finished     *time.Time        `json:"finished"`
	LastSeen     time.Time         `json:"last_seen"`
	Status       string            `json:"status"` // derived: a stale running row reads "interrupted"
	Err          string            `json:"err"`
	Steps        int               `json:"steps"`
	Pending      int               `json:"pending"`
	StopReason   string            `json:"stop_reason"`
	Usage        core.Usage        `json:"usage"`
	EventCount   int64             `json:"event_count"`
	MessageCount int64             `json:"message_count"`
	// The request record's run columns (ADR 0028 §10):
	// instructions_hash "" means the run was written before the record
	// existed, and requests_badge then reads "not_recorded" (absent
	// otherwise); request_count 0 with an instructions_hash means the
	// run made no model call — no badge.
	InstructionsHash string `json:"instructions_hash"`
	CatalogHash      string `json:"catalog_hash"`
	RequestCount     int64  `json:"request_count"`
	RequestsBadge    string `json:"requests_badge,omitempty"`
}

// row maps an obsdb run row onto the DTO. The status is the
// database's derived one: a crash-orphaned running row reads
// interrupted and is shown, never hidden (A1).
func row(rec obsdb.RunRow) runRow {
	out := runRow{
		ID:           rec.ID,
		ParentRunID:  rec.ParentRunID,
		ParentCallID: rec.ParentCallID,
		TraceID:      rec.TraceID,
		Agent:        rec.Agent,
		Model:        core.ModelInfo{Provider: rec.Provider, Name: rec.Model},
		ManifestHash: rec.ManifestHash,
		WeftVersion:  rec.WeftVersion,
		Service:      rec.Service,
		SessionID:    rec.SessionID,
		PublicID:     rec.PublicID,
		Turn:         rec.Turn,
		Playground:   rec.Playground,
		ExperimentID: rec.ExperimentID,
		ForkedFrom:   rec.ForkedFrom,
		Meta:         rec.Meta,
		Started:      rec.Started,
		LastSeen:     rec.LastSeen,
		Status:       string(rec.Status),
		Err:          rec.Err,
		Steps:        rec.Steps,
		Pending:      rec.Pending,
		StopReason:   rec.StopReason,
		Usage:        rec.Usage,
		EventCount:   rec.EventCount,
		MessageCount: rec.MessageCount,

		InstructionsHash: rec.InstructionsHash,
		CatalogHash:      rec.CatalogHash,
		RequestCount:     rec.RequestCount,
		RequestsBadge:    string(rec.RequestsHole()),
	}
	if out.Meta == nil {
		out.Meta = map[string]string{}
	}
	if rec.Finished != nil {
		f := *rec.Finished
		out.Finished = &f
	}
	return out
}

type runsPage struct {
	Total      int        `json:"total"`
	Runs       []runRow   `json:"runs"`
	NextBefore *time.Time `json:"next_before"`
	// NextBeforeID is the cursor's second half: with next_before, the
	// pair (started, id) resumes exactly inside a group of runs sharing
	// one started — a page rides a tie along only up to obsdb.MaxTies.
	// Null on the last page, like next_before.
	NextBeforeID *string `json:"next_before_id"`
}

// runDoc is GET /api/runs/{id}: the row and the subagent children
// (obsdb's RunDetail). Events are deliberately not here — they are
// paged (ADR 0018 §8); the UI loads each child's events lazily.
//
// Holes are the run's own, from ADR 0028 §11's closed table, each with
// its reason and, where one exists, its fix (runHoles) — what the run
// header badges; [] when the run has none.
type runDoc struct {
	runRow
	Children []runRow   `json:"children"`
	Holes    []stepHole `json:"holes"`
}

// posEvent is one event in a paged stream: its 0-based position and
// time beside the event itself (S4.3's EventsPage entry). Attrs is the
// stored record's weft.content.* attributes and nothing else — what
// the destination's chain did to the content (ADR 0028 §11: stripped,
// a cap's truncated_bytes); absent when it did nothing.
type posEvent struct {
	Pos   int64           `json:"pos"`
	Time  time.Time       `json:"time"`
	Event json.RawMessage `json:"event"`
	Attrs map[string]any  `json:"attrs,omitempty"`
}

// Content attribute keys a posEvent and a live record frame carry.
const (
	attrContent   = "weft.content"
	attrTruncated = "weft.content.truncated_bytes"
)

// contentFull is the core's weft.content mark on an event emitted with
// its content: the content as emitted, no hole — the one value the
// attrs object leaves out ("none", the core's own capture-off mark,
// and "stripped", a content-off chain's, are kept).
const contentFull = "full"

// contentAttrs is the attrs object of a stored event: nil when the
// chain left the content as emitted.
func contentAttrs(content string, truncated int64) map[string]any {
	if content == contentFull {
		content = ""
	}
	if content == "" && truncated <= 0 {
		return nil
	}
	out := map[string]any{}
	if content != "" {
		out[attrContent] = content
	}
	if truncated > 0 {
		out[attrTruncated] = truncated
	}
	return out
}

// contentAttrsOf limits an ingested record's attributes to the
// weft.content.* keys (the same object posEvent carries): a number
// arrives as int64, float64 or a numeric string and leaves as an
// integer; nil when there are none.
func contentAttrsOf(attrs map[string]any) map[string]any {
	var out map[string]any
	for k, v := range attrs {
		if k != attrContent && !strings.HasPrefix(k, attrContent+".") || k == attrContent && v == contentFull {
			continue
		}
		if k == attrTruncated {
			var n int64
			switch x := v.(type) {
			case int64:
				n = x
			case float64:
				n = int64(x)
			case string:
				n, _ = strconv.ParseInt(x, 10, 64)
			}
			if n <= 0 {
				continue
			}
			v = n
		}
		if out == nil {
			out = map[string]any{}
		}
		out[k] = v
	}
	return out
}

// posEventOf is the API's event row of a stored event.
func posEventOf(pe obsdb.PosEvent) posEvent {
	return posEvent{Pos: pe.Pos, Time: pe.Time, Event: pe.Event, Attrs: contentAttrs(pe.Content, pe.TruncatedBytes)}
}

type eventsPage struct {
	// Events is the page's slice of the run's durable stream, in pos
	// order. Empty (not null) past the end.
	Events []posEvent `json:"events"`
	// NextAfter is the first position of the next page when more
	// buffered events remain; else null.
	NextAfter *int64 `json:"next_after"`
	// Done is true whenever the run reads terminal at derivation time
	// (succeeded, failed, or interrupted — never running), even on a
	// partial page with next_after set: it says the poll cadence can
	// stop, not that every event has been returned. next_after is the
	// paging cursor — follow it (not done) until it reads null.
	Done bool `json:"done"`
	// Gaps are durable positions missing below the high-water mark: a
	// lost batch, never a delta (D3) — the fold's input stays
	// contiguous unless something was dropped in transit.
	Gaps []int64 `json:"gaps"`
}

// transcriptBatch is one messages record: the body at its index, the
// step it joined and whether it is the run's input — all three as
// obsdb stored them (ADR 0028 §8), never derived from the order of the
// batches. Step is -1 when the record carried no weft.step.index (a
// ClickHouse row written before migration 0004, a producer that never
// stamped it); such a batch says so with Badge "not_recorded", and a
// client that places it anyway infers the step and marks the result
// derived (ADR 0028 §11's closed table). A batch whose input flag the
// backend inferred carries Badge "derived".
type transcriptBatch struct {
	Index    int64           `json:"index"`
	Step     int             `json:"step"`
	Input    bool            `json:"input"`
	Badge    string          `json:"badge,omitempty"`
	Messages json.RawMessage `json:"messages"`
}

type transcript struct {
	Batches []transcriptBatch `json:"batches"`
}

// spanStatus names an OTLP status code the way a reader expects it.
func spanStatus(code int) string {
	switch code {
	case 1:
		return "ok"
	case 2:
		return "error"
	default:
		return "unset"
	}
}

// spanKindName names an OTLP span kind the way a reader expects it
// (S4.3's example writes "kind": "client", not the wire int) — the
// same mapping spanStatus makes for the status code.
func spanKindName(kind int) string {
	switch kind {
	case 1:
		return "internal"
	case 2:
		return "server"
	case 3:
		return "client"
	case 4:
		return "producer"
	case 5:
		return "consumer"
	default:
		return "unspecified"
	}
}

type spanDTO struct {
	TraceID       string         `json:"trace_id"`
	SpanID        string         `json:"span_id"`
	ParentSpanID  string         `json:"parent_span_id"`
	Name          string         `json:"name"`
	Kind          string         `json:"kind"`
	Start         time.Time      `json:"start"`
	End           time.Time      `json:"end"`
	Status        string         `json:"status"`
	StatusMessage string         `json:"status_message"`
	Service       string         `json:"service"`
	Attrs         map[string]any `json:"attrs"`
	Events        []spanEventDTO `json:"events"`
}

type spanEventDTO struct {
	Time  time.Time      `json:"time"`
	Name  string         `json:"name"`
	Attrs map[string]any `json:"attrs"`
}

type spansDoc struct {
	Spans []spanDTO `json:"spans"`
}

func spans(in []obsdb.Span) spansDoc {
	out := spansDoc{Spans: make([]spanDTO, 0, len(in))}
	for _, s := range in {
		dto := spanDTO{
			TraceID: s.TraceID, SpanID: s.SpanID, ParentSpanID: s.ParentSpanID,
			Name: s.Name, Kind: spanKindName(s.Kind), Start: s.Start, End: s.End,
			Status: spanStatus(s.StatusCode), StatusMessage: s.StatusMessage,
			Service: s.Service, Attrs: s.Attrs, Events: []spanEventDTO{},
		}
		if dto.Attrs == nil {
			dto.Attrs = map[string]any{}
		}
		for _, ev := range s.Events {
			dto.Events = append(dto.Events, spanEventDTO{Time: ev.Time, Name: ev.Name, Attrs: ev.Attrs})
		}
		out.Spans = append(out.Spans, dto)
	}
	return out
}

// sessionRow is one thread: turns, usage, first/last seen, the newest
// turn's status (S4.3).
type sessionRow struct {
	ID        string     `json:"id"`
	PublicID  string     `json:"public_id"`
	Agent     string     `json:"agent"`
	Turns     int        `json:"turns"`
	FirstSeen time.Time  `json:"first_seen"`
	LastSeen  time.Time  `json:"last_seen"`
	Status    string     `json:"status"`
	Usage     core.Usage `json:"usage"`
}

func sessRow(r obsdb.SessionRow) sessionRow {
	return sessionRow{
		ID: r.ID, PublicID: r.PublicID, Agent: r.Agent, Turns: r.Turns,
		FirstSeen: r.FirstSeen, LastSeen: r.LastSeen,
		Status: string(r.Status), Usage: r.Usage,
	}
}

type sessionsPage struct {
	Total      int          `json:"total"`
	Sessions   []sessionRow `json:"sessions"`
	NextBefore *time.Time   `json:"next_before"`
	// NextBeforeID pairs with next_before as in runsPage: (last_seen, id).
	NextBeforeID *string `json:"next_before_id"`
}

// sessionDoc is GET /api/sessions/{id}: the row and its top-level
// turns in order; experiments hang off runs via forked_from.
type sessionDoc struct {
	sessionRow
	Runs []runRow `json:"runs"`
}

// publicResolution is GET /api/public/{public_id}: the public id is
// the browser-safe handle; this is where it becomes a session.
type publicResolution struct {
	SessionID string `json:"session_id"`
}

// ── Handlers ───────────────────────────────────────────────────────

// serveMeta answers api/meta (S4.3): versions, the DB kind, the
// title, whether a manifest is present, whether ingest is open
// without a token (S4.4 says meta must say so), the derived-interval
// clock, and the capabilities the registered route groups provide
// (computed, never hard-coded) plus any the backing server declared.
func (s *Server) serveMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, struct {
		WeftVersion        string   `json:"weft_version"`
		StudioVersion      string   `json:"studio_version"`
		DB                 string   `json:"db"`
		Title              string   `json:"title"`
		HasManifest        bool     `json:"has_manifest"`
		IngestOpen         bool     `json:"ingest_open"`
		InterruptedAfterMs int64    `json:"interrupted_after_ms"`
		Capabilities       []string `json:"capabilities"`
		// DebugScope says what the debugger's write verbs (breakpoints,
		// steer) can act on — the runtime-started runs only. The app's
		// own turns are viewer-only (D7, PQ7); meta says so plainly
		// (WEFT-DEVTOOLS §8.5 item 6).
		DebugScope string `json:"debug_scope,omitempty"`
	}{
		WeftVersion:        weftVersion(),
		StudioVersion:      Version,
		DB:                 dbKind(s.db),
		Title:              s.title,
		HasManifest:        len(s.manifest) > 0,
		IngestOpen:         !s.noIngest && s.ingestToken == "",
		InterruptedAfterMs: obsdb.InterruptedAfter.Milliseconds(),
		Capabilities:       s.capabilityList(),
		DebugScope:         s.debugScope(),
	})
}

// debugScope names what the debugger's write verbs may act on: the
// runtime-started runs, only when those verbs are registered.
func (s *Server) debugScope() string {
	for _, g := range s.groups {
		if g.capability == "breakpoints" {
			return "runtime-started runs"
		}
	}
	return ""
}

// serveRuns answers api/runs (S4.2): agent, status, session,
// public_id, playground, parent ("" top-level only, "*" all, or a run
// id for its children), tag.<k>=<v> metadata matches, before (the
// RFC 3339 paging cursor on started) with before_id (the cursor's run
// id: exact inside a tie) and limit. next_before / next_before_id are
// the next page's before / before_id, null on the last page.
func (s *Server) serveRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := obsdb.RunQuery{
		Agent:       q.Get("agent"),
		Status:      obsdb.Status(q.Get("status")),
		SessionID:   q.Get("session"),
		PublicID:    q.Get("public_id"),
		ParentRunID: parentParam(q),
		Meta:        tagParams(q),
	}
	if v := q.Get("playground"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			badRequest(w, r, "playground must be true or false")
			return
		}
		query.Playground = &b
	}
	if v := q.Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			badRequest(w, r, "before must be RFC 3339: "+err.Error())
			return
		}
		query.Before = t
		query.BeforeID = q.Get("before_id")
	}
	if !scopeRunsQuery(w, r, &query, q.Get("public_id")) {
		return
	}
	limit, ok := limitParam(w, r, q)
	if !ok {
		return
	}
	query.Limit = limit

	page, err := s.db.Runs(r.Context(), query)
	if err != nil {
		dbError(w, r, "runs", "", err)
		return
	}
	out := runsPage{Total: page.Total, Runs: make([]runRow, 0, len(page.Runs))}
	for _, rec := range page.Runs {
		out.Runs = append(out.Runs, row(rec))
	}
	// The cursor is the database's own: it knows whether the page was
	// cut at its limit. Counting rows here cannot — a page may exceed
	// the limit by the runs tied on the cursor time (they ride along so
	// the time cursor does not skip them), and a count that expects
	// exactly limit rows would read such a page as the last one.
	if page.NextBefore != nil {
		next := *page.NextBefore
		out.NextBefore = &next
		if page.NextBeforeID != "" {
			id := page.NextBeforeID
			out.NextBeforeID = &id
		}
	}
	writeJSON(w, r, http.StatusOK, out)
}

// parentParam resolves the parent query param onto RunQuery.ParentRunID,
// whose zero value already means "top-level only" (obsdb.RunQuery):
// absent → top-level; "*" → every run; a concrete id → that run's
// children.
func parentParam(q map[string][]string) string {
	vs, ok := q["parent"]
	if !ok || len(vs) == 0 || vs[0] == "" {
		return ""
	}
	if vs[0] == "*" {
		return "*"
	}
	return vs[0]
}

// tagParams collects the tag.<k>=<v> metadata matches (the row's
// metadata — what the store era called tags; core.Metadata and
// thread's session keys land here).
func tagParams(q map[string][]string) map[string]string {
	var meta map[string]string
	for k, vs := range q {
		if name, ok := strings.CutPrefix(k, "tag."); ok && name != "" {
			if meta == nil {
				meta = map[string]string{}
			}
			meta[name] = vs[0]
		}
	}
	return meta
}

// limitParam parses the shared limit parameter: a non-negative
// integer, 0 for the default.
func limitParam(w http.ResponseWriter, r *http.Request, q map[string][]string) (int, bool) {
	vs, ok := q["limit"]
	if !ok || len(vs) == 0 || vs[0] == "" {
		return 0, true
	}
	n, err := strconv.Atoi(vs[0])
	if err != nil || n < 0 {
		badRequest(w, r, "limit must be a non-negative integer")
		return 0, false
	}
	return n, true
}

// serveRunRoutes dispatches the /api/runs/ subtree: run documents and
// the sub-routes the groups registered (addRunRoute, addRunItemRoute)
// — paged events, transcript, spans, requests, tools and one step.
// Everything after /api/runs/ is the run id, slashes included — a
// subagent's child id is <parent>/<step>/<callID> (the core's
// childRunID), and its page is a full run page (B7). An unknown id still answers 404 — from
// the database, naming the run.
func (s *Server) serveRunRoutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	if rest == "" {
		notFound(w, r, "no such api route "+r.URL.Path)
		return
	}
	for _, sub := range s.runRoutes {
		if sub.item != nil {
			i := strings.LastIndexByte(rest, '/')
			if i < 0 {
				continue
			}
			if id, ok := strings.CutSuffix(rest[:i], "/"+sub.ext); ok && id != "" && rest[i+1:] != "" {
				sub.item(w, r, id, rest[i+1:])
				return
			}
			continue
		}
		if id, ok := strings.CutSuffix(rest, "/"+sub.ext); ok && id != "" {
			sub.serve(w, r, id)
			return
		}
	}
	s.serveRun(w, r, rest)
}

// serveRun answers api/runs/{id}: the row and the child runs (obsdb's
// Run detail carries both). Events are deliberately not here — they
// are paged (ADR 0018 §8).
func (s *Server) serveRun(w http.ResponseWriter, r *http.Request, id string) {
	if !s.scopeRunID(w, r, id) {
		return
	}
	det, err := s.db.Run(r.Context(), id)
	if err != nil {
		dbError(w, r, "run", id, err)
		return
	}
	doc := runDoc{runRow: row(det.RunRow), Children: make([]runRow, 0, len(det.Children))}
	for _, kid := range det.Children {
		doc.Children = append(doc.Children, row(kid))
	}
	holes, err := s.runHoles(r.Context(), det.RunRow)
	if err != nil {
		dbError(w, r, "run", id, err)
		return
	}
	doc.Holes = holes
	writeJSON(w, r, http.StatusOK, doc)
}

// runHoles is the run's own holes, from what obsdb can tell about the
// whole run (ADR 0028 §11; plan A3):
//
//   - not_recorded: RequestsHole — written before the request record;
//   - interrupted: the derived status (obsdb.InterruptedAfter);
//   - derived: no stored event, yet the run is no longer running and
//     never finished — its row was built from its spans alone;
//   - stripped: its events came through a content-off chain, or the
//     core captured none (the run_start's weft.content: stripped or
//     none);
//   - gap: event positions missing below the run's high-water mark
//     (EventPage.Gaps) once the run is no longer running — a running
//     run's missing positions may still be in flight.
//
// One one-event read answers the last two; truncated, redacted,
// max_tokens and compacted are facts of an event or a step, badged
// there.
func (s *Server) runHoles(ctx context.Context, rec obsdb.RunRow) ([]stepHole, error) {
	holes := holeSet{}
	if h := rec.RequestsHole(); h != "" {
		holes.note(h)
	}
	if rec.Status == obsdb.StatusInterrupted {
		holes.note(obsdb.HoleInterrupted)
	}
	if rec.EventCount == 0 && rec.Status != obsdb.StatusRunning && rec.Status != obsdb.StatusSucceeded {
		holes.add(obsdb.HoleDerived, "this run has spans but no stored events: its row was built from its spans by the reader", holeFix(obsdb.HoleDerived))
	}
	if rec.EventCount > 0 {
		page, err := s.db.Events(ctx, rec.ID, -1, 1)
		if err != nil {
			return nil, err
		}
		if len(page.Events) > 0 && (page.Events[0].Content == "stripped" || page.Events[0].Content == "none") {
			holes.note(obsdb.HoleStripped)
		}
		if len(page.Gaps) > 0 && rec.Status != obsdb.StatusRunning {
			n := strconv.Itoa(len(page.Gaps))
			if len(page.Gaps) >= obsdb.MaxGaps {
				n += "+"
			}
			holes.add(obsdb.HoleGap, n+" of the run's event positions are missing: a destination dropped a batch", holeFix(obsdb.HoleGap))
		}
	}
	return holes.list(), nil
}

// serveRunEvents answers api/runs/{id}/events?after=&limit=: one page
// of the run's durable event stream, 0-based positions. `after` is the
// first position returned (next_after feeds straight back in); -1 and
// 0 both read from the start (-1 is the documented default for "the
// whole stream"). The database pages natively; done is its Done flag
// OR the row reading terminal through derivation (a crash orphan —
// running in the table, interrupted at read time — is finished in
// effect, exactly as the store era served it).
func (s *Server) serveRunEvents(w http.ResponseWriter, r *http.Request, id string) {
	if !s.scopeRunID(w, r, id) {
		return
	}
	q := r.URL.Query()
	after := int64(0)
	if v := q.Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < -1 {
			badRequest(w, r, "after must be -1 (from the start) or a non-negative position")
			return
		}
		after = max(n, 0)
	}
	limit := eventsDefaultLimit
	if n, ok := limitParam(w, r, q); !ok {
		return
	} else if n > 0 {
		limit = min(n, eventsMaxLimit)
	}

	// The API's cursor is inclusive (the first position to return);
	// obsdb's is exclusive (positions strictly after). One page is the
	// limit events from after: events at pos >= after.
	page, err := s.db.Events(r.Context(), id, after-1, limit)
	if err != nil {
		dbError(w, r, "events of run", id, err)
		return
	}
	done := page.Done
	if !done {
		// A row that reads interrupted at derivation time is terminal
		// in effect: its tail must report done, or a polling client
		// never stops. Queried only when the database itself did not
		// answer done (a running or interrupted row).
		det, err := s.db.Run(r.Context(), id)
		if err != nil {
			dbError(w, r, "events of run", id, err)
			return
		}
		if det.Status != obsdb.StatusRunning {
			done = true
		}
	}
	out := eventsPage{
		Events: make([]posEvent, 0, len(page.Events)),
		Done:   done,
		Gaps:   page.Gaps,
	}
	if out.Gaps == nil {
		out.Gaps = []int64{}
	}
	for _, pe := range page.Events {
		out.Events = append(out.Events, posEventOf(pe))
	}
	if page.NextAfter != nil {
		next := *page.NextAfter + 1
		out.NextAfter = &next
	}
	writeJSON(w, r, http.StatusOK, out)
}

// serveRunTranscript answers api/runs/{id}/transcript (S4.3): the
// messages bodies, in order, one batch per messages record — the
// replay-grade record of what the run saw and said (what replaces
// the store's result document; the fold takes finished text from
// here, because deltas are not stored).
func (s *Server) serveRunTranscript(w http.ResponseWriter, r *http.Request, id string) {
	if !s.scopeRunID(w, r, id) {
		return
	}
	batches, err := s.db.TranscriptBatches(r.Context(), id)
	if err != nil {
		dbError(w, r, "transcript of run", id, err)
		return
	}
	out := transcript{Batches: make([]transcriptBatch, 0, len(batches))}
	for _, b := range batches {
		tb := transcriptBatch{
			Index: b.Index, Step: b.Step, Input: b.Input, Messages: rawOrNull(string(b.Messages)),
		}
		switch {
		case b.Step < 0:
			tb.Step, tb.Badge = -1, string(obsdb.HoleNotRecorded)
		case b.InputDerived:
			tb.Badge = string(obsdb.HoleDerived)
		}
		out.Batches = append(out.Batches, tb)
	}
	writeJSON(w, r, http.StatusOK, out)
}

// serveRunSpans answers api/runs/{id}/spans (S4.3): the run's timed
// spans — the chat calls, tool executions and the invoke_agent span
// itself, with times.
func (s *Server) serveRunSpans(w http.ResponseWriter, r *http.Request, id string) {
	if !s.scopeRunID(w, r, id) {
		return
	}
	list, err := s.db.RunSpans(r.Context(), id)
	if err != nil {
		dbError(w, r, "spans of run", id, err)
		return
	}
	writeJSON(w, r, http.StatusOK, spans(list))
}

// serveTrace answers api/traces/{trace_id} (S4.3): any trace, weft or
// not — a stock OTel application's spans pass through obsdb whole,
// which is the polyglot promise.
func (s *Server) serveTrace(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/traces/")
	if id == "" {
		notFound(w, r, "no such api route "+r.URL.Path)
		return
	}
	list, err := s.db.Trace(r.Context(), id)
	if err != nil {
		dbError(w, r, "trace", id, err)
		return
	}
	// A trace nobody wrote reads the same as one the database never
	// held: obsdb answers empty, the API says 404.
	if len(list) == 0 {
		notFound(w, r, "no trace "+id)
		return
	}
	doc := spans(list)
	if !scopeSpans(w, r, doc.Spans) {
		return
	}
	writeJSON(w, r, http.StatusOK, doc)
}

// serveSessions answers api/sessions (S4.2): public_id and agent
// filters, before (RFC 3339, on last-seen) with before_id (the
// cursor's session id) and limit.
func (s *Server) serveSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := obsdb.SessionQuery{Agent: q.Get("agent"), PublicID: q.Get("public_id")}
	if id := idFrom(r); id.panel != nil {
		if asked := q.Get("public_id"); asked != "" && asked != id.panel.PublicID {
			forbidden(w, r)
			return
		}
		query.PublicID = id.panel.PublicID
	}
	if v := q.Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			badRequest(w, r, "before must be RFC 3339: "+err.Error())
			return
		}
		query.Before = t
		query.BeforeID = q.Get("before_id")
	}
	if n, ok := limitParam(w, r, q); !ok {
		return
	} else {
		query.Limit = n
	}
	page, err := s.db.Sessions(r.Context(), query)
	if err != nil {
		dbError(w, r, "sessions", "", err)
		return
	}
	out := sessionsPage{Total: page.Total, Sessions: make([]sessionRow, 0, len(page.Sessions))}
	for _, sr := range page.Sessions {
		out.Sessions = append(out.Sessions, sessRow(sr))
	}
	if page.NextBefore != nil { // the database's own cursor, as in serveRuns
		next := *page.NextBefore
		out.NextBefore = &next
		if page.NextBeforeID != "" {
			id := page.NextBeforeID
			out.NextBeforeID = &id
		}
	}
	writeJSON(w, r, http.StatusOK, out)
}

// serveSessionRoutes dispatches /api/sessions/{id}: the thread's turns
// in order (obsdb's SessionDetail).
func (s *Server) serveSessionRoutes(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if id == "" || strings.Contains(id, "/") {
		notFound(w, r, "no such api route "+r.URL.Path)
		return
	}
	if !s.scopeSessionID(w, r, id) {
		return
	}
	det, err := s.db.Session(r.Context(), id)
	if err != nil {
		dbError(w, r, "session", id, err)
		return
	}
	doc := sessionDoc{sessionRow: sessRow(det.SessionRow), Runs: make([]runRow, 0, len(det.Runs))}
	for _, rr := range det.Runs {
		doc.Runs = append(doc.Runs, row(rr))
	}
	writeJSON(w, r, http.StatusOK, doc)
}

// servePublic answers api/public/{public_id} (S4.3): the public id
// resolves to its session — the panel's handle, never the internal
// session id, is what a browser carries.
func (s *Server) servePublic(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/public/")
	if id == "" || strings.Contains(id, "/") {
		notFound(w, r, "no such api route "+r.URL.Path)
		return
	}
	if pid := idFrom(r).panel; pid != nil && id != pid.PublicID {
		forbidden(w, r)
		return
	}
	sid, err := s.db.ResolvePublicID(r.Context(), id)
	if err != nil {
		dbError(w, r, "public id", id, err)
		return
	}
	writeJSON(w, r, http.StatusOK, publicResolution{SessionID: sid})
}

// serveManifest answers api/manifest with the bytes passed to
// Manifest(...), or 404 when none was given (the UI hides the Agents
// nav).
func (s *Server) serveManifest(w http.ResponseWriter, r *http.Request) {
	// The manifest carries every agent's system prompt, which a
	// read-scoped panel token's page does not get (serveRuntimes strips
	// the same field for it): only an identity that may start
	// experiments reads it. The panel itself never asks for it.
	if !readsPrompts(r) {
		writeError(w, r, http.StatusForbidden, "forbidden",
			"the manifest carries the agents' system prompts: a read-scoped panel token does not read it")
		return
	}
	if len(s.manifest) == 0 {
		notFound(w, r, "no manifest configured")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(s.manifest)
}

// dbKind names the database's backend for api/meta, best effort: the
// dynamic type's full name ("*sqlite.DB" → "sqlite", anything else
// carrying "mem" → "memory", the hosted backend → "clickhouse"), else
// its bare type name.
func dbKind(db obsdb.DB) string {
	full := fmt.Sprintf("%T", db)
	lower := strings.ToLower(full)
	switch {
	case strings.Contains(lower, "mem"):
		return "memory"
	case strings.Contains(lower, "sqlite"):
		return "sqlite"
	case strings.Contains(lower, "clickhouse"):
		return "clickhouse"
	}
	name := strings.TrimPrefix(full, "*")
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		return "unknown"
	}
	return name
}

// weftVersion reports the framework module version this process built
// against ([version.Runtime]): the tag in a consumer's build, the
// source's own tag inside the workspace. Provenance, not a gate.
func weftVersion() string { return version.Runtime() }
