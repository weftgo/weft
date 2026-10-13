package obsdb

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/weftgo/weft/core"
)

// DB is the observability database every backend implements (sqlite by
// default, clickhouse hosted). Implementations must be safe for
// concurrent use: writers are rare (the local sink's synchronous
// processors), readers many (Studio).
//
// DeleteRun, DeleteSession, Sweep and Vacuum are the run tables' alone.
// A CommandStore's rows — commands, run routes, breakpoints, runtimes —
// are Studio's record of what it asked a runtime to do, not run data:
// deleting a run leaves the commands and the route that name it, and
// CommandStore.PruneCommands is their only retention.
type DB interface {
	// Write stores one batch, idempotent on (run, record kind, pos)
	// and (trace, span): a retried transport's duplicate is a no-op.
	// Deltas are counted, never stored; heartbeats are never stored —
	// they only move the run's last-seen (DeriveStatus's clock).
	Write(ctx context.Context, b Batch) error

	Runs(ctx context.Context, q RunQuery) (RunPage, error)
	Run(ctx context.Context, id string) (RunDetail, error) // ErrNotFound
	// Events pages a run's durable events in position order. after is
	// exclusive — pass -1 to read from the start (0 would skip position
	// 0, the run_start) and EventPage.NextAfter to continue. limit: 0 =
	// 100, max 1000.
	Events(ctx context.Context, runID string, after int64, limit int) (EventPage, error)
	// Transcript returns the messages bodies in index order, one per
	// growth messages record (a compaction view, ADR 0028 §8, is not
	// transcript: see Compactions), read through DedupTranscript — so
	// they concatenate to the transcript the run held.
	Transcript(ctx context.Context, runID string) ([]json.RawMessage, error)
	// TranscriptBatches is Transcript with what each messages record
	// stored beside its body: its index, the step it joined
	// (weft.step.index, -1 when the record carried none) and whether
	// it is the run's input (weft.messages.input). The bodies are
	// Transcript's, through DedupTranscript, one per record.
	TranscriptBatches(ctx context.Context, runID string) ([]TranscriptBatch, error)
	// Compactions returns the compactions a run's records name (ADR
	// 0028 §8): every run-scope view in index order, then the session
	// markers thread filed under the run, in emission order — what a reader needs to draw the compaction and
	// rebuild a request's messages. Transcript and TranscriptBatches
	// never include a view: the plain transcript is growth records
	// only. A messages record with an unknown weft.messages.reason is
	// an error. ErrNotFound for an unknown run; empty, not an error,
	// for a run that never compacted.
	Compactions(ctx context.Context, runID string) ([]Compaction, error)

	// The request record (ADR 0028). Requests pages a run's request
	// records in index order, one per model-call attempt
	// (RequestQuery). Prompt and Tools return the run's prompt or tools
	// record of one hash: ErrNotFound when the run holds none, as a
	// *HoleError when the reason is known (not_recorded, stripped,
	// gap — ExplainMissing). Catalogs returns every tools record of the
	// run, one per hash, in index order; a content-off run has none.
	// All four answer ErrNotFound for an unknown run.
	Requests(ctx context.Context, runID string, q RequestQuery) ([]RequestRecord, error)
	Prompt(ctx context.Context, runID, hash string) (PromptRecord, error)
	Tools(ctx context.Context, runID, hash string) (ToolsRecord, error)
	Catalogs(ctx context.Context, runID string) ([]ToolsRecord, error)
	RunSpans(ctx context.Context, runID string) ([]Span, error)
	// OtherLogs pages a run's app log records — the non-weft records
	// the writers store beside weft's (other_logs on SQLite, otel_logs
	// on ClickHouse), attributed to the run through the spans they were
	// emitted under — in time order (LogQuery, ReadOtherLogs), with
	// what the read could not show (LogPage: partial while running,
	// lines under a never-stored span, the candidate cap).
	// ErrNotFound for an unknown run; empty Logs, not nil, for a run
	// with no app logs; a *HoleError (Kind "logs", HoleNotRecorded) for
	// a finished run with no span to attribute them through.
	OtherLogs(ctx context.Context, runID string, q LogQuery) (LogPage, error)
	// CountLogs counts a run's app log records at or above minSeverity
	// (an OTLP severity number; 13 is WARN) with OtherLogs'
	// attribution, in one scan the backend narrows by severity
	// (CountOtherLogs): the run document's log_warn / log_error without
	// paging every line's body. Capped past MaxLogCandidates such lines
	// (the counts are floors). OtherLogs' errors.
	CountLogs(ctx context.Context, runID string, minSeverity int) (LogCounts, error)
	Trace(ctx context.Context, traceID string) ([]Span, error) // empty, not an error, for an unknown trace
	// Traces lists traces — weft and non-weft alike, one row per trace
	// id the spans table holds — newest first (TraceQuery, TracePage):
	// the row is computed from the trace's spans alone, so a trace
	// whose root never arrived is listed with Root nil.
	Traces(ctx context.Context, q TraceQuery) (TracePage, error)

	Sessions(ctx context.Context, q SessionQuery) (SessionPage, error)
	Session(ctx context.Context, id string) (SessionDetail, error)
	ResolvePublicID(ctx context.Context, publicID string) (sessionID string, err error)

	// The playground's saved experiments (WEFT-PLAYGROUND §10.4,
	// PQ4): the definition rows keyed by the weft.experiment.id the
	// runs carry — one small table beside runs.
	SaveExperiment(ctx context.Context, e Experiment) error // upsert by ID
	Experiments(ctx context.Context) ([]Experiment, error)  // newest update first
	Experiment(ctx context.Context, id string) (Experiment, error)

	// Deletion and retention (plan A7, §13.13). Every one touches the
	// observability tables alone — never another module's tables
	// sharing the file (thread's sessions in one SQLite file stay
	// byte-identical) and never the experiments table (its definitions
	// name runs, they do not hold them).
	//
	// DeleteRun removes a run and its subagent children — every run
	// whose ParentRunID chain reaches id — with everything stored under
	// them: records (deltas kept by KeepDeltas included), the search
	// index rows, the spans named by DeletedSpans and the app log
	// records emitted under those spans; the CommandStore rows
	// (commands, run_routes, breakpoints, runtimes) are never touched.
	// The derived run of a stock agent under the run's tool goes too
	// (docs/record-contract.md §9): it is no child of the run — the
	// derivation stops at weft spans — but its root is among the spans
	// deleted, and its row would otherwise outlive every span it was
	// derived from (StockRunsOf).
	// ErrNotFound for an unknown id.
	// A run still being written is deleted as it stands: a later write
	// for it starts a new run row from what that write carries.
	DeleteRun(ctx context.Context, id string) error
	// DeleteSession is DeleteRun for every run carrying the session id
	// (weft.session.id), children included. The app's own conversation
	// store is not obsdb's and is never touched. ErrNotFound when no
	// run carries the id.
	DeleteSession(ctx context.Context, id string) error
	// Sweep deletes, as DeleteRun does, the runs whose LastSeen is
	// before the cutoff and whose status is not running (succeeded,
	// failed or interrupted — a run silent past InterruptedAfter is
	// interrupted, so it counts as not running and is swept), with
	// their children that are not running either; a running child keeps
	// its own subtree and is swept on its own once it ends. Until then
	// that child of a swept parent is listed only under RunQuery's
	// ParentRunID "*" (the API's all=1 / parent=*), never among the
	// top-level runs. It works in passes of at most SweepBatch roots
	// (each with its subtree), so a large backlog never holds the
	// writer for the whole sweep; ctx is checked between passes. It
	// returns how many runs it deleted, children and the derived runs
	// DeleteRun takes beside them counted, over every pass — on an
	// error, the passes before it stay deleted.
	Sweep(ctx context.Context, before time.Time) (int, error)
	// Vacuum gives the space deletions freed back: SQLite runs VACUUM
	// (the whole file is rewritten, other modules' tables with their
	// contents unchanged); ClickHouse runs OPTIMIZE TABLE … FINAL on
	// the tables deletions mutate, which merges every part — an
	// operator's command, heavy on a large database.
	Vacuum(ctx context.Context) error

	Close() error
}

// TranscriptBatch is one messages record as stored (ADR 0028 §8):
// readers place a batch by its Step, never by its neighbours. Step is
// -1 when the record carried no weft.step.index — a ClickHouse row
// written before migration 0004, or a producer that never stamped it;
// a reader that places such a batch anyway infers the step and says
// so (the derived badge, ADR 0028 §11). Input is the record's
// weft.messages.input: the input record — on a partial resume a prefix
// of what the run was fed, ending at the last assistant message with
// tool calls; the tail (the rebuilt tool message and what follows it)
// is the next batch, at step 0, not flagged. A backend that did not
// store the flag for a row (ClickHouse rows written before
// weft_records.Input existed) infers Input on index 0 — true unless
// the body is a lone assistant message, which is step 0 of a run fed
// no messages (the core writes the input record first, and none for an
// empty input) — and sets InputDerived on that batch so readers badge
// it HoleDerived.
type TranscriptBatch struct {
	Index        int64
	Step         int
	Input        bool
	InputDerived bool // Input was inferred by the backend, not read from the record
	Messages     json.RawMessage
}

// TranscriptBodies returns the batches' bodies in order — Transcript's
// answer, for a backend that reads both through one query.
func TranscriptBodies(batches []TranscriptBatch) []json.RawMessage {
	if batches == nil {
		return nil
	}
	out := make([]json.RawMessage, len(batches))
	for i, b := range batches {
		out[i] = b.Messages
	}
	return out
}

// DedupBatches runs DedupTranscript over the batches' bodies; the
// number of batches and their stored fields never change.
func DedupBatches(batches []TranscriptBatch) []TranscriptBatch {
	bodies := DedupTranscript(TranscriptBodies(batches))
	for i := range batches {
		batches[i].Messages = bodies[i]
	}
	return batches
}

// RunQuery selects runs for Runs. The zero value lists top-level runs,
// newest first, 50 at a time.
type RunQuery struct {
	Agent, SessionID, PublicID, ParentRunID string            // ParentRunID: "" top-level only, "*" all
	Status                                  Status            // "" any
	Playground                              *bool             // nil any
	ExperimentID                            string            // the runs of one experiment
	Meta                                    map[string]string // subset match on metadata
	Before                                  time.Time         // cursor on Started; zero = newest
	// LastSeenAfter keeps the runs last seen at or after it (zero = no
	// bound). A running run is always among them once the bound is
	// older than InterruptedAfter: running means seen that recently.
	LastSeenAfter time.Time
	// BeforeID, with Before, makes the cursor the pair (Started, ID):
	// the runs after that one in the list order — exact inside a group
	// sharing one Started. Pass RunPage.NextBeforeID.
	BeforeID string
	Limit    int // 0 = 50, max 500
	// StoppedBy keeps the runs whose RunRow.StoppedBy is exactly this
	// ("" any); a value ending in ':' is a prefix ("has_tool_call:"
	// matches every has_tool_call:<tools> value).
	StoppedBy string
	// ErrorType keeps the runs whose RunRow.ErrorType is exactly this
	// ("" any): max_steps, cancelled, model_error, …
	ErrorType string
}

// TraceQuery selects traces for Traces. The zero value lists every
// trace, newest first, 50 at a time. A trace's time is its Start: the
// earliest start of the spans it holds.
type TraceQuery struct {
	// Service keeps the traces holding at least one span whose resource
	// service name is exactly this ("" any).
	Service string
	// PublicID keeps the traces every span of which carries
	// weft.public_id = PublicID — the rule a panel token's
	// /api/traces/{id} read applies, so a scoped list offers no trace
	// its detail would refuse (a non-weft span or another public id's
	// run in the trace excludes it).
	PublicID string
	Since    time.Time // Start at or after; zero = no bound
	Until    time.Time // Start before; zero = no bound
	Before   time.Time // cursor on Start; zero = newest
	// BeforeID, with Before, makes the cursor the pair (Start, TraceID),
	// as RunQuery.BeforeID: pass TracePage.NextBeforeID. A late earlier
	// span can re-list a trace on a later page (TracePage).
	BeforeID string
	Limit    int // 0 = 50, max 500
}

// TraceRow is one trace in a list, computed from its spans: the time
// range they cover, how many there are (each (trace, span) once,
// whatever a retried batch duplicated), whether any ended with status
// error, the distinct weft.run.id values they carry (sorted: a subagent
// child shares its parent's trace, so both are here) and the root —
// the earliest span with no parent span id, nil when none arrived (only
// child spans were stored: a hole the reader badges). Service is the
// root's, or the earliest span's when the root is missing.
type TraceRow struct {
	TraceID    string
	Root       *TraceRoot
	Service    string
	Start, End time.Time
	SpanCount  int
	Error      bool
	RunIDs     []string
}

// TraceRoot is a trace's root span: its id and name.
type TraceRoot struct {
	SpanID, Name string
}

// TracePage is one Traces result, paged as RunPage is: newest Start
// first (ties by TraceID, descending), NextBefore/NextBeforeID are the
// next TraceQuery.Before/BeforeID (nil and "" on the last page), traces
// sharing the last row's Start ride along up to MaxTies, and Total
// counts the whole match, cursor aside. A trace's Start is its earliest
// span, so a trace whose earlier span arrives between page reads can be
// listed again on a later page (never skipped); clients dedupe by
// TraceID.
type TracePage struct {
	Traces       []TraceRow
	Total        int
	NextBefore   *time.Time
	NextBeforeID string
}

// Experiment is one saved playground group: a name, the variants and
// inputs that define it, keyed by the id its runs carry as
// weft.experiment.id (§10.4). Variants and inputs are the wire shapes
// verbatim (overrides JSON included); the runs themselves stay in the
// runs table.
type Experiment struct {
	ID, Name, Agent  string
	Created, Updated time.Time
	Variants         []ExperimentVariant
	Inputs           []ExperimentInput
	// ExperimentCommand is the rest of the §5.1 command the matrix
	// sent, so a saved experiment reopens as the form that ran it.
	// Zero on an experiment saved before obsdb kept it.
	ExperimentCommand
}

// ExperimentCommand is the experiment-wide part of the §5.1 run
// command, kept as sent: the modes, the step the cells continue from,
// the source run and the transcript edits. Every field is optional —
// zero is "not recorded" (an older row) or the command's default, and
// a reader does not tell the two apart. obsdb stores it as one JSON
// document beside the definition and does not validate it (Studio
// does, on the way in).
type ExperimentCommand struct {
	Engine          string          `json:"engine,omitempty"`           // live | scripted
	SideEffects     string          `json:"side_effects,omitempty"`     // substitute | park | allow
	Thread          string          `json:"thread,omitempty"`           // ephemeral | fork
	FromStep        *int            `json:"from_step,omitempty"`        // the step every cell continues from
	SourceRunID     string          `json:"source_run_id,omitempty"`    // the run the cells replay from
	TranscriptEdits json.RawMessage `json:"transcript_edits,omitempty"` // the list as sent
}

// ExperimentVariant is one column of the matrix: a key, the overrides
// object it applies (verbatim — instructions, tools_enabled, model,
// thinking, options, params, tool_choice, park_on, only_tools, and any
// key a later command adds), and the modes it runs under when they
// differ from the experiment's (empty: the experiment's own).
type ExperimentVariant struct {
	Key         string          `json:"key"`
	Overrides   json.RawMessage `json:"overrides,omitempty"`
	Engine      string          `json:"engine,omitempty"`
	SideEffects string          `json:"side_effects,omitempty"`
	Thread      string          `json:"thread,omitempty"`
}

// ExperimentInput is one row of the matrix: a key, a source run to
// take the turn from, or a literal text.
type ExperimentInput struct {
	Key         string `json:"key"`
	SourceRunID string `json:"source_run_id,omitempty"`
	Text        string `json:"text,omitempty"`
}

// Status is a run's derived state. running, succeeded and failed are
// what the data says; interrupted is what a reader concludes from a
// stale last-seen (DeriveStatus) — never stored, so a crash leaves
// evidence and a live run in another process is not mistaken for a
// corpse (the store's rule, moved to last-seen).
type Status string

const (
	StatusRunning     Status = "running"
	StatusSucceeded   Status = "succeeded"
	StatusFailed      Status = "failed"
	StatusInterrupted Status = "interrupted"
)

// RunRow is one run in a list or detail: the identity chain, timing,
// derived status and the denormalised usage and counts the list view
// reads without a second query.
type RunRow struct {
	ID, ParentRunID, ParentCallID, TraceID                     string
	Agent, Provider, Model, ManifestHash, WeftVersion, Service string
	SessionID, PublicID                                        string
	Turn                                                       int
	Playground                                                 bool
	ExperimentID, ForkedFrom                                   string
	// ResumedFrom is the run this one resumes — its weft.resumed_from
	// (core.ResumeOf; thread and weft/runtime pass it, an app passes
	// the parked RunResult.ID) — and ResumedBy, on a parked run, the
	// run that resumed it: the latest run by start time whose
	// ResumedFrom names this run (a tie: the larger id), derived at read time on both backends, so a resumer
	// stored before or after the parked run links it alike (record
	// contract §6, UX-E-17). "" when none.
	ResumedFrom, ResumedBy   string
	Meta                     map[string]string
	Started                  time.Time
	Finished                 *time.Time
	LastSeen                 time.Time
	Status                   Status
	Err                      string
	Steps, Pending           int
	StopReason               string
	Usage                    core.Usage
	EventCount, MessageCount int64
	DeltaCount               int64 // deltas are counted, never stored (Q4)
	// ADR 0028 §10: run_start's weft.instructions.hash ("" on a run
	// written before the request record — RequestsHole reads the
	// table), request index 0's weft.catalog.hash ("" when it offered
	// no tools) and the request records' high-water mark (max
	// weft.request.index + 1). The API names them instructions_hash,
	// catalog_hash and request_count.
	InstructionsHash, CatalogHash string
	RequestCount                  int64
	// PromptPreview is the run's prompt — the turn's own, PromptText's
	// rule with TurnPrompt's resume clause (Searcher's prompt field) — cut to
	// PromptPreviewRunes runes; "" when the records carried no content
	// (a content-off run stores no messages) or no user text.
	PromptPreview string
	// Derived marks a run the reader built from stock GenAI spans
	// (DeriveStockRuns; docs/record-contract.md §9): an invoke_agent
	// span with gen_ai.conversation.id and no weft.run.id, its id
	// otel-<trace id>-<root span id>. Such a run has no events, no
	// transcript and no request records (its counts and hashes are
	// zero, its status succeeded or failed only); RequestsHole reads
	// HoleDerived for it, and the API badges it "derived from spans ·
	// no events" (HoleNoteFor(HoleDerived, CauseStockSpans)).
	Derived bool
	// RootEnded marks a run whose invoke_agent span was stored: the span
	// is exported only once it ended, so the run is over whatever records
	// are missing (docs/record-contract.md §6). Its error status is
	// failed; any other status reads succeeded with or without a
	// run_finish (DeriveStatus's ended). LostRecordsHole reads it too.
	RootEnded bool
	// FinishRecorded marks a run whose run_finish record is stored (the
	// column both backends already keep: runs.finished_ok,
	// weft_runs.FinishedOK; a derived run's reads its succeeded status).
	// A RootEnded run without it lost its record tail on the way
	// (TailLostHole) or never had records (LostRecordsHole).
	FinishRecorded bool
	// Settling marks a RootEnded weft run (not Derived, not Failed) whose run_finish
	// is not stored and that was heard from within InterruptedAfter
	// (DeriveSettling): a destination may store the root span before the
	// records that trail it (a log exporter's retry or backlog), so for
	// that window the reader neither calls the records lost
	// (LostRecordsHole, TailLostHole) nor the event stream done
	// (EventPage.Done). Derived at read time with Status, never stored;
	// Status itself already reads succeeded or failed from the root.
	Settling bool
	// StoppedBy names what ended the run, in the manifest's stop_when
	// vocabulary (has_tool_call:<tools>, step_count_is:N,
	// output_submitted, final_answer, parked, max_steps, usage_limit,
	// loop_detected, model_retries_exceeded, canceled, a custom
	// condition's name): the invoke_agent span's weft.run.stopped_by, or
	// run_finish's stopped_by — one core value on both carriers, so a
	// failed run (no run_finish) still names it from its span. "" when
	// neither carried one: a failure no control decided (a model error),
	// a run still going, or one recorded before the field existed.
	StoppedBy string
	// CancelCause is a canceled run's weft.cancel.cause
	// (thread.interrupted, thread.closed, studio.cancel, deadline, or a
	// caller's own core.CancelCause kind); "" on a plain cancel and on
	// every other run.
	CancelCause string
	// EventsEmitted and MessagesEmitted are what the core emitted (U5):
	// the invoke_agent span's weft.run.events and weft.run.messages — the
	// durable event records and growth messages records the run handed
	// to its LoggerProvider. 0 when the root carried neither (not stored
	// yet, or a weft before them) and when the run emitted none (no
	// logger). Fewer stored than emitted is a loss (EventsLost,
	// TailLostHole, LostRecordsHole); emitted 0 with nothing stored is
	// no_logger. Fewer messages stored than emitted, some stored, is a
	// lost transcript batch (MessagesLostHole); none stored is not
	// called lost: a content-off destination legitimately stores no
	// messages record (contract §5), and the row cannot tell it from one
	// that lost them all (contract §7).
	EventsEmitted, MessagesEmitted int64
	// ErrorType is a failed run's kind (U4): the invoke_agent span's
	// error.type — max_steps, usage_limit, loop_detected,
	// model_retries_exceeded, model_contract, model_error, … (the
	// core's closed table, record contract §3) — with the context
	// errors read as "cancelled" (RunErrorType); "" for a run that did
	// not fail, a failure whose root carried none, or a run still
	// going. The larger when several roots differ (both backends).
	ErrorType string
	// Problems are the run's problem counters (U4; Problems): failed
	// attempts, fallbacks, tool errors, denials, truncated results —
	// derived at ingest from its spans; the log counters are filled on
	// the run document only.
	Problems Problems
}

// RunPage is one Runs result, newest Started first (ties by ID,
// descending). NextBefore and NextBeforeID are the next page's
// RunQuery.Before and BeforeID, nil and "" on the last page. A page
// does not end inside a group of runs sharing one Started: the rest of
// the group rides along, so a caller paging on Before alone skips
// nothing — up to MaxTies rows past Limit. A longer tie (a bulk import
// at one truncated timestamp) is cut there, and only the (Before,
// BeforeID) pair resumes inside it. Total counts the whole match,
// cursor aside.
type RunPage struct {
	Runs         []RunRow
	Total        int
	NextBefore   *time.Time
	NextBeforeID string
}

// MaxTies bounds how far a page may run past its Limit to finish a group
// of rows sharing the cursor's time (RunPage, SessionPage): one page
// never grows unbounded however many rows share one timestamp.
const MaxTies = 500

// RunDetail is one run with its subagent children (runs whose
// ParentRunID is this run, by Started).
type RunDetail struct {
	RunRow
	Children []RunRow
}

// PosEvent is one positioned event of a run: the body verbatim, at its
// durable position, with the two content attributes the destination's
// chain stamped on it (ADR 0028 §11): Content is weft.content
// ("stripped" from a content-off chain, "" as emitted) and
// TruncatedBytes weft.content.truncated_bytes (what a content-on
// chain's cap cut, 0 for none). A ClickHouse row written before
// migration 0004 carries neither.
type PosEvent struct {
	Pos            int64
	Time           time.Time
	Event          json.RawMessage // the body, verbatim
	Content        string
	TruncatedBytes int64
}

// EventPage is one page of a run's durable events. Gaps are durable
// positions missing below the high-water mark: a lost batch, never a
// delta — deltas are on their own counter, so their absence cannot open
// a hole here (D3). At most MaxGaps are listed, lowest first.
type EventPage struct {
	Events    []PosEvent
	NextAfter *int64
	// Done: every stored event was returned and the run is terminal —
	// its run_finish is stored, its invoke_agent span ended with Error,
	// or its invoke_agent span is stored (RunRow.RootEnded) and nothing
	// was heard from the run for over InterruptedAfter (the settling
	// window, DeriveSettling: records that trail a stored root are given
	// that long to land, so a run whose run_finish was lost still ends
	// a poll that waits for Done). An interrupted run (no root, stale)
	// is not Done here: a reader that follows it decides from the row's
	// derived Status.
	Done bool
	Gaps []int64
}

// MaxGaps bounds EventPage.Gaps: positions are the sender's numbers, and
// one stray high position must not turn a page read into a list of
// every position below it.
const MaxGaps = 1000

// SessionQuery selects sessions. The zero value lists them newest
// activity first, 50 at a time. PublicID scopes the runs a session is
// grouped over, before the grouping (RunQuery.PublicID's rule): a
// session is listed when one of its turns carries the public id, and
// its turns, usage, first/last seen, agent, status, the page's order,
// Total and the cursor are those turns' alone — a run of another app
// sharing the session id (a colliding derived run) moves none of them.
//
// Q finds sessions by text: every whitespace-separated term must be a
// case-insensitive substring of one of the session's title, its first
// turn's prompt (the whole prompt, not the preview's cut), its id, its
// public id or its agent — terms AND, fields OR. Case folding is
// ASCII-only on SQLite (lower()), Unicode on ClickHouse
// (positionCaseInsensitiveUTF8).
type SessionQuery struct {
	Agent, PublicID string
	Q               string
	Before          time.Time // cursor on LastSeen; zero = newest
	BeforeID        string    // with Before: the (LastSeen, ID) pair, as RunQuery.BeforeID
	Limit           int       // 0 = 50, max 500
}

// SessionRow is one session (a GROUP BY over its top-level,
// non-playground runs).
type SessionRow struct {
	ID, PublicID, Agent string
	Turns               int
	FirstSeen, LastSeen time.Time
	Status              Status // the newest turn's
	Usage               core.Usage
	// Title is the newest turn's (by turn, then start) non-empty
	// SessionTitleKey — thread's Title as that turn started, a label
	// that survives content-off — "" when no turn carried one.
	Title string
	// FirstPrompt is the first turn's (lowest turn, then earliest
	// start) RunRow.PromptPreview: "" when that turn's records carried
	// no content (content-off) or no user text — never a later turn's.
	FirstPrompt string
	// ContentOff marks a session whose first turn (FirstPrompt's) was
	// recorded without content: events stored and not one messages
	// record (RunRow.EventCount > 0, MessageCount 0), and not Derived,
	// nor running or settling — its messages records may still arrive
	// (SessionContentOff). The list says "(content not captured)" for it
	// instead of the id.
	ContentOff bool
}

// SessionContentOff is SessionRow.ContentOff's rule over the session's
// first turn, a run row with its Status and Settling derived
// (DeriveStatus, DeriveSettling): events stored, not one messages
// record, not Derived, and over — not running, not settling, whose
// messages records may still arrive. Every backend and Studio's scoped
// session row read it; the web client's lib/title.ts contentOff is its
// twin.
func SessionContentOff(first RunRow) bool {
	return !first.Derived && first.MessageCount == 0 && first.EventCount > 0 &&
		first.Status != StatusRunning && !first.Settling
}

// SessionTitleKey is the run metadata a thread session stamps with its
// current title (docs/record-contract.md §2): it lands in RunRow.Meta
// and is read back as SessionRow.Title.
const SessionTitleKey = "weft.session.title"

// SessionPage is one Sessions result, paged as RunPage is: newest
// LastSeen first (ties by ID, descending), NextBefore/NextBeforeID are
// the next SessionQuery.Before/BeforeID, sessions sharing the last
// row's LastSeen ride along up to MaxTies, and Total counts the whole
// match.
type SessionPage struct {
	Sessions     []SessionRow
	Total        int
	NextBefore   *time.Time
	NextBeforeID string
}

// SessionDetail is one session with its turns — all of them: top-level
// runs by Turn, then Started; experiments excluded (they hang off runs
// via ForkedFrom).
type SessionDetail struct {
	SessionRow
	Runs []RunRow
}

// ErrNotFound is returned by Run, Session, Events, Transcript,
// RunSpans, OtherLogs, Requests, Prompt, Tools, Catalogs, Experiment,
// DeleteRun, DeleteSession and ResolvePublicID for an id the database does not hold (Prompt and
// Tools also for a hash, possibly as a *HoleError). Trace answers an
// unknown trace with no spans instead: a trace is only ever the spans
// that arrived.
var ErrNotFound = errors.New("obsdb: not found")

// ErrClosed is returned by a backend used after Close.
var ErrClosed = errors.New("obsdb: closed")

// InterruptedAfter is the "last seen" age that turns a non-terminal run
// interrupted: three missed heartbeats.
const InterruptedAfter = 30 * time.Second

// SweepBatch bounds one pass of DB.Sweep: at most this many root runs
// (each with its subtree of not-running children) are deleted per
// writer transaction on SQLite, per round of DELETE mutations on
// ClickHouse. Between passes the writer is free: a sweep of a large
// backlog (the first sweep of `weft studio --retain` on an old
// database) interleaves with ingest instead of blocking it.
const SweepBatch = 500

// DeriveStatus applies the four-row table every backend reads through:
//
//	the invoke_agent span ended with status error          →  failed
//	a run_finish record exists, or the invoke_agent span
//	ended with any other status (Ok, Unset)                 →  succeeded
//	neither, and now - LastSeen ≤ InterruptedAfter          →  running
//	neither, and older                                      →  interrupted
//
// spanFailed is the invoke_agent span's error status; ended is the
// run_finish record's presence or the invoke_agent span's
// (RunRow.RootEnded) — a span is exported only once it ended, so a run
// whose root arrived is over even when its run_finish was lost (a
// tracer-only app's run, or one whose record tail a destination
// dropped). LastSeen is the newest record or span time for the run,
// heartbeats included. failed outranks succeeded: a run whose span
// errored failed, whatever records raced behind it. Pending > 0 on a
// succeeded run means parked at an approval, not stuck.
func DeriveStatus(spanFailed, ended bool, lastSeen, now time.Time) Status {
	switch {
	case spanFailed:
		return StatusFailed
	case ended:
		return StatusSucceeded
	case !lastSeen.IsZero() && now.Sub(lastSeen) > InterruptedAfter:
		return StatusInterrupted
	default:
		return StatusRunning
	}
}

// DeriveSettling is RunRow.Settling's rule: the invoke_agent span is
// stored (rootEnded), the run_finish record is not (finishRecorded
// false), and now - lastSeen ≤ InterruptedAfter — the window in which a
// destination that stored the root before the records behind it (a
// log exporter's retry or backlog; Studio's destination batches logs
// faster than spans, but a user's Exporters pair need not) may still
// deliver them. Pass rootEnded false for a derived run — stock spans
// have no records to wait for — and for a failed one: a failed root is
// terminal, its event page done at once (EventPage.Done), so the row
// never says "settling" while the page says done. A backend derives it at read time beside
// DeriveStatus, with the same clock.
func DeriveSettling(rootEnded, finishRecorded bool, lastSeen, now time.Time) bool {
	return rootEnded && !finishRecorded && !lastSeen.IsZero() && now.Sub(lastSeen) <= InterruptedAfter
}

// LimitOf normalizes a query limit: 0 means the default 50, values
// above 500 clamp to 500, negatives read as the default.
func LimitOf(n int) int {
	switch {
	case n <= 0:
		return 50
	case n > 500:
		return 500
	default:
		return n
	}
}
