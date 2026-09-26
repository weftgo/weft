package store

import (
	"context"
	"errors"
	"time"

	"github.com/weftgo/weft"
)

// FormatVersion is the wire version of every document the store writes
// (ADR 0010 §2.3) — the integer the manifest already uses ("weft": 1).
// It moves when any stored document changes incompatibly: a renamed or
// retyped key bumps it; an added optional key never does (readers
// ignore unknown keys). Backends record it per row and refuse a file
// whose schema is newer than the binary (sqlite.ErrNewerSchema).
const FormatVersion = 1

// Status is a run record's state. running, succeeded, and failed are
// stored; interrupted is what a reader concludes (DeriveStatus) —
// never written, so a crash leaves evidence and a live run in another
// process is not mistaken for a corpse (ADR 0010 §2.4).
type Status string

const (
	Running     Status = "running"
	Succeeded   Status = "succeeded"
	Failed      Status = "failed"
	Interrupted Status = "interrupted" // derived on read from the heartbeat; never written
)

// HeartbeatTimeout is how stale a running record's heartbeat may be
// before a reader concludes the run was interrupted: 30 s by default.
// The recorder bumps the heartbeat on every event and on a ticker at a
// third of this timeout while a run is alive but quiet, so a slow tool
// call never reads as a crash.
const HeartbeatTimeout = 30 * time.Second

// DeriveStatus applies the heartbeat rule (ADR 0010 §2.4): a running
// record whose heartbeat is older than HeartbeatTimeout reads as
// Interrupted; every other status reads as stored. Get, List, and
// Query.Status all see records through it. Exported because any reader
// of the format — an Inspector over a JSONL export, a future backend —
// must reach the same conclusion from the same bytes.
func DeriveStatus(status Status, heartbeat, now time.Time) Status {
	if status == Running && !heartbeat.IsZero() && now.Sub(heartbeat) > HeartbeatTimeout {
		return Interrupted
	}
	return status
}

// RunRecord is one run: its identity, its event stream, and its
// result. The event stream is every weft.Event the run emitted in
// emission order, Nested inline, so a parent's record replays as one
// stream (ADR 0004's total order is the record's order). The result is
// kept on failure too — RunError carries the partial transcript, so a
// failed run's transcript up to the failure is part of the record
// (ADR 0010 §2.1). The record is not a checkpoint: it says what
// happened, not how to resume mid-step.
type RunRecord struct {
	ID           string
	ParentID     string // the parent run when this is a subagent's child record; "" = top-level
	ParentCallID string // the parent's tool call that owns the child run
	Agent        string // weft.Name, via RunStart
	Model        weft.ModelInfo
	ManifestHash string            // sha256 of weft.Manifest(agent) at RunStart; "" when unnamed
	WeftVersion  string            // the core module version the recording process built against
	Started      time.Time         // UTC
	Finished     time.Time         // zero while running
	Heartbeat    time.Time         // last write; readers derive Interrupted from it
	Status       Status            // Running | Succeeded | Failed as stored; DeriveStatus may read Interrupted
	Tags         map[string]string // consumer-supplied (Tags); hmm: cwd, Weft CI: pr
	Events       []weft.Event      // Seq order, Nested inline; empty from List
	Result       *weft.RunResult   // set on success and on failure (the partial); omitted by List
	// Usage is the run's total usage, denormalized onto the record
	// from Result (ADR 0010 §2.6: the Usage struct per run) — the
	// list view's token column without a Get.
	Usage weft.Usage
	// Err is the RunError text when Status == Failed, and the
	// degraded marker when a store write failed mid-run.
	Err string
}

// Query selects runs for List. The zero value lists top-level runs,
// newest first, 50 at a time.
type Query struct {
	Agent  string // exact match; "" = any
	Status Status // "" = any; Interrupted matches by the heartbeat rule, Running excludes it
	// ParentID selects the run tree: "" (the zero value) lists
	// top-level runs only, so children do not flood the list view;
	// "*" lists every run; a concrete id lists that run's children.
	ParentID string
	// Tags matches runs carrying every given pair (a subset match —
	// a run may carry more).
	Tags map[string]string
	// Before is the paging cursor: only runs started strictly before
	// it are returned. Zero means newest first. Offsets are
	// deliberately absent — they drift under concurrent inserts
	// (ADR 0010 §0.1: LangGraph keys, Mastra is moving there).
	Before time.Time
	// Limit caps the page: 0 means 50, values above 500 are clamped.
	Limit int
}

// Page is one List result.
type Page struct {
	// Runs is the page, newest first by Started, Events omitted — a
	// list body with full transcripts is the storage bloat every
	// surveyed store had to walk back (Mastra's listTracesLight).
	// Get returns the events.
	Runs []RunRecord
	// Total is the number of rows matching the query, ignoring Before
	// and Limit — the "how many pages are there" number.
	Total int
}

// Store is a run-record backend: Memory in process, sqlite on disk,
// Postgres later — all running the storetest conformance table.
// Implementations must be safe for concurrent use; the recorder is the
// only writer in weft dev's process, but readers (the Inspector) may
// read concurrently (WAL, or a second process with its own handle).
type Store interface {
	// Save upserts the run's row by ID and appends any events in
	// r.Events not yet present (compared by position: the first
	// Save's events are the first rows). Saving the same record twice
	// changes nothing; saving one with more events appends the tail.
	// The recorder saves the row at RunStart (running), per heartbeat
	// tick, and at the run's end.
	Save(ctx context.Context, r RunRecord) error

	// Append adds events to a run's stream in arrival order and bumps
	// the heartbeat — the hot path: one transaction per call, nothing
	// buffered. Events are stored under ordinals assigned atomically
	// per run; a call appending to an unknown run fails with
	// ErrNotFound.
	Append(ctx context.Context, id string, ev ...weft.Event) error

	// Get returns the whole record — identity, events in order,
	// result. An event whose type this weft does not know fails with
	// an error wrapping ErrUnknownEvent (naming the type and the run);
	// loud over silent is the format's rule (ADR 0010 §2.5).
	Get(ctx context.Context, id string) (RunRecord, error)

	// List returns a page of records without events, newest first,
	// with the Total count. See Query for filtering and paging.
	List(ctx context.Context, q Query) (Page, error)

	// Delete removes the run and its events. Children survive with
	// their ParentID cleared — orphaned by design: a child is its own
	// record (ADR 0010 §2.1), and deleting a parent does not pretend
	// its delegations never ran. Retention ("older than N") is a
	// consumer loop over List and Delete, not a store feature.
	Delete(ctx context.Context, id string) error
}

var (
	// ErrNotFound is returned by Get, Append, and Delete for a run id
	// the store does not hold.
	ErrNotFound = errors.New("store: run not found")

	// ErrUnknownEvent wraps the decode failure Get reports for an
	// event whose "type" this build's weft does not know: a recorded
	// stream must replay exactly what was emitted, and an older weft
	// reading a newer recording says so instead of guessing. List —
	// which never reads events — still works, so an older Inspector
	// shows the run and names why it cannot open it.
	ErrUnknownEvent = errors.New("store: event type unknown to this weft")
)

// limitOf normalizes Query.Limit: 0 means the default 50, values above
// 500 clamp to 500, negatives read as the default.
func limitOf(n int) int {
	switch {
	case n <= 0:
		return 50
	case n > 500:
		return 500
	default:
		return n
	}
}
