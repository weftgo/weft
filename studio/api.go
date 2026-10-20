package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/studio/internal/reqlog"
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

// writeError answers in the API's error shape. The reason is the
// request's in Studio's server log
// (studio/internal/reqlog): a 5xx is one ERROR line naming it and the
// request id, which a 5xx body carries too (request_id), so a user's
// report meets its line in studio.log.
func writeError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	reqlog.Refuse(r.Context(), code, msg)
	type body struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id,omitempty"`
	}
	b := body{Code: code, Message: msg}
	if status >= 500 {
		b.RequestID = reqlog.ID(r.Context())
	}
	writeJSON(w, r, status, struct {
		Error body `json:"error"`
	}{b})
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
// database's own error text (paths, SQL, hosts) — while Studio's server
// log still names the real error (reqlog.Cause), so the operator's line
// for that request id says what failed.
func dbError(w http.ResponseWriter, r *http.Request, noun, id string, err error) {
	switch {
	case errors.Is(err, obsdb.ErrNotFound):
		notFound(w, r, "no "+noun+" "+id)
	case idFrom(r).panel != nil:
		writeError(w, r, http.StatusInternalServerError, "internal",
			"read "+noun+" "+id+" failed")
		reqlog.Cause(r.Context(), "read "+noun+" "+id+": "+err.Error())
	default:
		writeError(w, r, http.StatusInternalServerError, "internal",
			"read "+noun+" "+id+": "+err.Error())
	}
}

// ── DTOs (S4.3) ────────────────────────────────────────────────────

// runRow is every list and detail: the identity chain, timing, the
// derived status, usage and the counts (S4.3's RunRow).
type runRow struct {
	ID           string         `json:"id"`
	ParentRunID  string         `json:"parent_run_id"`
	ParentCallID string         `json:"parent_call_id"`
	TraceID      string         `json:"trace_id"`
	Agent        string         `json:"agent"`
	Model        core.ModelInfo `json:"model"`
	ManifestHash string         `json:"manifest_hash"`
	WeftVersion  string         `json:"weft_version"`
	Service      string         `json:"service"`
	SessionID    string         `json:"session_id"`
	PublicID     string         `json:"public_id"`
	Turn         int            `json:"turn"`
	Playground   bool           `json:"playground"`
	ExperimentID string         `json:"experiment_id"`
	ForkedFrom   string         `json:"forked_from"`
	// SessionCompactions and DelegatedSessions are a session turn's
	// marks on a conversation's runs list (U16, UX-B-30: the devtools
	// panel's ⌗ and ↳ chips, read with the page so the panel needs no
	// per-turn fetch): the session compaction markers filed under the
	// turn (thread compacted after it — the session_events index,
	// obsdb.SessionEventReader) and the sessions its calls delegated
	// (SessionQuery.Parents' rows naming it as ParentRun). Only on a
	// /api/runs page the request narrowed by public_id or session (at
	// most turnMarksSessions sessions read: past it, the page's
	// turn_marks_capped hole); each absent when 0 or not read.
	SessionCompactions int `json:"session_compactions,omitempty"`
	DelegatedSessions  int `json:"delegated_sessions,omitempty"`
	// EndedBy, EndedByTurn and EndedKind name what ended a turn a busy
	// policy felled (U16-fix): the weft.thread.interrupt record filed
	// under it (target run) — by_run, by_turn and its policy
	// (interrupt | rollback). Read with the marks above, on the same
	// pages; absent when no such record was read.
	EndedBy     string `json:"ended_by,omitempty"`
	EndedByTurn int    `json:"ended_by_turn,omitempty"`
	EndedKind   string `json:"ended_kind,omitempty"`
	// ResumedFrom is the parked run this run resumes (its
	// weft.resumed_from, core.ResumeOf) and ResumedBy, on a parked run,
	// the run that resumed it (obsdb.RunRow; docs/record-contract.md
	// §6, UX-E-17). Absent when none: the status word reads
	// "parked → <resumer>" from resumed_by (lib/status.ts).
	ResumedFrom string `json:"resumed_from,omitempty"`
	ResumedBy   string `json:"resumed_by,omitempty"`
	// CallerRunID / CallerCallID are the tool call this run was started
	// from when it is no core Subagent child (obsdb.RunRow.CallerRunID;
	// weft.caller.*, docs/record-contract.md §2, U14): a thread/pool
	// child session's turn, another agent's Generate inside a handler —
	// a link, not parenthood (parent_run_id names a Subagent's parent
	// alone). Absent when none.
	CallerRunID  string            `json:"caller_run_id,omitempty"`
	CallerCallID string            `json:"caller_call_id,omitempty"`
	Meta         map[string]string `json:"meta"`
	Started      time.Time         `json:"started"`
	Finished     *time.Time        `json:"finished"`
	LastSeen     time.Time         `json:"last_seen"`
	Status       string            `json:"status"` // derived: a stale running row reads "interrupted"
	// Settling marks a run whose root span is stored (status already
	// succeeded or failed from it) but whose run_finish record is not,
	// heard from within 30 s by its own timestamps (obsdb.RunRow.Settling,
	// docs/record-contract.md §6): its records may still arrive, so a
	// client keeps following it as it follows a running one. Absent
	// otherwise. Derived at read time, never stored.
	Settling   bool   `json:"settling,omitempty"`
	Err        string `json:"err"`
	Steps      int    `json:"steps"`
	Pending    int    `json:"pending"`
	StopReason string `json:"stop_reason"`
	// StoppedBy names what ended the run (obsdb.RunRow.StoppedBy, U3.2):
	// the fired StopWhen condition's manifest name (has_tool_call:submit,
	// step_count_is:3, output_submitted, a custom name), final_answer,
	// parked, max_steps, usage_limit, loop_detected,
	// model_retries_exceeded or canceled — the control, where stop_reason
	// is the last model call's finish. Absent when nothing recorded one: a
	// failure no control decided (error names it), a run still going, a
	// run recorded before the field. CancelCause is a canceled run's
	// cause (thread.interrupted, thread.closed, studio.cancel, deadline,
	// or the caller's own), absent on a plain cancel.
	StoppedBy   string `json:"stopped_by,omitempty"`
	CancelCause string `json:"cancel_cause,omitempty"`
	// EventsEmitted / MessagesEmitted are what the core emitted (the
	// root span's weft.run.events / weft.run.messages, U5): beside
	// event_count / message_count, the denominator of a counted gap.
	// Omitted when 0 (no root yet, a weft before U5, or nothing emitted).
	EventsEmitted   int64 `json:"events_emitted,omitempty"`
	MessagesEmitted int64 `json:"messages_emitted,omitempty"`
	// ErrorType is a failed run's kind (obsdb.RunRow.ErrorType, U4): the
	// root's error.type — max_steps, usage_limit, loop_detected,
	// model_retries_exceeded, model_contract, model_error, … — with the
	// context errors read as "cancelled"; absent when the run did not
	// fail. The status stays failed: cancelled is a kind, never a status.
	ErrorType string `json:"error_type,omitempty"`
	// The problem counters (obsdb.Problems, U4), each omitted when 0:
	// what went wrong inside a run that may still have succeeded.
	problemFields
	Usage core.Usage `json:"usage"`
	// The run's cost (plan A5, pricing.go): its own model calls
	// (cost_usd: its usage minus its subagent children's, at its own
	// model's rates) and its descendants' (delegated_cost_usd), two
	// numbers, never folded; null with a badge and a reason when
	// unpriced, never 0. Computed at read time, never stored.
	costFields
	delegatedFields
	EventCount   int64 `json:"event_count"`
	MessageCount int64 `json:"message_count"`
	// DeltaCount is the run's streamed deltas: counted, never stored
	// (obsdb.RunRow.DeltaCount, a high-water mark over their counter).
	DeltaCount int64 `json:"delta_count"`
	// The request record's run columns (ADR 0028 §10):
	// instructions_hash "" means the run was written before the record
	// existed, and requests_badge then reads "not_recorded" (absent
	// otherwise); request_count 0 with an instructions_hash means the
	// run made no model call — no badge.
	InstructionsHash string `json:"instructions_hash"`
	CatalogHash      string `json:"catalog_hash"`
	RequestCount     int64  `json:"request_count"`
	RequestsBadge    string `json:"requests_badge,omitempty"`
	// Derived marks a run the reader built from stock OTel GenAI spans
	// (obsdb.RunRow.Derived, docs/record-contract.md §9): its id is
	// otel-<trace id>-<root span id>, it has no events, transcript or
	// request records, and requests_badge reads "derived" (the run's
	// holes carry the words, cause stock_spans). Absent on weft's runs.
	Derived bool `json:"derived,omitempty"`
	// PromptPreview is the run's prompt — the turn's own (obsdb.PromptText/TurnPrompt:
	// the input record's last user text, a resume's new prompt when it
	// carries one, else the first) — cut to 200 runes (obsdb.RunRow.PromptPreview, plan A7).
	// Absent when the records carried no content: a content-off run
	// stores no messages, and the stripped / not_recorded holes the run
	// document already reports say why — no badge of its own.
	PromptPreview string `json:"prompt_preview,omitempty"`
	// ChildrenCount is the runs whose parent_run_id names this one, one
	// level deep: its core Subagent children (on runs recorded before
	// weft 0.18 also the pool child turns and nested Generates the core
	// then linked as children — since O11 they carry caller_run_id and
	// are listed in the run page's caller_linked instead) and ChildrenFailed those of them whose status is
	// failed (H9, UX-A-19: the runs list's "⑂ n" chip). Read time, from
	// the children read the cost already makes (coster.primeTrees: one
	// obsdb.TreeReader.ChildRuns read per level of a page, never one per
	// row); each omitted when 0, and on a row the coster did not price.
	ChildrenCount  int `json:"children_count,omitempty"`
	ChildrenFailed int `json:"children_failed,omitempty"`
	// ChildrenUnknown says the children read failed (P8-fix-b): the
	// count above is not known — absent, it would read as "no
	// subagents". Omitted when the count is known.
	ChildrenUnknown bool `json:"children_unknown,omitempty"`
	// Holes is set on a run document's children[] rows only (plan
	// A10): each child's own runHoles, so the parent's page badges a
	// content-off or interrupted child before its document is read.
	// Absent on list rows; a runDoc's own Holes shadows it.
	Holes []stepHole `json:"holes,omitempty"`
}

// row maps an obsdb run row onto the DTO. The status is the
// database's derived one: a crash-orphaned running row reads
// interrupted and is shown, never hidden (A1).
func row(rec obsdb.RunRow) runRow {
	out := runRow{
		ID:            rec.ID,
		ParentRunID:   rec.ParentRunID,
		ParentCallID:  rec.ParentCallID,
		TraceID:       rec.TraceID,
		Agent:         rec.Agent,
		Model:         core.ModelInfo{Provider: rec.Provider, Name: rec.Model},
		ManifestHash:  rec.ManifestHash,
		WeftVersion:   rec.WeftVersion,
		Service:       rec.Service,
		SessionID:     rec.SessionID,
		PublicID:      rec.PublicID,
		Turn:          rec.Turn,
		Playground:    rec.Playground,
		ExperimentID:  rec.ExperimentID,
		ForkedFrom:    rec.ForkedFrom,
		ResumedFrom:   rec.ResumedFrom,
		ResumedBy:     rec.ResumedBy,
		CallerRunID:   rec.CallerRunID,
		CallerCallID:  rec.CallerCallID,
		Meta:          rec.Meta,
		Started:       rec.Started,
		LastSeen:      rec.LastSeen,
		Status:        string(rec.Status),
		Settling:      rec.Settling,
		Err:           rec.Err,
		Steps:         rec.Steps,
		Pending:       rec.Pending,
		StopReason:    rec.StopReason,
		StoppedBy:     rec.StoppedBy,
		CancelCause:   rec.CancelCause,
		EventsEmitted: rec.EventsEmitted, MessagesEmitted: rec.MessagesEmitted,
		ErrorType:     rec.ErrorType,
		problemFields: problemsOf(rec.Problems),
		Usage:         rec.Usage,
		EventCount:    rec.EventCount,
		MessageCount:  rec.MessageCount,
		DeltaCount:    rec.DeltaCount,

		InstructionsHash: rec.InstructionsHash,
		CatalogHash:      rec.CatalogHash,
		RequestCount:     rec.RequestCount,
		RequestsBadge:    string(rec.RequestsHole()),
		PromptPreview:    rec.PromptPreview,
		Derived:          rec.Derived,
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

// problemFields are a run row's problem counters on the wire
// (obsdb.Problems, docs/record-contract.md §6), each omitted when 0:
// attempts_retried (failed model attempts), fallbacks (calls another
// model answered), tool_errors, denied (policy refusals and denied
// approvals), truncated (results the cap cut), repaired (0 until the
// core reports repaired arguments) and tap_panics (observer panics the
// run contained, weft.tap.panics). log_warn / log_error count the
// app's own log records under the run's spans by severity — read time,
// on the run document only (GET /api/runs/{id}), never a list row —
// and logs_capped says the count stopped at the reader's cap
// (obsdb.MaxLogCandidates WARN-or-above lines): both are floors, drawn
// "≥".
type problemFields struct {
	AttemptsRetried int64 `json:"attempts_retried,omitempty"`
	Fallbacks       int64 `json:"fallbacks,omitempty"`
	ToolErrors      int64 `json:"tool_errors,omitempty"`
	Denied          int64 `json:"denied,omitempty"`
	Truncated       int64 `json:"truncated,omitempty"`
	Repaired        int64 `json:"repaired,omitempty"`
	TapPanics       int64 `json:"tap_panics,omitempty"`
	LogWarn         int64 `json:"log_warn,omitempty"`
	LogError        int64 `json:"log_error,omitempty"`
	LogsCapped      bool  `json:"logs_capped,omitempty"`
}

func problemsOf(p obsdb.Problems) problemFields {
	return problemFields{
		AttemptsRetried: p.AttemptsRetried, Fallbacks: p.Fallbacks, ToolErrors: p.ToolErrors,
		Denied: p.Denied, Truncated: p.Truncated, Repaired: p.Repaired,
		TapPanics: p.TapPanics, LogWarn: p.LogWarn, LogError: p.LogError,
	}
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
	// TurnMarksCapped is the turn marks' hole (U16-fix): truncated,
	// cause response_cap, Count the page's session turns left without
	// their marks — sessions past turnMarksSessions, or a session whose
	// marks ran past turnMarksPages pages. Absent when every turn's
	// marks were read (or none were asked for).
	TurnMarksCapped *treeHole `json:"turn_marks_capped,omitempty"`
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

// runPage is what GET /api/runs/{id} serves: the run document and the
// compactions its records name (ADR 0028 §8, obsdb.DB.Compactions) —
// [] for a run that never compacted, and for every run written before
// A9: the record is optional, so its absence is no hole. The export's
// run block is the bare runDoc; it carries the compactions (with
// their messages) in a block of its own.
type runPage struct {
	runDoc
	Compactions []runCompaction `json:"compactions"`
	// ManifestAvailable: the run's manifest_hash resolves in the
	// manifest store — GET /api/manifests/{manifest_hash} answers the
	// configuration it ran with (U27). Absent when it does not.
	ManifestAvailable bool `json:"manifest_available,omitempty"`
	// CallerLinked is the runs started from this run's tool calls that
	// are not its Subagent children (RunQuery.CallerRunID, U14): each a
	// thread/pool child session's turn (its own session_id and
	// meta weft.session.parent: "delegated session") or another agent's
	// Generate inside a handler — links, never parenthood: their usage
	// is their own, in no figure of this run. In start order, the first
	// callerLinkedCap (CallerLinkedMore: more were not listed); under a
	// panel token its public id's alone, the rest counted in
	// CallerLinkedHidden, never named. Absent when none;
	// CallerLinkedUnknown says the read failed (absent, the list would
	// read as "none").
	CallerLinked        []runRow `json:"caller_linked,omitempty"`
	CallerLinkedMore    bool     `json:"caller_linked_more,omitempty"`
	CallerLinkedHidden  int      `json:"caller_linked_hidden,omitempty"`
	CallerLinkedUnknown bool     `json:"caller_linked_unknown,omitempty"`
	// Decided is, on a parked run a resume names (resumed_by), each
	// parked call's decision as that resume applied it; Resumes, on the
	// resume (resumed_from), the same calls — the card drawn before its
	// step 0 (U13.2, decisions.go). Absent when the run is neither.
	Decided *decisionDoc `json:"decided,omitempty"`
	Resumes *decisionDoc `json:"resumes,omitempty"`
	// Summarizer is what the summarizer calls filed under this run cost
	// (UX-B-13, U15.1-fix): the compaction markers and SummarizeLeft
	// branch records under it that name summarizer_usage, priced as the
	// session timeline prices its turn (markCost: the recorded
	// summarizer_model's rates, else the run's model's, an estimate).
	// Never in the run's usage or cost_usd. Absent when none, or when
	// the session events could not be read.
	Summarizer *summarizerCost `json:"summarizer,omitempty"`
}

// callerLinkedCap bounds a run page's caller_linked rows.
const callerLinkedCap = 100

// runCompaction is one compaction a run's records name, counts and
// hash only: a run-scope view (scope "run": the PrepareStep rewrite of
// one step's request — index, step and the replaced transcript range
// [from_seq, to_seq), seq being the message's position in the
// concatenated growth records) or thread's session marker (scope
// "session": reason, the counts and the token estimates; no index,
// step or range). A view's messages are not here: the page reads the
// replaced range from the transcript route it already holds, and the
// export carries the view's body (the compactions block).
type runCompaction struct {
	Scope        string `json:"scope"`
	Index        *int64 `json:"index,omitempty"`
	Step         *int   `json:"step,omitempty"`
	FromSeq      *int64 `json:"from_seq,omitempty"`
	ToSeq        *int64 `json:"to_seq,omitempty"`
	Hash         string `json:"hash"`
	Replaced     int    `json:"replaced"`
	Entries      int    `json:"entries"`
	TokensBefore int64  `json:"tokens_before,omitempty"`
	TokensAfter  int64  `json:"tokens_after,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// runCompactions maps DB.Compactions onto the run page's DTO.
func runCompactions(cs []obsdb.Compaction) []runCompaction {
	out := make([]runCompaction, 0, len(cs))
	for _, c := range cs {
		rc := runCompaction{Scope: c.Scope, Hash: c.Hash, Replaced: c.Replaced, Entries: c.Entries,
			TokensBefore: c.TokensBefore, TokensAfter: c.TokensAfter, Reason: c.Reason}
		if c.Scope != obsdb.CompactionSession {
			idx, step, from, to := c.Index, c.Step, c.FromSeq, c.ToSeq
			rc.Index, rc.Step, rc.FromSeq, rc.ToSeq = &idx, &step, &from, &to
		}
		out = append(out, rc)
	}
	return out
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
	// paging cursor — follow it (not done) until it reads null. A
	// settling run (its root span stored, its run_finish not, heard
	// from within 30 s: obsdb.RunRow.Settling) is not done yet — the
	// records behind its root may still land.
	Done bool `json:"done"`
	// Gaps are durable positions missing below the high-water mark: a
	// lost batch, never a delta (D3) — the fold's input stays
	// contiguous unless something was dropped in transit.
	Gaps []int64 `json:"gaps"`
	// The badge, on a derived run's page alone (derivedBadge): it has
	// no events by construction, and says so rather than read empty.
	badgeFields
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

// transcriptAsOf is api/runs/{id}/transcript?step=N (ADR 0029): the
// bare route's batches, then the messages step N's model call carried
// — the record's truth, assembled by obsdb.MessagesAsOf, and the replay
// prefix for from_step N > 0. (from_step 0 re-runs step 0's PrepareStep
// over the run's original input, so at N = 0 a compacted answer here is
// not the replay's prefix.) CompactedAt names the run-scope view step
// N's request carried (counts, range and hash, as the run page serves
// compactions), null when the request carried the plain transcript.
// The badge is "derived" when no request record placed the messages (a
// run written before ADR 0028: the from_step cut rule did), "hidden"
// with Messages null when a read-scoped panel token asks for a step
// whose request carried a view (its messages are request content).
type transcriptAsOf struct {
	transcript
	Step        int            `json:"step"`
	Messages    []core.Message `json:"messages"`
	CompactedAt *compactedNote `json:"compacted_at"`
	badgeFields
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
	// RunID is the run the span belongs to: on api/runs/{id}/spans the
	// run's (every span), with ?tree=1 each descendant's own; absent on
	// api/traces/{id}, whose spans' weft.run.id is in attrs.
	// ParentRunID is that run's parent, on ?tree=1 only, absent when
	// none is known. A tree's spans are keyed by run_id + span_id.
	RunID       string `json:"run_id,omitempty"`
	ParentRunID string `json:"parent_run_id,omitempty"`
}

type spanEventDTO struct {
	Time  time.Time      `json:"time"`
	Name  string         `json:"name"`
	Attrs map[string]any `json:"attrs"`
}

type spansDoc struct {
	Spans []spanDTO `json:"spans"`
}

// toolNameAttrs are the invoke_agent span's per-run tool overrides
// (core's OnlyTools, ParkOn, ParkAllExcept): comma-joined tool names —
// a slice of the catalog, which a read-scoped panel token does not
// read. A named tool choice (weft.override.tool_choice "tool:<name>",
// core's ToolChoiceNamed mode and the name) names one too.
var toolNameAttrs = []string{"weft.override.tools", "weft.override.park_on", "weft.override.park_all_except"}

const attrOverrideToolChoice = "weft.override.tool_choice"

// namesTools says whether a span carries a tool name a read-scoped
// panel token does not read.
func namesTools(attrs map[string]any) bool {
	for _, k := range toolNameAttrs {
		if _, ok := attrs[k]; ok {
			return true
		}
	}
	tc, _ := attrs[attrOverrideToolChoice].(string)
	return strings.HasPrefix(tc, string(core.ToolChoiceNamed)+":")
}

// spansFor is the spans the request's identity may read: all of them
// for one that reads prompts (readsPrompts); for a read-scoped panel
// token, copies without the tool names (toolNameAttrs, a named tool
// choice cut to its mode). Every route that serves spans goes through
// it: spans, traces, the export.
func spansFor(r *http.Request, list []obsdb.Span) []obsdb.Span {
	if readsPrompts(r) {
		return list
	}
	out := make([]obsdb.Span, len(list))
	for i, sp := range list {
		if namesTools(sp.Attrs) {
			sp.Attrs = maps.Clone(sp.Attrs)
			for _, k := range toolNameAttrs {
				delete(sp.Attrs, k)
			}
			if tc, _ := sp.Attrs[attrOverrideToolChoice].(string); strings.HasPrefix(tc, string(core.ToolChoiceNamed)+":") {
				sp.Attrs[attrOverrideToolChoice] = string(core.ToolChoiceNamed)
			}
		}
		out[i] = sp
	}
	return out
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
// turn's status (S4.3) — its outcome (obsdb.OutcomeOf, the session
// page's word and SessionQuery.Status's rule, so a row and ?status=
// agree): succeeded, failed, running or interrupted, else cancelled,
// ended (thread's busy policy), parked (an open park) or resumed (a
// decided one), which no ?status= keeps.
type sessionRow struct {
	ID        string     `json:"id"`
	PublicID  string     `json:"public_id"`
	Agent     string     `json:"agent"`
	Turns     int        `json:"turns"`
	FirstSeen time.Time  `json:"first_seen"`
	LastSeen  time.Time  `json:"last_seen"`
	Status    string     `json:"status"`
	Usage     core.Usage `json:"usage"`
	// SummarizerUsage is what weft/thread's summarizer model calls cost
	// the session (obsdb.SessionRow.SummarizerUsage, record contract §6):
	// apart from usage, never folded into it nor priced here (U15.1).
	// Absent when the session summarized nothing.
	SummarizerUsage *core.Usage `json:"summarizer_usage,omitempty"`
	// Title is the newest turn's weft.session.title (thread's Title, a
	// label that survives content-off; obsdb.SessionRow.Title), absent
	// when none was set. FirstPrompt is the first turn's prompt_preview,
	// absent when that turn stored no content. The UI titles the row
	// with the first present of the two, else the id (lib/title.ts).
	Title       string `json:"title,omitempty"`
	FirstPrompt string `json:"first_prompt,omitempty"`
	// ContentOff: the first turn was recorded without content
	// (obsdb.SessionRow.ContentOff) — the list says "(content not
	// captured)" for an untitled row instead of its id. Absent when
	// false.
	ContentOff bool `json:"content_off,omitempty"`
	// The turns' state over time (U16; obsdb.SessionTurnFacts): pending
	// is the newest turn's pending calls when it is an open park (the
	// session awaits a decision; the status chip reads parked, as a
	// run row's); failed_turns the failed turns and ended_turns, apart,
	// the turns thread's busy policy ended (interrupt / rollback, never
	// a failure); cancelled_turns, apart again, the other turns whose
	// context was cancelled (thread.closed, studio.cancel, a deadline:
	// no failure, the strip's ⊘); resumes the turns that resume a parked one (turns -
	// resumes exchanges); outcomes the newest 40 turns' marks, oldest
	// first (succeeded · failed · running · interrupted · cancelled ·
	// ended · parked · resumed); forked_from_session a forked session's
	// <origin>#<entry>. Each absent when zero / empty.
	Pending     int      `json:"pending,omitempty"`
	FailedTurns int      `json:"failed_turns,omitempty"`
	EndedTurns  int      `json:"ended_turns,omitempty"`
	Cancelled   int      `json:"cancelled_turns,omitempty"`
	Resumes     int      `json:"resumes,omitempty"`
	Outcomes    []string `json:"outcomes,omitempty"`
	ForkedFrom  string   `json:"forked_from_session,omitempty"`
	// ParentSession, ParentTurn, ParentCall and ParentRun are a
	// delegated session's lineage (thread/pool, ADR 0022;
	// obsdb.SessionRow, U14): the session that delegated it, the
	// delegating turn's number (from parent_run when it is that
	// session's <session>-t<n>), the tool call and the run it belonged
	// to. Each absent when unknown — every one for a session nothing
	// delegated; parent_run and parent_turn for a child submitted
	// outside a run (p.Submit).
	ParentSession string `json:"parent_session,omitempty"`
	ParentTurn    int    `json:"parent_turn,omitempty"`
	ParentCall    string `json:"parent_call,omitempty"`
	ParentRun     string `json:"parent_run,omitempty"`
	// The session's cost (plan A5): its turns' own + delegated — the
	// session's usage already includes their subagents. A thread/pool
	// child is another session, with its own row: its cost is on the
	// parent's session document (delegated_sessions_*), never folded in
	// here.
	costFields
}

func sessRow(r obsdb.SessionRow) sessionRow {
	var sum *core.Usage
	if r.SummarizerUsage != (core.Usage{}) {
		u := r.SummarizerUsage
		sum = &u
	}
	return sessionRow{
		SummarizerUsage: sum,
		ID:              r.ID, PublicID: r.PublicID, Agent: r.Agent, Turns: r.Turns,
		FirstSeen: r.FirstSeen, LastSeen: r.LastSeen,
		Status: sessionStatus(r), Usage: r.Usage,
		Title: r.Title, FirstPrompt: r.FirstPrompt, ContentOff: r.ContentOff,
		ParentSession: r.ParentSession, ParentTurn: turnOf(r.ParentSession, r.ParentRun),
		ParentCall: r.ParentCall, ParentRun: r.ParentRun,
		Pending: r.Pending, FailedTurns: r.FailedTurns, EndedTurns: r.EndedTurns,
		Cancelled: r.CancelledTurns, Resumes: r.Resumes, Outcomes: outcomeWords(r.Outcomes), ForkedFrom: r.ForkedFrom,
	}
}

// sessionStatus is a session row's status: its newest turn's outcome
// (the strip's last mark), else — a row that read no turn facts — its
// stored status.
func sessionStatus(r obsdb.SessionRow) string {
	if n := len(r.Outcomes); n > 0 {
		return string(r.Outcomes[n-1])
	}
	return string(r.Status)
}

// outcomeWords is a session row's strip as JSON strings.
func outcomeWords(os []obsdb.TurnOutcome) []string {
	out := make([]string, len(os))
	for i, o := range os {
		out[i] = string(o)
	}
	return out
}

// turnOf is n when run is session's <session>-t<n> (thread's id rule),
// else 0.
func turnOf(session, run string) int {
	rest, ok := strings.CutPrefix(run, session+"-t")
	if !ok || session == "" {
		return 0
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

type sessionsPage struct {
	Total      int          `json:"total"`
	Sessions   []sessionRow `json:"sessions"`
	NextBefore *time.Time   `json:"next_before"`
	// NextBeforeID pairs with next_before as in runsPage: (last_seen, id).
	NextBeforeID *string `json:"next_before_id"`
}

// sessionDoc is GET /api/sessions/{id}: the row and its own turns in
// order; experiments hang off runs via forked_from. Delegated is the
// sessions it delegated (thread/pool children, SessionQuery.Parents,
// U14): each a row with its lineage and its own cost, newest first, at
// most delegatedCap (DelegatedMore past it), [] when none;
// DelegatedUnknown says the read failed. DelegatedUsage and the
// delegated_sessions_cost_* figures are their whole tree's — every
// session delegated under this one, at any depth, own + their
// subagents — beside the session's own usage and cost, never folded
// into them (thread's Usage().Delegated, docs/thread-operations.md).
type sessionDoc struct {
	sessionRow
	Runs             []runRow     `json:"runs"`
	Delegated        []sessionRow `json:"delegated"`
	DelegatedMore    bool         `json:"delegated_more,omitempty"`
	DelegatedUnknown bool         `json:"delegated_unknown,omitempty"`
	DelegatedUsage   core.Usage   `json:"delegated_usage"`
	// DelegatedHoles is the delegated block's holes (ADR 0028 §11's
	// table): hidden, counted, when a panel token's scope left out
	// sessions this one delegated (another public id's, or none) —
	// never named, and the delegated cost then not known.
	DelegatedHoles []stepHole `json:"delegated_holes,omitempty"`
	delegatedSessionsFields
}

// delegatedSessionsFields is a session's delegated sessions' bill.
type delegatedSessionsFields struct {
	CostUSD    *float64 `json:"delegated_sessions_cost_usd"`
	CostKnown  bool     `json:"delegated_sessions_cost_known"`
	CostReason string   `json:"delegated_sessions_cost_reason,omitempty"`
	// Cache is cost_fields' cache over the delegated sessions, kept
	// from each visited session's own cost: exact only when every one
	// is exact, not_reported when any one is not (a sum's split would
	// read exact over a session that reported none); absent when
	// nothing was delegated.
	Cache string `json:"delegated_sessions_cache,omitempty"`
}

// delegatedCap bounds a session document's delegated rows, and
// delegatedTreeCap the sessions its delegated figures walk.
const (
	delegatedCap     = 100
	delegatedTreeCap = 500
)

// publicResolution is GET /api/public/{public_id}: the public id is
// the browser-safe handle; this is where it becomes a session.
type publicResolution struct {
	SessionID string `json:"session_id"`
}

// ── Handlers ───────────────────────────────────────────────────────

// debugScope names what the debugger's write verbs may act on, only
// when those verbs are registered: "own+runtime" while a connected
// runtime registered runtime.Sessions (the app's own turns take
// approvals and steer through it (cancel is refused), plan F4.1) or exposes an
// agent built with runtime.Breakpoints() (breakpoints park the app's
// own runs, F4.2), else the runtime-started runs alone.
func (s *Server) debugScope() string {
	for _, g := range s.groups {
		if g.capability == "breakpoints" {
			if s.runtimeSrv != nil && s.runtimeSrv.AnyOwnRuns() {
				return debugScopeOwn
			}
			return debugScopeRuntime
		}
	}
	return ""
}

// meta's debug_scope values.
const (
	debugScopeRuntime = "runtime-started runs"
	debugScopeOwn     = "own+runtime"
)

// serveRuns answers api/runs (S4.2): agent, status, session,
// public_id, playground, experiment_id (the runs one playground
// experiment labelled weft.experiment.id: a filter that narrows, so any
// identity may send it — a panel token's rows stay inside its public
// id, which it scopes on top), parent ("" top-level only, "*" all, or a run
// id for its children), all (a boolean: true is parent=*, every run,
// subagent children included — a parent id given beside it wins),
// stopped_by (what ended the run, exact — has_tool_call:submit — or, a
// value ending in ':', a prefix: has_tool_call: is every tool-call
// stop), error_type (a failed run's kind, exact: max_steps, cancelled,
// model_error, …), id_prefix (the runs whose id starts with it, byte
// for byte: a pasted short id — the ⌘K palette's lookup, children
// included under all=1), turn_below / turn_above (the runs whose turn
// is below / above it: with session and limit=1, a turn's neighbour and
// total how many lie that side — the run page's turn stepper, O(1)
// rows), oldest (true: oldest first, the cursor paging forward),
// tag.<k>=<v> metadata matches, before (the
// RFC 3339 paging cursor on started) with before_id (the cursor's run
// id: exact inside a tie) and limit. next_before / next_before_id are
// the next page's before / before_id, null on the last page.
func (s *Server) serveRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := obsdb.RunQuery{
		Agent:        q.Get("agent"),
		SessionID:    q.Get("session"),
		PublicID:     q.Get("public_id"),
		ExperimentID: q.Get("experiment_id"),
		StoppedBy:    q.Get("stopped_by"),
		ErrorType:    q.Get("error_type"),
		IDPrefix:     q.Get("id_prefix"),
		ParentRunID:  parentParam(q),
		Meta:         tagParams(q),
	}
	st, ok := statusParam(w, r, q)
	if !ok {
		return
	}
	query.Status = st
	if v := q.Get("playground"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			badRequest(w, r, "playground must be true or false")
			return
		}
		query.Playground = &b
	}
	if v := q.Get("all"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			badRequest(w, r, "all must be true or false (1 or 0)")
			return
		}
		if b && query.ParentRunID == "" {
			query.ParentRunID = "*"
		}
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
	for name, dst := range map[string]*int{"turn_below": &query.TurnBelow, "turn_above": &query.TurnAbove} {
		if v := q.Get(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				badRequest(w, r, name+" must be a positive turn number")
				return
			}
			*dst = n
		}
	}
	if v := q.Get("oldest"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			badRequest(w, r, "oldest must be true or false (1 or 0)")
			return
		}
		query.Oldest = b
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
	c := s.coster(r.Context())
	c.primeTrees(page.Runs)
	for _, rec := range page.Runs {
		out.Runs = append(out.Runs, c.row(rec))
	}
	// The marks only on a page the request itself narrowed: a panel
	// token's scope sets PublicID on every page (scopeRunsQuery).
	if q.Get("public_id") != "" || q.Get("session") != "" {
		out.TurnMarksCapped = s.turnMarks(r.Context(), out.Runs, panelPublicID(r))
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

// turnMarksSessions bounds the sessions one runs page reads turn marks
// for: a conversation's page holds one or a few; turnMarksPages the
// pages one read follows (SessionEvents' MaxSessionEventLimit and
// Sessions' 500 each) before it stops and says so.
const (
	turnMarksSessions = 50
	turnMarksPages    = 10
)

// turnMarkKinds are the session events a turn mark reads: the
// compaction markers (⌗) and the interrupt records (ended_by).
var turnMarkKinds = []string{obsdb.RecordCompaction, "interrupt"}

// turnMarks fills a conversation page's session turns' marks
// (runRow.SessionCompactions, DelegatedSessions, EndedBy / EndedByTurn
// / EndedKind; U16, U16-fix): per session on the page (the first
// turnMarksSessions), its compaction markers and interrupt records
// under the page's turns — one SessionEvents read with those kinds,
// paged by offset while Total says more — and the sessions it
// delegated, paged by cursor. pub, a panel token's public id, scopes
// every read. What it could not read is the returned hole (nil when
// none): a turn left unmarked is said, never a chip drawn wrong — past
// a bound, or when a read fails (its turns' marks absent and counted in
// the hole, the failure logged at Debug), on its first page or a later
// one.
func (s *Server) turnMarks(ctx context.Context, rows []runRow, pub string) *treeHole {
	bySession := map[string][]int{}
	var order []string
	skipped := 0
	for i, r := range rows {
		if r.SessionID == "" {
			continue
		}
		idx, ok := bySession[r.SessionID]
		if !ok {
			if len(order) == turnMarksSessions {
				skipped++ // a session past the bound: its turns unmarked
				continue
			}
			order = append(order, r.SessionID)
		}
		bySession[r.SessionID] = append(idx, i)
	}
	if len(order) == 0 {
		return capHole(skipped)
	}
	byID := map[string]int{}
	for _, idx := range bySession {
		for _, i := range idx {
			byID[rows[i].ID] = i
		}
	}
	if er, ok := s.db.(obsdb.SessionEventReader); ok {
		for _, sid := range order {
			ids := make([]string, 0, len(bySession[sid]))
			for _, i := range bySession[sid] {
				ids = append(ids, rows[i].ID)
			}
			read, total, failed := 0, 0, false
			for range turnMarksPages {
				page, err := er.SessionEvents(ctx, obsdb.SessionEventQuery{SessionID: sid, PublicID: pub, RunIDs: ids,
					Kinds: turnMarkKinds, Offset: read, Limit: obsdb.MaxSessionEventLimit})
				if err != nil {
					slog.DebugContext(ctx, "studio: a page's turn marks could not be read", "session", sid, "err", err)
					failed = true
					break
				}
				total = page.Total
				for _, ev := range page.Events {
					if i, ok := byID[ev.RunID]; ok {
						markTurn(&rows[i], ev)
					}
				}
				read += len(page.Events)
				if len(page.Events) == 0 || read >= total {
					break
				}
			}
			if failed || read < total {
				skipped += len(ids)
			}
		}
	}
	q := obsdb.SessionQuery{Parents: order, PublicID: pub, Limit: 500}
	for p := 0; ; p++ {
		if p == turnMarksPages {
			// The delegated counts are floors: every listed turn's.
			skipped += len(byID)
			break
		}
		kids, err := s.db.Sessions(ctx, q)
		if err != nil {
			slog.DebugContext(ctx, "studio: a page's delegated sessions could not be read", "err", err)
			skipped += len(byID) // the delegated counts are floors, or absent
			break
		}
		for _, k := range kids.Sessions {
			if i, ok := byID[k.ParentRun]; ok {
				rows[i].DelegatedSessions++
			}
		}
		if kids.NextBefore == nil || len(kids.Sessions) == 0 {
			break
		}
		q.Before, q.BeforeID = *kids.NextBefore, kids.NextBeforeID
	}
	return capHole(min(skipped, len(rows)))
}

// markTurn adds one session event to its turn's marks: a compaction
// marker counts, an interrupt record that felled the turn (target run)
// names what ended it — its first, should two arrive.
func markTurn(r *runRow, ev obsdb.SessionEvent) {
	switch obsdb.SessionEventKind(ev) {
	case obsdb.RecordCompaction:
		r.SessionCompactions++
	case "interrupt":
		var f struct {
			Policy string `json:"policy"`
			Target string `json:"target"`
			ByRun  string `json:"by_run"`
			ByTurn int    `json:"by_turn"`
		}
		if r.EndedKind != "" || json.Unmarshal(ev.Body, &f) != nil || (f.Target != "" && f.Target != "run") {
			return
		}
		r.EndedBy, r.EndedByTurn, r.EndedKind = f.ByRun, f.ByTurn, f.Policy
	}
}

// capHole is the turn marks' hole for n turns left without their
// marks, nil for none.
func capHole(n int) *treeHole {
	if n == 0 {
		return nil
	}
	return &treeHole{
		Hole: string(obsdb.HoleTruncated), Cause: string(obsdb.CauseResponseCap), Count: n,
		Reason: fmt.Sprintf("this page's turn marks (compacted, ended by, delegated) were not all read: %d %s may lack theirs, nothing was lost", n, map[bool]string{true: "turn", false: "turns"}[n == 1]),
		Fix:    "open the session's timeline (sessions/{id}/timeline), or narrow the page by session",
	}
}

// statusParam is the one status parser of /api/runs and /api/search
// (I3, issue 003): "" any; a word of obsdb.StatusWords — the derived
// four and the read-time parked and cancelled, which the backends map
// onto stored columns (docs/record-contract.md §6); anything else a 400
// naming the allowed words, never an empty list for a typo.
func statusParam(w http.ResponseWriter, r *http.Request, q map[string][]string) (obsdb.Status, bool) {
	var v string
	if vs := q["status"]; len(vs) > 0 {
		v = vs[0]
	}
	st, err := obsdb.ParseStatus(v)
	if err != nil {
		badRequest(w, r, err.Error())
		return "", false
	}
	return st, true
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

// serveRun answers api/runs/{id}: the row, the child runs (obsdb's
// Run detail carries both) and the run's compactions (runPage). Events are deliberately not here — they
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
	c := s.coster(r.Context())
	c.prime(det.ID, det.Children)
	c.primeTrees(c.kids[det.ID]) // the scope's children (prime)
	kids, hidden := c.visibleKids(det.ID)
	doc := runDoc{runRow: c.row(det.RunRow), Children: s.childRows(r.Context(), c, kids)}
	doc.LogWarn, doc.LogError, doc.LogsCapped = s.logCounts(r.Context(), id)
	holes, err := s.runHoles(r.Context(), det.RunRow)
	if err != nil {
		dbError(w, r, "run", id, err)
		return
	}
	if hidden > 0 {
		// A panel token's run document lists its public id's children
		// only (inScope); the rest are one counted hole, never named.
		holes = append(holes, hiddenChildrenHole(hidden))
	}
	doc.Holes = holes
	comps, err := s.db.Compactions(r.Context(), id)
	if err != nil {
		dbError(w, r, "compactions of run", id, err)
		return
	}
	page := runPage{runDoc: doc, Compactions: runCompactions(comps),
		ManifestAvailable: s.manifestAvailable(r.Context(), det.ManifestHash)}
	s.callerLinked(r.Context(), c, id, &page)
	s.runDecisions(r, det.RunRow, &page)
	page.Summarizer = s.runSummarizer(r, det.RunRow)
	writeJSON(w, r, http.StatusOK, page)
}

// runSummarizer is a session run's summarizer figure (runPage's
// Summarizer): the session events filed under it — one bounded read,
// the run's own marks — that name a summarizer call, each priced by
// markCost at the run's model as the stand-in. nil for a run of no
// session, a database without session events, a failed read (logged:
// reporting never fails the run document) or no summarizer call.
func (s *Server) runSummarizer(r *http.Request, rr obsdb.RunRow) *summarizerCost {
	er, ok := s.db.(obsdb.SessionEventReader)
	if !ok || rr.SessionID == "" {
		return nil
	}
	pub := panelPublicID(r)
	page, err := er.SessionEvents(r.Context(), obsdb.SessionEventQuery{SessionID: rr.SessionID, PublicID: pub,
		RunIDs: []string{rr.ID}, Limit: obsdb.MaxSessionEventLimit})
	if err != nil {
		slog.Default().DebugContext(r.Context(), "studio: a run's summarizer marks could not be read", "run", rr.ID, "err", err)
		return nil
	}
	mb := &markBuilder{sessionID: rr.SessionID, turns: map[string]int{}, pub: pub}
	at := core.ModelInfo{Provider: rr.Provider, Name: rr.Model}
	var sum summarizerSum
	for _, e := range page.Events {
		m, ok := mb.mark(e)
		if !ok {
			continue
		}
		if u, mi, ok := markSummarizer(m); ok {
			sum.add(u, s.markCost(u, mi, at))
		}
	}
	if sum.n == 0 {
		return nil
	}
	return sum.figure()
}

// callerLinked fills a run page's caller_linked rows (U14): the runs
// whose weft.caller.run.id names the run, oldest first, at most
// callerLinkedCap; a panel token's public id's alone, read in its scope
// (RunQuery.PublicID) so the page and caller_linked_more count what it
// may see, the rest counted in caller_linked_hidden (the unscoped
// total less the scoped one). The rows' subagent trees are primed
// before they are priced: one children read per tree level, not one
// per row.
func (s *Server) callerLinked(ctx context.Context, c *coster, id string, out *runPage) {
	pub := scopeOf(ctx)
	q := obsdb.RunQuery{CallerRunID: id, ParentRunID: "*", PublicID: pub, Oldest: true, Limit: callerLinkedCap}
	page, err := s.db.Runs(ctx, q)
	if err == nil && pub != "" {
		q.PublicID, q.Limit = "", 1
		var all obsdb.RunPage
		if all, err = s.db.Runs(ctx, q); err == nil {
			out.CallerLinkedHidden = max(all.Total-page.Total, 0)
		}
	}
	if err != nil {
		slog.DebugContext(ctx, "studio: a run's caller-linked runs could not be read", "run", id, "err", err)
		out.CallerLinkedUnknown = true
		out.CallerLinkedHidden = 0
		return
	}
	recs := page.Runs
	if len(recs) > callerLinkedCap { // the ties a page carries past its limit
		recs = recs[:callerLinkedCap]
	}
	out.CallerLinkedMore = page.Total > len(recs)
	c.primeTrees(recs)
	for _, rec := range recs {
		if !inScope(pub, rec) { // the query's scope already; kept as the wall
			out.CallerLinkedHidden++
			continue
		}
		out.CallerLinked = append(out.CallerLinked, c.row(rec))
	}
}

// childRows is a run document's children[] rows, each with its own
// runHoles (plan A10) — the run page's and the export's; kids are the
// coster's scoped ones (coster.visibleKids), never det.Children. A child whose
// holes cannot be read goes out without them: one unreadable child
// never fails its parent's page.
func (s *Server) childRows(ctx context.Context, c *coster, kids []obsdb.RunRow) []runRow {
	out := make([]runRow, 0, len(kids))
	for _, kid := range kids {
		kr := c.row(kid)
		if kh, err := s.runHoles(ctx, kid); err == nil {
			kr.Holes = kh
		}
		out = append(out, kr)
	}
	return out
}

// lostEvents is the one rule for a run none of whose events was stored
// (EventCount 0) — the run document's holes and the export's events
// block (its first) alike, in the table's order:
//
//   - gap, cause events_lost: the root span says the core emitted M
//     events (weft.run.events, U5) — "M of M events were lost — every
//     one", the counted words in place of the two guesses below;
//   - gap: the row counts request records (the records arrived, the
//     events beside them did not), or counts steps with no span to
//     have counted them (a row whose events were dropped);
//   - not_recorded, cause no_logger: spans hold the run and no record of
//     it was stored — the app exported spans only
//     (obsdb.LostRecordsHole, docs/record-contract.md §7).
//
// Nothing for a run with events, a running one (in flight) or a
// settling one (obsdb.RunRow.Settling: its records may trail its stored
// root span — decided once the window has passed). A derived run (obsdb.RunRow.Derived) has none by construction: its one
// badge is derivedBadge's — derived is a stock-span run's alone, never
// a weft run's whose records are missing.
func lostEvents(rec obsdb.RunRow, hasSpans bool) []badgeFields {
	if rec.EventCount > 0 || rec.Status == obsdb.StatusRunning || rec.Settling {
		return nil
	}
	if rec.Derived {
		return []badgeFields{derivedBadge()}
	}
	var out []badgeFields
	if h, cause := obsdb.EventsLostHole(rec); h != "" {
		// The root counts what was emitted (U5): every one was lost, in
		// numbers — the counted words replace the guesses below.
		b := badgeFor(h, cause)
		return []badgeFields{{Badge: string(h), Reason: obsdb.EventsLostReason(rec, 0), Fix: b.Fix}}
	}
	if rec.RequestCount > 0 || (rec.Steps > 0 && !hasSpans) {
		out = append(out, badgeFields{Badge: string(obsdb.HoleGap),
			Reason: "the run counts steps or model calls, but none of its events was stored: a destination dropped them",
			Fix:    holeFix(obsdb.HoleGap)})
	}
	if h, cause := obsdb.LostRecordsHole(rec, hasSpans); h != "" {
		out = append(out, badgeFor(h, cause))
	}
	return out
}

// runHoles is the run's own holes, from what obsdb can tell about the
// whole run (ADR 0028 §11; plan A3):
//
//   - derived: a run built from stock spans (obsdb.RunRow.Derived,
//     docs/record-contract.md §9) — derivedBadge's words, first, so
//     every later reason for the badge yields to it;
//   - not_recorded: no stored record beside its spans (lostEvents,
//     cause no_logger: the app exported spans only) — read first, so
//     its words win over RequestsHole's for the same badge; else
//     RequestsHole — written before the request record;
//   - interrupted: the derived status (obsdb.InterruptedAfter);
//   - gap: no stored event (lostEvents) — counted steps or requests
//     whose events were dropped;
//   - gap, cause events_lost: the root span says the core emitted M
//     events (weft.run.events, U5) and fewer are stored — "N of M events
//     were lost", split into positions missing below the last stored
//     one and the run's tail ("the last K events were lost"), the
//     counter-hole words below folded in (obsdb.EventsLostHole through
//     TailLostHole, obsdb.EventsLostReason; raised once the run stopped
//     settling);
//   - gap, cause tail_lost: a root without the count (a weft before U5)
//     ended (succeeded) with events stored and no run_finish — a
//     destination dropped the record tail (obsdb.TailLostHole's
//     fallback; raised once the run stopped settling);
//   - gap, cause messages_lost: the root span says the core emitted N
//     growth messages records (weft.run.messages) and the store holds
//     some but fewer — "K of N transcript batches were lost"
//     (obsdb.MessagesLostHole, obsdb.MessagesLostReason; none stored is
//     never called lost: a content-off destination stores none);
//   - stripped: its events came through a content-off chain, or the
//     core captured none (the run_start's weft.content: stripped or
//     none);
//   - gap: event positions missing below the run's high-water mark
//     (EventPage.Gaps) once the run is no longer running or settling —
//     a live run's missing positions may still be in flight — and only
//     where the root carries no count (the counted gap says them);
//   - derived: a playground replay whose prefix no request record
//     placed — its weft.replay.view metadata (ADR 0029 §2) is derived
//     or unknown (replayViewHole); never a child run, which inherits
//     the mark but not the prefix.
//
// One one-event read answers the last two; truncated, redacted,
// max_tokens and compacted are facts of an event or a step, badged
// there.
func (s *Server) runHoles(ctx context.Context, rec obsdb.RunRow) ([]stepHole, error) {
	holes := holeSet{}
	if rec.Derived {
		b := derivedBadge()
		holes.add(obsdb.HoleDerived, b.Reason, b.Fix)
	}
	if rec.EventCount == 0 && rec.Status != obsdb.StatusRunning {
		sp, err := s.db.RunSpans(ctx, rec.ID)
		if err != nil {
			return nil, err
		}
		for _, b := range lostEvents(rec, len(sp) > 0) {
			holes.add(obsdb.Hole(b.Badge), b.Reason, b.Fix)
		}
	}
	// The counted gap (U5, cause events_lost) is worded below, where the
	// event walk gives the positions missing under the high-water mark.
	tailHole, tailCause := obsdb.TailLostHole(rec)
	if tailHole != "" && tailCause != obsdb.CauseEventsLost {
		b := badgeFor(tailHole, tailCause)
		holes.add(tailHole, b.Reason, b.Fix)
	}
	if h := rec.RequestsHole(); h != "" {
		holes.note(h)
	}
	if rec.Status == obsdb.StatusInterrupted {
		holes.note(obsdb.HoleInterrupted)
	}
	if reason, fix := replayViewHole(rec); reason != "" {
		holes.also(obsdb.HoleDerived, reason, fix)
	}
	if n := rec.Problems.MetadataDropped; n > 0 {
		// The core's metadata limits dropped pairs (UX-E-42): the
		// table's truncated hole, cause metadata_cap, counted.
		b := badgeFor(obsdb.HoleTruncated, obsdb.CauseMetadataCap)
		holes.also(obsdb.HoleTruncated, obsdb.MetadataCapReason(n), b.Fix)
	}

	if rec.EventCount > 0 {
		page, err := s.db.Events(ctx, rec.ID, -1, 1)
		if err != nil {
			return nil, err
		}
		if len(page.Events) > 0 && (page.Events[0].Content == "stripped" || page.Events[0].Content == "none") {
			holes.note(obsdb.HoleStripped)
		}
		if tailCause == obsdb.CauseEventsLost {
			// "N of M events were lost", split into the positions missing
			// below the last stored one and the run's tail above it — one
			// badge, the counter-hole words folded in.
			below := len(page.Gaps)
			if below >= obsdb.MaxGaps {
				below = -1 // the walk was cut: the split is not known
			}
			b := badgeFor(tailHole, tailCause)
			holes.add(tailHole, obsdb.EventsLostReason(rec, below), b.Fix)
		} else if len(page.Gaps) > 0 && rec.Status != obsdb.StatusRunning && !rec.Settling {
			n := strconv.Itoa(len(page.Gaps))
			if len(page.Gaps) >= obsdb.MaxGaps {
				n += "+"
			}
			// also: a run that lost its record tail (TailLostHole's gap,
			// added above) and a mid-run batch says both reasons.
			holes.also(obsdb.HoleGap, n+" of the run's event positions are missing: a destination dropped a batch", holeFix(obsdb.HoleGap))
		}
	}
	if h, cause := obsdb.MessagesLostHole(rec); h != "" {
		// Transcript batches the root counts and the store lacks (R7-prep):
		// "K of N transcript batches were lost" — last, so an events gap
		// keeps its words first and this one joins it, never in its place.
		b := badgeFor(h, cause)
		holes.also(h, obsdb.MessagesLostReason(rec), b.Fix)
	}
	return holes.list(), nil
}

// attrReplayView is weft/runtime's mark on a playground replay from a
// step > 0 (ADR 0029 §2): transcript | compacted:<index> | derived |
// unknown — how its prefix knows what the source's model saw there.
const attrReplayView = "weft.replay.view"

// replayViewHole is a replay's derived hole when its prefix is not the
// request record's (weft.replay.view derived or unknown): the reason
// and fix the run's holes carry, "" for any other run. A truncation the
// user cannot see is a hole with a reason: the replay ran, but on a
// prefix that may not be what the source's model saw.
//
// A child run is never one: core's metadata is inherited, so a
// Subagent child of a replay — or a run its tool call started (a pool
// child session's turn, a nested Generate: caller_run_id) — carries the
// parent's weft.replay.view and weft.forked_from, but its input is its
// tool call's arguments, not a cut prefix — the hole is the parent's
// alone.
func replayViewHole(rec obsdb.RunRow) (reason, fix string) {
	if rec.ParentRunID != "" || rec.CallerRunID != "" {
		return "", ""
	}
	at := "its source step"
	if _, step, ok := strings.Cut(rec.ForkedFrom, "#"); ok && step != "" {
		at = "step " + step + " of " + strings.TrimSuffix(rec.ForkedFrom, "#"+step)
	}
	switch rec.Meta[attrReplayView] {
	case "derived":
		return "this replay's prefix was cut from the transcript at " + at + " (weft.replay.view = derived): no request record placed that step's messages, so a compaction view its model saw is not known",
			"record the source with a current weft and content on, so a request record places every step (ADR 0028)"
	case "unknown":
		return "this replay's prefix is the transcript before " + at + " (weft.replay.view = unknown): the runtime could not read that step's records (Studio unreachable, content off, or a Studio without ?step=), so a compaction view its model saw may be missing",
			"replay again with the runtime able to read the source's records: Studio reachable, content on, Studio upgraded"
	}
	return "", ""
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
		// answer done (a running, settling or interrupted row). A
		// settling row (obsdb.RunRow.Settling: its root span stored,
		// its run_finish not yet, heard from within
		// obsdb.InterruptedAfter) reads succeeded or failed already,
		// but the records behind its root may still land: done stays
		// false, and the run row's settling (served on the run doc and
		// every row) is what the UI's isLive reads to keep tailing.
		det, err := s.db.Run(r.Context(), id)
		if err != nil {
			dbError(w, r, "events of run", id, err)
			return
		}
		if det.Status != obsdb.StatusRunning && !det.Settling {
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
	if len(out.Events) == 0 && isDerivedID(id) {
		out.badgeFields = derivedBadge()
	}
	writeJSON(w, r, http.StatusOK, out)
}

// isDerivedID says an id is a derived run's (obsdb.ParseStockRunID: a
// run built from stock spans, docs/record-contract.md §9). The routes
// that do not read the run row badge an empty answer by it; the shape
// is reserved, so a weft run never carries it (§2).
func isDerivedID(id string) bool {
	_, _, ok := obsdb.ParseStockRunID(id)
	return ok
}

// derivedBadge is the badge every pane of a derived run carries in
// place of the records it cannot have — events, the transcript, the
// request records: derived, cause stock_spans ("derived from spans ·
// no events"), its fix naming the record contract.
func derivedBadge() badgeFields { return badgeFor(obsdb.HoleDerived, obsdb.CauseStockSpans) }

// refuseDerived answers 400 with derivedBadge: a route that needs what
// a derived run never recorded (a transcript to replay or fixture).
func refuseDerived(w http.ResponseWriter, r *http.Request, msg string) {
	type errBody struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	writeJSON(w, r, http.StatusBadRequest, struct {
		Error errBody `json:"error"`
		badgeFields
	}{Error: errBody{"bad_request", msg}, badgeFields: derivedBadge()})
}

// serveRunTranscript answers api/runs/{id}/transcript (S4.3): the
// messages bodies, in order, one batch per messages record — the
// replay-grade record of what the run saw and said (what replaces
// the store's result document; the fold takes finished text from
// here, because deltas are not stored). With ?step=N it also answers
// the messages step N's model call carried (transcriptAsOf, ADR 0029);
// a step the run never reached is 404, as steps/{n} answers it.
func (s *Server) serveRunTranscript(w http.ResponseWriter, r *http.Request, id string) {
	if !s.scopeRunID(w, r, id) {
		return
	}
	step := -1
	if q := r.URL.Query(); q.Has("step") {
		n, err := strconv.Atoi(q.Get("step"))
		if err != nil || n < 0 || strconv.Itoa(n) != q.Get("step") {
			badRequest(w, r, "step must be a non-negative integer ordinal")
			return
		}
		step = n
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
	if len(out.Batches) == 0 && isDerivedID(id) {
		// A derived run has no messages records by construction: the
		// bare route says so; a step's messages are refused with the
		// badge, as a step whose records are missing is (409).
		if step >= 0 {
			conflict(w, r, fmt.Sprintf("run %s was derived from stock spans: no messages record holds step %d's", id, step), derivedBadge())
			return
		}
		writeJSON(w, r, http.StatusOK, struct {
			transcript
			badgeFields
		}{out, derivedBadge()})
		return
	}
	if step < 0 {
		writeJSON(w, r, http.StatusOK, out)
		return
	}
	sm, err := obsdb.MessagesAsOf(r.Context(), s.db, id, step)
	var se *obsdb.StepMessagesError
	switch {
	case errors.As(err, &se):
		// The hole by cause: gap (a record the request names is missing
		// or does not fit), stripped (no messages were captured).
		conflict(w, r, err.Error(), badgeOf(se.Hole))
		return
	case errors.Is(err, obsdb.ErrNotFound):
		notFound(w, r, fmt.Sprintf("no step %d of run %s", step, id))
		return
	case err != nil:
		dbError(w, r, "transcript of run", id, err)
		return
	}
	doc := transcriptAsOf{transcript: out, Step: step, Messages: sm.Messages}
	if doc.Messages == nil {
		doc.Messages = []core.Message{}
	}
	if sm.Derived {
		doc.badgeFields = badgeOf(obsdb.HoleDerived)
	}
	if sm.View != nil {
		doc.CompactedAt = noteOf(*sm.View)
		if !readsPrompts(r) {
			// A view's messages are the request's content (the export's
			// rule, export.go's compactionsBlock): a read-scoped panel
			// token gets the counts and hash, the messages null under
			// the hidden badge.
			doc.Messages, doc.badgeFields = nil, badgeOf(obsdb.HoleHidden)
		}
	}
	writeJSON(w, r, http.StatusOK, doc)
}

// serveRunSpans answers api/runs/{id}/spans (S4.3): the run's timed
// spans — the chat calls, tool executions and the invoke_agent span
// itself, with times — each with run_id the run's. With tree=1 (U9.1,
// capability spans_tree: obsdb.TreeReader) the spans of every
// descendant run come beside them — the subagent tree, by the
// weft.parent.run.id chain and core's id rule, found from the run rows
// and read by run id (obsdb.TreeReader.RunTreeSpans) — each keyed by
// run_id + span_id with its run's parent_run_id, in start order, at
// most obsdb.TreeSpanCap, with the tree's holes (treeSpansDoc). A
// panel token reads the run's own spans and the descendants of its
// public id (obsdb.TreeQuery.PublicID: cut, then scoped, then capped,
// so a hidden run never takes a visible one's place under the cap):
// the others are left out under a hidden hole, never a 403 for the
// whole tree.
func (s *Server) serveRunSpans(w http.ResponseWriter, r *http.Request, id string) {
	tree := false
	if v := r.URL.Query().Get("tree"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			badRequest(w, r, "tree must be true or false (1 or 0)")
			return
		}
		tree = b
	}
	tr, canTree := s.db.(obsdb.TreeReader)
	if tree && !canTree {
		writeError(w, r, http.StatusNotImplemented, "not_implemented", offSpansTree)
		return
	}
	if !s.scopeRunID(w, r, id) {
		return
	}
	if !tree {
		list, err := s.db.RunSpans(r.Context(), id)
		if err != nil {
			dbError(w, r, "spans of run", id, err)
			return
		}
		doc := spans(spansFor(r, list))
		for i := range doc.Spans {
			doc.Spans[i].RunID = id
		}
		writeJSON(w, r, http.StatusOK, doc)
		return
	}
	q := obsdb.TreeQuery{RunID: id}
	if p := idFrom(r).panel; p != nil {
		q.PublicID = p.PublicID
	}
	rt, err := tr.RunTreeSpans(r.Context(), q)
	if err != nil {
		dbError(w, r, "span tree of run", id, err)
		return
	}
	list := rt.Spans
	plain := make([]obsdb.Span, len(list))
	for i, sp := range list {
		plain[i] = sp.Span
	}
	doc := treeSpansDoc{Spans: spans(spansFor(r, plain)).Spans, Holes: []treeHole{}}
	for i := range doc.Spans {
		doc.Spans[i].RunID, doc.Spans[i].ParentRunID = list[i].RunID, list[i].ParentRunID
	}
	if rt.Truncated {
		doc.Holes = append(doc.Holes, treeHole{
			Hole: string(obsdb.HoleTruncated), Cause: string(obsdb.CauseResponseCap),
			Reason: fmt.Sprintf("this response reads at most %d spans of the run tree and the tree holds more: the later spans were not read, nothing was lost", obsdb.TreeSpanCap),
			Fix:    "open a child run's own tree (runs/{id}/spans?tree=1 on the child), or its spans (runs/{id}/spans)",
		})
	}
	if rt.TraceTruncated {
		doc.Holes = append(doc.Holes, treeHole{
			Hole: string(obsdb.HoleTruncated), Cause: string(obsdb.CauseResponseCap),
			Reason: fmt.Sprintf("this run's spans carry no run id, so its tree is cut from its trace, and this response reads at most the trace's first %d spans: a span of the tree after them was not read, nothing was lost", obsdb.TreeTraceCap),
			Fix:    "open the trace (traces/{trace_id}), or a child run's own tree",
		})
	}
	if rt.MissingCount > 0 {
		_, fix := obsdb.HoleNoteFor(obsdb.HoleNotRecorded, obsdb.CauseNoSpans)
		runs := make([]string, len(rt.Missing))
		for i, m := range rt.Missing {
			runs[i] = m.ID
		}
		doc.Holes = append(doc.Holes, treeHole{
			Hole: string(obsdb.HoleNotRecorded), Cause: string(obsdb.CauseNoSpans),
			Reason: missingRunsReason(rt.MissingCount, len(runs)), Fix: fix, Runs: runs, Count: rt.MissingCount,
		})
	}
	if rt.Hidden {
		doc.Holes = append(doc.Holes, treeHole{
			Hole:   string(obsdb.HoleHidden),
			Reason: hiddenTreeReason(rt.HiddenCount),
			Fix:    "open the tree with the server (dev) token",
			Count:  rt.HiddenCount,
		})
	}
	writeJSON(w, r, http.StatusOK, doc)
}

// treeSpansDoc is api/runs/{id}/spans?tree=1: the tree's spans and its
// holes (U9.1-fix) — truncated (cause response_cap) past
// obsdb.TreeSpanCap spans, and again, for a derived run, past the
// trace's first obsdb.TreeTraceCap; not_recorded (cause no_spans, runs
// naming the first obsdb.TreeMissingCap, count all of them) for
// descendant runs with a row, no span and no longer running; hidden
// for the runs a panel token's scope left out. Always a list, empty
// when the tree is whole.
type treeSpansDoc struct {
	Spans []spanDTO  `json:"spans"`
	Holes []treeHole `json:"holes"`
}

// treeHole is one hole of a run tree: the A3 table's badge and cause,
// the reader's reason with the tree's numbers in it, the table's fix
// where it has one, and the runs it names with their count (no_spans
// only: runs at most obsdb.TreeMissingCap, count every one).
type treeHole struct {
	Hole   string   `json:"hole"`
	Cause  string   `json:"cause,omitempty"`
	Reason string   `json:"reason"`
	Fix    string   `json:"fix,omitempty"`
	Runs   []string `json:"runs,omitempty"`
	Count  int      `json:"count,omitempty"`
}

// missingRunsReason is the no_spans hole's reason with the count, and
// how many of them runs names when the cap cut the list.
func missingRunsReason(n, named int) string {
	runs := "runs have"
	if n == 1 {
		runs = "run has"
	}
	out := fmt.Sprintf("%d finished descendant %s a row and no span in this tree: it ran without a tracer, or a destination dropped its spans", n, runs)
	if named < n {
		out += fmt.Sprintf(" (the first %d by start are named)", named)
	}
	return out
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
	doc := spans(spansFor(r, list))
	if !scopeSpans(w, r, doc.Spans) {
		return
	}
	writeJSON(w, r, http.StatusOK, doc)
}

// serveSessions answers api/sessions (S4.2): public_id and agent
// filters, awaiting / failed / compacted / status / lineage / since
// (obsdb.SessionQuery's turn-state filters, U16), q (obsdb.SessionQuery.Q: every term in the title, the first
// turn's prompt, the id, the public id or the agent), tag.<k>=<v>
// (obsdb.SessionQuery.Meta: the sessions one of whose own turns carries
// every pair — thread.WithRunMetadata's tags; U12.2, D6), before (RFC
// 3339, on last-seen) with before_id (the cursor's session id) and
// limit.
func (s *Server) serveSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := obsdb.SessionQuery{Agent: q.Get("agent"), PublicID: q.Get("public_id"), Q: strings.TrimSpace(q.Get("q")), Meta: tagParams(q)}
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
	// The turn-state filters (U16, UX-B-28): awaiting / failed /
	// compacted booleans, lineage child|fork, since (RFC 3339, on
	// last-seen).
	for _, f := range []struct {
		key string
		dst *bool
	}{{"awaiting", &query.Awaiting}, {"failed", &query.Failed}, {"compacted", &query.Compacted}} {
		if v := q.Get(f.key); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				badRequest(w, r, f.key+" must be true or false (1 or 0)")
				return
			}
			*f.dst = b
		}
	}
	switch st := obsdb.Status(q.Get("status")); st {
	case "", obsdb.StatusRunning, obsdb.StatusSucceeded, obsdb.StatusFailed, obsdb.StatusInterrupted:
		query.Status = st
	default:
		badRequest(w, r, "status must be running, succeeded, failed or interrupted (a session's newest turn; awaiting=1 is its parked)")
		return
	}
	switch v := q.Get("lineage"); v {
	case "", obsdb.LineageChild, obsdb.LineageFork:
		query.Lineage = v
	default:
		badRequest(w, r, "lineage must be child or fork")
		return
	}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			badRequest(w, r, "since must be RFC 3339: "+err.Error())
			return
		}
		query.Since = t
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
	// A panel token's page is scoped in the query (SessionQuery.PublicID
	// is a WHERE on the runs, before the grouping): each row's turns,
	// usage, first/last seen and status, the order, Total and the
	// cursor are its public id's runs alone — a run of another app
	// sharing the session id (a colliding derived run) is in none of
	// them (docs/record-contract.md §9). The cost walk reads the same
	// scope — the query's public id, the panel token's or the server
	// token's ?public_id= — so a row is one scope throughout: turns,
	// usage and cost.
	out := sessionsPage{Total: page.Total, Sessions: make([]sessionRow, 0, len(page.Sessions))}
	pub := query.PublicID
	for _, sr := range page.Sessions {
		sc, _, _ := s.sessionCost(r.Context(), sr.ID, pub)
		srow := sessRow(sr)
		srow.costFields = sc.fields()
		out.Sessions = append(out.Sessions, srow)
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
// in order (obsdb's SessionDetail); /api/sessions/{id}/public_id is the
// reverse of api/public (serveSessionPublicID); /timeline and /entries
// are the session's state log (timeline.go, U12.2); /export the whole
// conversation as JSON lines (session_export.go). Session ids carry no
// slash, so the sub-route shadows none.
func (s *Server) serveSessionRoutes(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if sid, ok := strings.CutSuffix(id, "/public_id"); ok && sid != "" && !strings.Contains(sid, "/") {
		s.serveSessionPublicID(w, r, sid)
		return
	}
	for ext, serve := range map[string]func(http.ResponseWriter, *http.Request, string){
		"/timeline": s.serveSessionTimeline,
		"/entries":  s.serveSessionEntries,
		"/export":   s.serveSessionExport, // UX-B-29, session_export.go
	} {
		if sid, ok := strings.CutSuffix(id, ext); ok && sid != "" && !strings.Contains(sid, "/") {
			serve(w, r, sid)
			return
		}
	}
	if id == "" || strings.Contains(id, "/") {
		notFound(w, r, "no such api route "+r.URL.Path)
		return
	}
	pub := panelPublicID(r)
	var det obsdb.SessionDetail
	if er, ok := s.db.(obsdb.SessionEventReader); ok {
		// The timeline's read (sessionScopeFor): a session no turn of
		// which is stored still answers when its state log has marks —
		// session-level records alone, every one held at Close — so the
		// plain route agrees with its /timeline (P9-pre-web-fix2 10).
		if det, ok = s.sessionScopeFor(w, r, er, id); !ok {
			return
		}
	} else {
		if !s.scopeSessionID(w, r, id) {
			return
		}
		var err error
		if det, err = s.db.Session(r.Context(), id); err != nil {
			sessionReadError(w, r, id, err)
			return
		}
		if pub != "" {
			// A panel token's session is its public id's turns (the
			// runs list's rule): a run of another app sharing the
			// session id — a derived run whose gen_ai.conversation.id
			// collides — is neither listed nor summed
			// (docs/record-contract.md §9). sessionScopeFor does the same.
			s.scopeSessionDetail(r.Context(), &det, pub)
		}
	}
	doc := sessionDoc{sessionRow: sessRow(det.SessionRow), Runs: make([]runRow, 0, len(det.Runs))}
	sc, c, _ := s.sessionCost(r.Context(), id, pub)
	doc.costFields = sc.fields()
	// The turn rows' children counts: every parent-linked run, as on
	// the runs list — the session walk primed its own runs only.
	c.primeChildren(det.Runs)
	for _, rr := range det.Runs {
		doc.Runs = append(doc.Runs, c.row(rr))
	}
	s.delegatedSessions(r.Context(), id, pub, &doc)
	writeJSON(w, r, http.StatusOK, doc)
}

// delegatedSessions fills a session document's delegated block (U14):
// its direct delegated sessions as rows (SessionQuery.Parents, the
// panel token's public id's alone — the list's scope), newest first,
// the first delegatedCap of them, and the usage and cost of every
// session delegated under it, walked breadth first — one paged
// Sessions read per tree level (Parents IN the level), never one per
// session, and one paged Runs read per level for the level's costs
// (RunQuery.SessionIDs, sessionCosts) — up to delegatedTreeCap
// sessions: past it, or when a read fails, the delegated cost is not
// known and the reason says why (the usage then a floor, said in the
// same reason). The direct level is paged whole, so a session
// delegating more than delegatedCap still has every one of them in its
// figures.
func (s *Server) delegatedSessions(ctx context.Context, id, pub string, doc *sessionDoc) {
	doc.Delegated = []sessionRow{}
	seen := map[string]bool{id: true}
	level := []string{id}
	var parts []runCost
	var why string
	walked := 0
	hidden := 0              // the sessions a panel token's scope left out, every level's
	var hiddenFront []string // the last level's hidden sessions: their own delegated ones are hidden too
	hiddenBudget := delegatedTreeCap
	hiddenFloor := false // hiddenBudget ran out: hidden is a floor
	allExact := true     // every visited session's own cost reported its cache split exact
	// price costs one level's sessions in one batched read and folds
	// them into the figures, in the order the level listed them.
	price := func(depth int, rows []obsdb.SessionRow) {
		if len(rows) == 0 {
			return
		}
		ids := make([]string, len(rows))
		for i, sr := range rows {
			ids[i] = sr.ID
		}
		costs := s.sessionCosts(ctx, ids, pub)
		for _, sr := range rows {
			sc := costs[sr.ID]
			doc.DelegatedUsage = doc.DelegatedUsage.Add(sr.Usage)
			allExact = allExact && sc.cache == cacheExact
			parts = append(parts, runCost{id: sr.ID, own: sc, delegated: cost{known: true}})
			if depth == 0 && len(doc.Delegated) < delegatedCap {
				row := sessRow(sr)
				row.costFields = sc.fields()
				doc.Delegated = append(doc.Delegated, row)
			}
		}
	}
walk:
	for depth := 0; len(level) > 0; depth++ {
		var next []string
		var rows []obsdb.SessionRow
		q := obsdb.SessionQuery{Parents: level, PublicID: pub, Limit: 500}
		scopedTotal := -1 // this level's total in the request's scope, once read
		for {
			page, err := s.db.Sessions(ctx, q)
			if err != nil {
				slog.DebugContext(ctx, "studio: a session's delegated sessions could not be read", "session", id, "depth", depth, "err", err)
				if depth == 0 {
					doc.DelegatedUnknown = true
				}
				why = fmt.Sprintf("the sessions delegated %d level(s) under this one could not be read; delegated_usage stops at the levels read", depth+1)
				price(depth, rows)
				break walk
			}
			if scopedTotal < 0 {
				scopedTotal = page.Total
				// The level's sessions the scope left out — a visible
				// child's own delegated session under another public id
				// as much as a direct one, and every session under a
				// hidden one (P9-pre-web-fix3 F6).
				n, front, floor := s.hiddenDelegated(ctx, id, level, hiddenFront, pub, scopedTotal, &hiddenBudget)
				hidden += n
				hiddenFront, hiddenFloor = front, hiddenFloor || floor
			}
			if depth == 0 {
				doc.DelegatedMore = page.Total > delegatedCap
			}
			for _, sr := range page.Sessions {
				if seen[sr.ID] {
					continue
				}
				seen[sr.ID] = true
				if walked == delegatedTreeCap {
					why = fmt.Sprintf("more than %d sessions are delegated under this one: too many to price at read time; delegated_usage counts the first %d sessions", delegatedTreeCap, delegatedTreeCap)
					price(depth, rows)
					break walk
				}
				walked++
				rows = append(rows, sr)
				next = append(next, sr.ID)
			}
			if page.NextBefore == nil || len(page.Sessions) == 0 {
				break
			}
			q.Before, q.BeforeID = *page.NextBefore, page.NextBeforeID
		}
		price(depth, rows)
		level = next
	}
	// Below the last visible level, the hidden sessions' own delegated
	// sessions: counted, never read past their ids. A walk that stopped
	// early (a failed read, the cap) leaves level set: its reason says so.
	for len(level) == 0 && len(hiddenFront) > 0 {
		n, front, floor := s.hiddenDelegated(ctx, id, nil, hiddenFront, pub, 0, &hiddenBudget)
		hidden += n
		hiddenFront, hiddenFloor = front, hiddenFloor || floor
	}
	if hidden > 0 {
		h := hiddenDelegatedHole(hidden, hiddenFloor)
		doc.DelegatedHoles = append(doc.DelegatedHoles, h)
		if why != "" {
			why += "; "
		}
		why += h.Reason
	}
	if len(parts) == 0 && why == "" {
		zero := 0.0
		doc.delegatedSessionsFields = delegatedSessionsFields{CostUSD: &zero, CostKnown: true}
		return
	}
	total := tally(parts, "delegated session", false)
	if why != "" {
		total.known, total.usd = false, 0
		if total.reason != "" {
			why += "; " + total.reason
		}
		total.reason = why
	}
	out := delegatedSessionsFields{CostKnown: total.known, CostReason: total.reason, Cache: cacheNotReported}
	if allExact {
		out.Cache = cacheExact
	}
	if out.CostKnown && out.Cache == cacheNotReported && out.CostReason == "" {
		out.CostReason = cacheNote // fields()' rule
	}
	if total.known {
		v := round6(total.usd)
		out.CostUSD = &v
	}
	doc.delegatedSessionsFields = out
}

// hiddenDelegated is how many sessions one level of id's delegated
// tree a panel token's scope left out: the sessions level (the visible
// sessions of the level above) and hiddenFront (its hidden ones)
// delegated, less scopedTotal, level's children in the scope
// (callerLinked's rule) — one Limit 1 read — and, when some are hidden,
// their ids (pages of 500, the ids alone: never shown), the hidden
// parents of the next level, at most *budget over the whole walk
// (delegatedTreeCap): past it floor, the deeper levels not counted. A
// session under a hidden one is hidden whatever its public id. 0 for
// an unscoped read, or when the unscoped read failed (logged; the
// walk's own error says a failed scoped read).
func (s *Server) hiddenDelegated(ctx context.Context, id string, level, hiddenFront []string, pub string, scopedTotal int, budget *int) (n int, front []string, floor bool) {
	parents := append(slices.Clone(level), hiddenFront...)
	if pub == "" || scopedTotal < 0 || len(parents) == 0 {
		return 0, nil, false
	}
	q := obsdb.SessionQuery{Parents: parents, Limit: 1}
	all, err := s.db.Sessions(ctx, q)
	if err != nil {
		slog.DebugContext(ctx, "studio: a session's unscoped delegated count could not be read", "session", id, "err", err)
		return 0, nil, false
	}
	n = max(all.Total-scopedTotal, 0)
	if n == 0 {
		return 0, nil, false
	}
	underHidden := make(map[string]bool, len(hiddenFront))
	for _, h := range hiddenFront {
		underHidden[h] = true
	}
	q.Limit = 500
	for {
		page, err := s.db.Sessions(ctx, q)
		if err != nil {
			slog.DebugContext(ctx, "studio: a session's hidden delegated sessions could not be read", "session", id, "err", err)
			return n, front, true
		}
		for _, sr := range page.Sessions {
			if sr.PublicID == pub && !underHidden[sr.ParentSession] {
				continue
			}
			if *budget == 0 {
				return n, front, true
			}
			*budget--
			front = append(front, sr.ID)
		}
		if page.NextBefore == nil || len(page.Sessions) == 0 {
			return n, front, false
		}
		q.Before, q.BeforeID = *page.NextBefore, page.NextBeforeID
	}
}

// hiddenDelegatedHole is the hidden hole of a session document whose
// n delegated sessions a panel token's scope left out: counted, never
// named — at least n (count_floor) when the hidden subtrees were more
// than the walk counts.
func hiddenDelegatedHole(n int, floor bool) stepHole {
	r := fmt.Sprintf("%d delegated sessions carry another public id, or none: a panel token reads its own public id's sessions, so they are left out and the delegated figures do not cover them", n)
	switch {
	case floor:
		r = fmt.Sprintf("at least %d delegated sessions carry another public id, or none, or sit under one (past %d hidden ones the deeper levels are not counted): a panel token reads its own public id's sessions, so they are left out and the delegated figures do not cover them", n, delegatedTreeCap)
	case n == 1:
		r = "1 delegated session carries another public id, or none: a panel token reads its own public id's sessions, so it is left out and the delegated figures do not cover it"
	}
	return stepHole{Hole: string(obsdb.HoleHidden), Reason: r, Fix: "open the session with the server (dev) token", Count: n, CountFloor: floor}
}

// panelPublicID is the request's panel token's public id, "" for any
// other identity.
func panelPublicID(r *http.Request) string {
	return scopeOf(r.Context())
}

// withPublicID keeps the runs carrying pub.
func withPublicID(runs []obsdb.RunRow, pub string) []obsdb.RunRow {
	out := make([]obsdb.RunRow, 0, len(runs))
	for _, rr := range runs {
		if rr.PublicID == pub {
			out = append(out, rr)
		}
	}
	return out
}

// scopeSessionDetail narrows a session's detail to a panel token's
// public id: its turns (withPublicID) and its row (scopedSessionRow).
// The row's summarizer_usage is obsdb's over every run of the session
// id; when a turn of another public id shares it (a colliding derived
// run), the figure and its per-model split are re-read in pub's scope —
// the session list's own query by id (SessionQuery.IDs, PublicID) — and
// are absent when that read fails or finds nothing, never another
// scope's.
func (s *Server) scopeSessionDetail(ctx context.Context, det *obsdb.SessionDetail, pub string) {
	all := len(det.Runs)
	det.Runs = withPublicID(det.Runs, pub)
	det.SessionRow = scopedSessionRow(det.SessionRow, det.Runs, pub)
	if len(det.Runs) == all {
		return
	}
	det.SummarizerUsage, det.SummarizerModels = core.Usage{}, nil
	page, err := s.db.Sessions(ctx, obsdb.SessionQuery{IDs: []string{det.ID}, PublicID: pub, Limit: 1})
	if err != nil {
		return
	}
	for _, sr := range page.Sessions {
		if sr.ID == det.ID {
			det.SummarizerUsage, det.SummarizerModels = sr.SummarizerUsage, sr.SummarizerModels
			return
		}
	}
}

// scopedSessionRow recomputes a session row over the turns a panel
// token sees (turns, those with pub only): obsdb's SessionRow is a
// GROUP BY over every run of the session id, which a run of another
// app can share. The same aggregates as the database's: the turn
// count, first/last seen, summed usage, MAX(agent), and the newest
// turn's status (by turn, then start), the newest titled turn's title
// and the first turn's prompt preview and content-off mark. The
// summarizer usage and its per-model split are row's, carried as is:
// not turn aggregates (scopeSessionDetail re-reads them when row's
// scope is wider than pub's).
func scopedSessionRow(row obsdb.SessionRow, turns []obsdb.RunRow, pub string) obsdb.SessionRow {
	out := obsdb.SessionRow{ID: row.ID, PublicID: pub, SummarizerUsage: row.SummarizerUsage, SummarizerModels: row.SummarizerModels}
	var newest, first, titled *obsdb.RunRow
	later := func(a, b *obsdb.RunRow) bool { // a after b, by turn then start
		return a.Turn > b.Turn || (a.Turn == b.Turn && a.Started.After(b.Started))
	}
	for i := range turns {
		t := &turns[i]
		if t.PublicID != pub {
			continue
		}
		out.Turns++
		out.Usage = out.Usage.Add(t.Usage)
		if t.Agent > out.Agent {
			out.Agent = t.Agent
		}
		if out.FirstSeen.IsZero() || t.Started.Before(out.FirstSeen) {
			out.FirstSeen = t.Started
		}
		if t.LastSeen.After(out.LastSeen) {
			out.LastSeen = t.LastSeen
		}
		if newest == nil || later(t, newest) {
			newest = t
		}
		if first == nil || later(first, t) {
			first = t
		}
		if t.Meta[obsdb.SessionTitleKey] != "" && (titled == nil || later(t, titled)) {
			titled = t
		}
	}
	if newest != nil {
		out.Status = newest.Status
	}
	if first != nil {
		out.FirstPrompt = first.PromptPreview
		// Gated like lib/title.ts's contentOff: a first turn still
		// running or settling may yet store its messages records.
		out.ContentOff = obsdb.SessionContentOff(*first)
	}
	if titled != nil {
		out.Title = titled.Meta[obsdb.SessionTitleKey]
	}
	// The turn facts (U16) over the same turns: the database's rule.
	scoped := make([]obsdb.RunRow, 0, out.Turns)
	for _, t := range turns {
		if t.PublicID == pub {
			scoped = append(scoped, t)
		}
	}
	obsdb.SessionTurnFacts(&out, scoped)
	return out
}

// sessionPublicID is api/sessions/{id}/public_id's answer: the public
// id the session was created with, or "" with the not_recorded badge
// when no turn of it carries one.
type sessionPublicID struct {
	SessionID string `json:"session_id"`
	PublicID  string `json:"public_id"`
	badgeFields
}

// serveSessionPublicID answers api/sessions/{id}/public_id (plan C4):
// the reverse of api/public — the weft.public_id the session's turns
// carry (thread.PublicID at Create stamps one value on every turn).
// The public id is MAX(weft.public_id) over the session's top-level
// turns (obsdb's SessionRow.PublicID, the sessions?public_id= filter's
// value): one value per thread session; a session whose turns carry
// several (core.Metadata, a non-Go OTLP app) answers the greatest,
// unbadged — and may then disagree with api/public, which resolves a
// public id to its newest session.
// The dev token's alone: a panel token is scoped to one public id and
// must not learn another's, so every panel token (read or playground,
// its own session included) is 403 with the hidden badge before the
// session is looked up — the refusal never says whether it exists.
func (s *Server) serveSessionPublicID(w http.ResponseWriter, r *http.Request, id string) {
	if idFrom(r).panel != nil {
		refuseHiddenFor(w, r, "the session-to-public-id lookup answers the dev token only, never a panel token", obsdb.CauseDevTokenOnly)
		return
	}
	det, err := s.db.Session(r.Context(), id)
	if err != nil {
		dbError(w, r, "session", id, err)
		return
	}
	out := sessionPublicID{SessionID: id, PublicID: det.PublicID}
	if out.PublicID == "" {
		out.badgeFields = badgeFor(obsdb.HoleNotRecorded, obsdb.CauseNoPublicID)
	}
	writeJSON(w, r, http.StatusOK, out)
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
