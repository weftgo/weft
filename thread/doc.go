// Package thread gives weft sessions: a conversation as an append-only
// tree of entries, durable through a Storage backend, with turns,
// branching, compaction, approvals and delegation all built on that
// one tree. ADR 0011 is the design record.
//
// # Sessions
//
// Create starts a session, Open loads one, List pages their headers
// and Delete removes one. A Session holds the tree in memory and is
// its session's only writer: every write is one Storage.Append, and
// nothing is ever rewritten or deleted in place. The leaf is the entry
// the next one attaches to; Session.Context is what the model sees —
// the messages on the path from a root to the leaf, repaired.
//
// Open reads and nothing else. It validates the tree — unique valid
// ids, every parent an earlier entry — and refuses a file that fails
// with ErrCorrupt; it writes no entry and starts no run. What a
// crashed writer left unfinished waits for the caller: Session.Queue
// lists the restored steers and Session.Continue runs them.
// Session.Close quiesces a session and releases its storage.
//
// # The entry tree and the format
//
// The entry kinds are a sealed set, like weft.Event, one wire
// discriminator each, restored by UnmarshalEntry: message, turn,
// compaction, branch_summary, leaf, label, info, custom,
// custom_message; the approval kinds approval_request,
// approval_decision, approval_audit, grant, grant_revoked; receipt
// (steering) and pool_receipt (delegation). Every entry carries an id,
// a parent and a time; a message entry embeds the core's message wire
// (ADR 0001) verbatim.
//
// A stored session is a header ({"weft":1,"type":"session",…} — the
// envelope integer every weft wire document carries, ADR 0005) and
// then its entries in append order. The format grows without breaking
// readers: an entry kind added after format 1 carries "v":N, the
// minimum reader version, and a reader that meets a kind or a version
// it does not know fails with ErrNewerFormat instead of skipping it.
// Unknown keys are additive and ignored. Golden files in testdata pin
// every format version, and every release reads them all back.
//
// The tree branches without losing anything: Session.Branch appends a
// leaf entry that moves the leaf to any earlier entry (SummarizeLeft
// records what the abandoned branch held), and Session.Fork copies a
// path into a new, self-contained session whose header names its
// origin. Session.Label, Session.SetInfo, Session.Custom and
// Session.CustomMessage append the bookkeeping kinds.
//
// # Storage
//
// Storage is the backend interface — Create, Append, Load, List,
// Delete — and package threadtest is the conformance table every
// backend runs. Three ship: Memory, in process; package jsonl, one
// JSON-lines file per session, readable with jq and backed up with cp;
// and package sqlite, in its own module so its driver stays out of
// this one. A backend's Append is atomic, and what Load returns never
// aliases what is stored. Optional capabilities are small interfaces
// found by type assertion: Flusher (the FsyncOnFlush durability
// cadence) and Watcher, the live tail — entries yielded as they are
// appended, for a reader that follows a session another process
// writes.
//
// A load that drops a torn final line, or skips a damaged one under
// Salvage, is never silent: Session.LoadReport says what was dropped,
// skipped, and orphaned by the skip.
//
// # Turns and busy policies
//
// Session.Send appends the prompt — durable before the run starts —
// runs the session's agent over the leaf's context under the run id
// <session>-t<n>, and returns a Turn at once: its receipt, its event
// stream, its Wait. The run's messages are appended as they join the
// run, and a turn entry closes the ledger whether the run succeeded,
// failed or was canceled.
//
// One run per session at a time. A Send that meets a running turn
// follows the busy policy, set with BusyPolicy or per Send with As:
// Queue (the default) runs it next; Reject fails it with ErrBusy;
// Steer delivers it into the running turn at its next drain point
// (ADR 0019), its journey recorded on receipt entries; Interrupt
// cancels the running turn and runs the message next; Rollback also
// branches back to before the interrupted turn.
//
// # Compaction, approvals, delegation
//
// Compaction (ADR 0020) keeps a long conversation inside the model's
// window without deleting anything: a compaction entry carries a
// summary and the id of the first entry kept raw, and the context
// reads the summary in place of what came before. ContextWindow arms
// the automatic trigger; Session.Compact, Session.PreviewCompaction
// and Session.ApplyCompaction are the manual path; a turn that fails
// with weft.ErrContextOverflow compacts and runs once more.
//
// Approvals (ADR 0021) make the core's approval boundary durable: a
// parked call is an approval_request entry, Session.Pending lists
// them across restarts, Session.Decide records a decision and resumes
// the turn. Grants, a live Approver, quorum, expiry and signed
// decisions (Keyring, DecideSigned) form the chain a call passes
// before it parks.
//
// Package pool (ADR 0022) runs bounded concurrent child sessions for
// a parent: each child is a session with a Lineage, its cost lands in
// the parent's Usage.Delegated, and its parked calls mirror onto the
// parent's Pending.
//
// # Identity
//
// Every run a session starts carries weft.session.id and weft.turn as
// run metadata, the fork origin or pool lineage when the header names
// one, and weft.public_id when the session was created with PublicID
// — an opaque handle safe to show a browser (ADR 0024 S5). The public
// id lives in the header, is matched by List's Query.Meta, and cannot
// be changed after Create.
//
// # Concurrency
//
// A Session is safe for concurrent use; one mutex guards its tree and
// is held across each storage write, so a returned write is durable
// and the tree in memory equals the stored one. Keep one Session per
// session id per process: backends refuse a second writer from
// another process or Storage value with ErrLocked. Hooks and
// callbacks run without the session's lock, except the IDs and Clock
// functions. The Session type documents the whole contract — what
// concurrent Sends do under each policy, Close, and Delete of an open
// session.
//
// # Errors
//
// Failures are sentinel errors, wrapped with context and matched with
// errors.Is. Two are retryable — the same call succeeds later:
// ErrBusy (a turn is running) and ErrLocked (another writer holds the
// session). Two are terminal for the stored data as this build reads
// it: ErrCorrupt (carried by *CorruptError, naming the line and
// entry) and ErrNewerFormat (written by a newer weft). The rest name
// a refused call: ErrNotFound, ErrExists, ErrClosed, ErrCreateOnly,
// ErrReservedKey, ErrNotPending, and the signed-decision failures.
package thread
