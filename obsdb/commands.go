package obsdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// CommandStore is the optional playground-link capability of a DB
// (plan I3): what Studio's runtime server asked the runtimes it links
// to do, so a restarted Studio still knows. It holds four small sets of
// mutable rows beside the run tables:
//
//   - commands: every command Studio enqueued to a runtime — its wire
//     body, its state (CommandQueued … CommandLost) and the times a
//     restarted Studio decides "lost" from (AcceptedAt, LastBeat),
//     never re-issuing one;
//   - routes: run id → the runtime that started it (an ack named it),
//     what cancel, steer and the debugger's verbs are routed by;
//   - breakpoints: each runtime's debugger tool set, replaced whole;
//   - runtimes: each registration, so a restarted Studio can list what
//     was registered before it.
//
// Both first-party backends implement it — obsdb/sqlite (migration
// 0007) and obsdb/clickhouse (migration 0008) — and the obsdbtest
// conformance table holds them to the same answers. A backend that does
// not implement it keeps the runtime server in memory only, as before.
//
// These rows are Studio's own bookkeeping, not run data: DB.DeleteRun,
// DB.DeleteSession, DB.Sweep and DB.Vacuum never touch them;
// PruneCommands is their retention.
type CommandStore interface {
	// PutCommand writes c, replacing any row under c.ID whole (an
	// upsert: the caller writes the row it holds). A zero Created or
	// Updated is stamped with the current time — on every write, so
	// the caller carries the Created it read back into each upsert; a
	// zero one would move the row to the front of the newest-first
	// order. ErrInvalidCommand for an empty ID or a State outside the
	// five.
	PutCommand(ctx context.Context, c StoredCommand) error
	// Commands answers the rows q selects, newest Created first (ties
	// by ID, descending). An empty query answers every row.
	Commands(ctx context.Context, q CommandQuery) ([]StoredCommand, error)
	// PutRoute writes r, replacing any route of r.RunID. ErrInvalidCommand
	// for an empty RunID or RuntimeID. A zero Seen is stamped with the
	// current time on every write.
	PutRoute(ctx context.Context, r RunRoute) error
	// Route answers the route of runID; false when none is stored.
	Route(ctx context.Context, runID string) (RunRoute, bool, error)
	// PutBreakpoints replaces runtimeID's breakpoint set whole; an empty
	// tools stores the empty set (a clear), which Breakpoints then
	// answers as stored. ErrInvalidCommand for an empty runtimeID.
	PutBreakpoints(ctx context.Context, runtimeID string, tools []string) error
	// Breakpoints answers runtimeID's stored set (never nil when
	// stored); false when none was ever stored — distinct from a stored
	// empty set, which a registering runtime's own set must not replace.
	Breakpoints(ctx context.Context, runtimeID string) ([]string, bool, error)
	// PutRuntime writes r, replacing any row under r.ID whole. Zero
	// Registered or LastSeen is stamped with the current time on every
	// write, so the caller carries Registered into each upsert.
	// ErrInvalidCommand for an empty ID.
	PutRuntime(ctx context.Context, r StoredRuntime) error
	// Runtimes answers every stored runtime, most recently seen first
	// (ties by ID).
	Runtimes(ctx context.Context) ([]StoredRuntime, error)
	// PruneCommands bounds the store, the in-memory runtime server's
	// rule made durable, and answers how many commands it deleted:
	//
	//   - a terminal command (CommandTerminal) whose Updated is before
	//     before is deleted;
	//   - then, while more than keep commands remain (keep ≤ 0: no
	//     count bound), the
	//     oldest terminal ones by Created go — a queued or accepted
	//     command is never deleted, whatever its age or the count;
	//   - a route goes when it names a command (CommandID) no longer
	//     stored, or when its Seen is before before and no queued or
	//     accepted command names its run;
	//   - a runtime goes when its LastSeen is before before, no queued
	//     or accepted command is addressed to it and its breakpoint set
	//     is absent or empty (that set goes with it);
	//   - an empty breakpoint set no runtime row names goes when it was
	//     last written before before; a non-empty one stays, as a
	//     runtime holding one does.
	//
	// The terminal commands go in bounded passes (SweepBatch rows on
	// SQLite, each its own transaction, ctx checked between them); on
	// an error the passes before it stay deleted and are counted. On
	// ClickHouse the prune is not atomic with concurrent Put* calls —
	// a Put racing it keeps its own version, but each rule decides
	// from what it read — so the caller serialises PruneCommands with
	// its own writes.
	PruneCommands(ctx context.Context, before time.Time, keep int) (int, error)
}

// Command states: the runtime server's lifecycle (WEFT-PLAYGROUND
// §10.5). Queued and accepted are live; the other three are terminal.
const (
	CommandQueued   = "queued"
	CommandAccepted = "accepted"
	CommandRejected = "rejected"
	CommandFinished = "finished"
	CommandLost     = "lost"
)

// CommandTerminal reports whether state is terminal — rejected,
// finished or lost: nothing acts on the command any more, and
// PruneCommands may delete it.
func CommandTerminal(state string) bool {
	switch state {
	case CommandRejected, CommandFinished, CommandLost:
		return true
	}
	return false
}

func validCommandState(state string) bool {
	return state == CommandQueued || state == CommandAccepted || CommandTerminal(state)
}

// ErrInvalidCommand is returned by a CommandStore write whose row lacks
// its key, or whose command State is not one of the five.
var ErrInvalidCommand = errors.New("obsdb: invalid command store row")

// StoredCommand is one command row. The indexed columns are what the
// queries select by and what a restart decides from; Body is the whole
// wire command (the runtime link's frame data) and Ack the last ack
// the runtime posted for it, both stored verbatim (nil reads back nil).
type StoredCommand struct {
	ID        string
	RuntimeID string // the runtime the command was enqueued to
	// Kind is the frame kind the caller names — "" or run, cancel,
	// approve, breakpoints, steer; stored verbatim, never interpreted.
	Kind  string
	State string // one of the five Command* states
	// RunID is the run an ack named ("" until one did).
	RunID string
	// PublicID is the public id the command is scoped to; Fork marks a
	// thread=fork command (its runs are sessions the runtime owns).
	PublicID string
	Fork     bool
	// Agent, SourceRunID and ExperimentID are the command's own, copied
	// out of Body for the reader that lists without decoding it.
	Agent, SourceRunID, ExperimentID string
	// Status is a finished command's run outcome (succeeded | failed);
	// Error why, when there is a why (a rejection's reason, a failed
	// run's error, what made it lost).
	Status, Error string
	// Created and Updated bound the row's life; AcceptedAt is when the
	// accepted ack arrived and LastBeat the last heartbeat (a repeated
	// accepted ack) — zero when none did. A restarted Studio decides a
	// silent accepted command lost from these two, never re-issuing it.
	Created, Updated, AcceptedAt, LastBeat time.Time
	Body                                   json.RawMessage
	Ack                                    json.RawMessage
}

// CommandQuery selects commands. Every set field must match; an empty
// query matches every row. Limit ≤ 0 answers every match.
type CommandQuery struct {
	ID        string
	RuntimeID string
	RunID     string
	States    []string // any of these states; nil and empty both mean no filter
	Limit     int
}

// CommandByRun answers the newest command whose RunID is runID; false
// when none names it.
func CommandByRun(ctx context.Context, s CommandStore, runID string) (StoredCommand, bool, error) {
	if runID == "" {
		return StoredCommand{}, false, nil
	}
	rows, err := s.Commands(ctx, CommandQuery{RunID: runID, Limit: 1})
	if err != nil || len(rows) == 0 {
		return StoredCommand{}, false, err
	}
	return rows[0], true, nil
}

// RunRoute is one run → runtime route: the runtime an ack said started
// the run, the command whose ack named it last, the public id and fork
// mark the command carried, and when an ack last named it.
type RunRoute struct {
	RunID, RuntimeID, CommandID, PublicID string
	Fork                                  bool
	Seen                                  time.Time
}

// StoredRuntime is one runtime's latest registration. Agents are its
// agents' names; Body is the whole registration as the runtime posted
// it. Cwd is the app's working directory when the registration carries
// one ("" otherwise).
type StoredRuntime struct {
	ID, Service, Host, Cwd string
	Pid                    int
	Agents                 []string
	Registered, LastSeen   time.Time
	Body                   json.RawMessage
}

// CheckStoredCommand is the validation every backend's PutCommand
// runs: an ID and one of the five states.
func CheckStoredCommand(c StoredCommand) error {
	if c.ID == "" {
		return fmt.Errorf("%w: command without an id", ErrInvalidCommand)
	}
	if !validCommandState(c.State) {
		return fmt.Errorf("%w: command %s: unknown state %q", ErrInvalidCommand, c.ID, c.State)
	}
	return nil
}
