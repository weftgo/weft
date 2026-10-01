package obsdb

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/weftgo/weft"
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
	Events(ctx context.Context, runID string, after int64, limit int) (EventPage, error)
	Transcript(ctx context.Context, runID string) ([]json.RawMessage, error) // messages bodies, in order
	RunSpans(ctx context.Context, runID string) ([]Span, error)
	Trace(ctx context.Context, traceID string) ([]Span, error)

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

// RunQuery selects runs for Runs. The zero value lists top-level runs,
// newest first, 50 at a time.
type RunQuery struct {
	Agent, SessionID, PublicID, ParentRunID string            // ParentRunID: "" top-level only, "*" all
	Status                                  Status            // "" any
	Playground                              *bool             // nil any
	ExperimentID                            string            // the runs of one experiment
	Meta                                    map[string]string // subset match on metadata
	Before                                  time.Time         // cursor on Started; zero = newest
	Limit                                   int               // 0 = 50, max 500
}

// Experiment is one saved playground group: a name, the variants and
// inputs that define it, keyed by the id its runs carry as
// weft.experiment.id (§10.4). Variants and inputs are the wire shapes
// verbatim (overrides JSON included); the runs themselves stay in the
// runs table.
type Experiment struct {
	ID, Name, Agent string
	Created, Updated time.Time
	Variants        []ExperimentVariant
	Inputs          []ExperimentInput
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
	Usage                                                      weft.Usage
	EventCount, MessageCount                                   int64
	DeltaCount                                                 int64 // deltas are counted, never stored (Q4)
}

// RunPage is one Runs result.
type RunPage struct {
	Runs       []RunRow
	Total      int
	NextBefore *time.Time
}

// RunDetail is one run with its subagent children (runs whose
// ParentRunID is this run, by Started).
type RunDetail struct {
	RunRow
	Children []RunRow
}

// PosEvent is one positioned event of a run: the body verbatim, at its
// durable position.
type PosEvent struct {
	Pos   int64
	Time  time.Time
	Event json.RawMessage // the body, verbatim
}

// EventPage is one page of a run's durable events. Gaps are durable
// positions missing below the high-water mark: a lost batch, never a
// delta — deltas are on their own counter, so their absence cannot open
// a hole here (D3).
type EventPage struct {
	Events    []PosEvent
	NextAfter *int64
	Done      bool // the run is terminal and every stored event was returned
	Gaps      []int64
}

// SessionQuery selects sessions. The zero value lists them newest
// activity first, 50 at a time.
type SessionQuery struct {
	Agent, PublicID string
	Before          time.Time // cursor on LastSeen; zero = newest
	Limit           int       // 0 = 50, max 500
}

// SessionRow is one session (a GROUP BY over its top-level,
// non-playground runs).
type SessionRow struct {
	ID, PublicID, Agent string
	Turns               int
	FirstSeen, LastSeen time.Time
	Status              Status // the newest turn's
	Usage               weft.Usage
}

// SessionPage is one Sessions result.
type SessionPage struct {
	Sessions   []SessionRow
	Total      int
	NextBefore *time.Time
}

// SessionDetail is one session with its turns: top-level runs by Turn,
// then Started; experiments excluded (they hang off runs via
// ForkedFrom).
type SessionDetail struct {
	SessionRow
	Runs []RunRow
}

// ErrNotFound is returned by Run, Session, Events, Transcript,
// RunSpans, Trace and ResolvePublicID for an id the database does not
// hold.
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
