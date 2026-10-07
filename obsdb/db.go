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
	Trace(ctx context.Context, traceID string) ([]Span, error) // empty, not an error, for an unknown trace

	Sessions(ctx context.Context, q SessionQuery) (SessionPage, error)
	Session(ctx context.Context, id string) (SessionDetail, error)
	ResolvePublicID(ctx context.Context, publicID string) (sessionID string, err error)

	// The playground's saved experiments (WEFT-PLAYGROUND §10.4,
	// PQ4): the definition rows keyed by the weft.experiment.id the
	// runs carry — one small table beside runs.
	SaveExperiment(ctx context.Context, e Experiment) error // upsert by ID
	Experiments(ctx context.Context) ([]Experiment, error)  // newest update first
	Experiment(ctx context.Context, id string) (Experiment, error)

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
	// BeforeID, with Before, makes the cursor the pair (Started, ID):
	// the runs after that one in the list order — exact inside a group
	// sharing one Started. Pass RunPage.NextBeforeID.
	BeforeID string
	Limit    int // 0 = 50, max 500
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
}

// ExperimentVariant is one column of the matrix: a key and the
// overrides object it applies.
type ExperimentVariant struct {
	Key       string          `json:"key"`
	Overrides json.RawMessage `json:"overrides,omitempty"`
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
	Meta                                                       map[string]string
	Started                                                    time.Time
	Finished                                                   *time.Time
	LastSeen                                                   time.Time
	Status                                                     Status
	Err                                                        string
	Steps, Pending                                             int
	StopReason                                                 string
	Usage                                                      core.Usage
	EventCount, MessageCount                                   int64
	DeltaCount                                                 int64 // deltas are counted, never stored (Q4)
	// ADR 0028 §10: run_start's weft.instructions.hash ("" on a run
	// written before the request record — RequestsHole reads the
	// table), request index 0's weft.catalog.hash ("" when it offered
	// no tools) and the request records' high-water mark (max
	// weft.request.index + 1). The API names them instructions_hash,
	// catalog_hash and request_count.
	InstructionsHash, CatalogHash string
	RequestCount                  int64
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
	Done      bool // the run is terminal and every stored event was returned
	Gaps      []int64
}

// MaxGaps bounds EventPage.Gaps: positions are the sender's numbers, and
// one stray high position must not turn a page read into a list of
// every position below it.
const MaxGaps = 1000

// SessionQuery selects sessions. The zero value lists them newest
// activity first, 50 at a time.
type SessionQuery struct {
	Agent, PublicID string
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
}

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
// RunSpans, OtherLogs, Requests, Prompt, Tools, Catalogs, Experiment and
// ResolvePublicID for an id the database does not hold (Prompt and
// Tools also for a hash, possibly as a *HoleError). Trace answers an
// unknown trace with no spans instead: a trace is only ever the spans
// that arrived.
var ErrNotFound = errors.New("obsdb: not found")

// ErrClosed is returned by a backend used after Close.
var ErrClosed = errors.New("obsdb: closed")

// InterruptedAfter is the "last seen" age that turns a non-terminal run
// interrupted: three missed heartbeats.
const InterruptedAfter = 30 * time.Second

// DeriveStatus applies the four-row table every backend reads through:
//
//	the invoke_agent span ended with status error  →  failed
//	a run_finish record exists (and no error span) →  succeeded
//	neither, and now - LastSeen ≤ InterruptedAfter →  running
//	neither, and older                             →  interrupted
//
// spanFailed is the invoke_agent span's error status; runFinished is
// the run_finish record's presence. LastSeen is the newest record or
// span time for the run, heartbeats included. failed outranks
// succeeded: a run whose span errored failed, whatever records raced
// behind it. Pending > 0 on a succeeded run means parked at an
// approval, not stuck.
func DeriveStatus(spanFailed, runFinished bool, lastSeen, now time.Time) Status {
	switch {
	case spanFailed:
		return StatusFailed
	case runFinished:
		return StatusSucceeded
	case !lastSeen.IsZero() && now.Sub(lastSeen) > InterruptedAfter:
		return StatusInterrupted
	default:
		return StatusRunning
	}
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
