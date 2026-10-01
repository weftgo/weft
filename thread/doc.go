// Package thread gives weft sessions: a conversation as an append-only
// tree of entries, durable through a Storage backend, with turns,
// branching, compaction, approvals and delegation all built on that
// one tree. ADR 0011 is the design record; docs/thread-operations.md
// is the operator's page.
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
// with ErrCorrupt; it writes no entry, takes no lock and starts no
// run. Input a stopped writer had accepted and not settled — steers
// and queued sends — is restored to the queue and waits for the
// caller: Session.Queue lists it, the next Send runs behind it,
// Session.Continue runs it now, Session.ClearQueue drops it.
//
// Session.Close ends a Session: new work is refused, the running turn
// and the queue drain, every later write fails with ErrClosed, and the
// session's writer lease is given up. Close every Session that wrote —
// until it does, no other Session can write that session.
//
// # The entry tree and the format
//
// The entry kinds are a sealed set, like weft.Event, one wire
// discriminator each, restored by UnmarshalEntry: message, turn,
// compaction, branch_summary, leaf, label, info, custom,
// custom_message; the approval kinds approval_request,
// approval_decision, approval_audit, grant, grant_revoked; receipt
// (steering and queued sends) and pool_receipt (delegation). Every
// entry carries an id, a parent and a time; a message entry embeds the
// core's message wire (ADR 0001) verbatim.
//
// A stored session is a header ({"weft":1,"type":"session",…} — the
// envelope integer every weft wire document carries, ADR 0005) and
// then its entries in append order. The reader's rules: an entry kind
// added after format 1, or an entry carrying a field an older reader
// would misread, is written with "v":N, the minimum reader version; a
// reader that meets a kind or a version it does not know fails with
// ErrNewerFormat instead of skipping it; every other unknown key is
// ignored. Golden files in testdata pin each format version this build
// reads. The format and the API are not frozen: before 1.0 a release
// may change either, and says so in the CHANGELOG (ADR 0011's format
// reference documents the current one).
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
// this one. A backend's Append is atomic against the writer's death,
// and what Load returns never aliases what is stored. Optional
// capabilities are small interfaces found by type assertion: Flusher
// (the FsyncOnFlush durability cadence), Releaser (the backend's hold
// on a session, ended), Leaser (the per-Session writer lease) and
// Watcher, the live tail — entries yielded as they are appended, for a
// reader that follows a session another process writes.
//
// A crash mid-append leaves at most a torn final line. A load drops it
// and the next writer removes it before appending; a damaged line
// elsewhere fails the load with ErrCorrupt unless the backend was
// opened with Salvage. None of it is silent: Session.LoadReport says
// what was dropped, skipped, and orphaned by the skip.
//
// # Turns and busy policies
//
// Session.Send appends the prompt — durable before the run starts —
// runs the session's agent over the leaf's context under the run id
// <session>-t<n>, and returns a Turn at once: its receipt and its
// handle. Turn.Wait blocks for the result, Turn.WaitContext bounds the
// wait, Turn.Done is the channel form, Turn.Events streams the run,
// and Turn.Outcome names the end — answered, parked, delivered,
// deferred, dropped, failed or canceled. The run's messages are
// appended as they join the run, each step exactly once, and a turn
// entry closes the ledger whether the run succeeded, failed or was
// canceled.
//
// One run per session at a time. A Send that meets a running turn
// follows the busy policy, set with BusyPolicy or per Send with As:
// Queue (the default) runs it next; Reject fails it with ErrBusy;
// Steer delivers it into the running turn at its next drain point
// (ADR 0019); Interrupt cancels the running turn and runs the message
// next; Rollback also branches back to before the interrupted turn.
// Whatever is accepted is durable at acceptance: a queued send and a
// steer each write a receipt entry before Send returns, so neither is
// lost to a crash, and Session.Queue lists both.
//
// A turn is decided before the session's between-turn work — the
// automatic compaction — so Wait never waits on a summarizer.
// Session.WaitIdle waits for that work too.
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
// decisions (Keyring, DecideSigned, RequireSigned) form the chain a
// call passes before it parks and the doors a decision comes through.
//
// Package pool (ADR 0022) runs bounded concurrent child sessions for
// a parent: each child is a session with a Lineage, its cost lands in
// the parent's Usage.Delegated, its journey is pool_receipt entries
// on the parent, and its parked calls mirror onto the parent's
// Pending.
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
// The whole contract, in one place; the Session type's godoc carries
// the detail.
//
//   - A Session is safe for concurrent use. One mutex guards its tree
//     and is held across each storage write, so a returned write is
//     durable and the tree in memory equals the stored one.
//   - One writer per session. Backends refuse a second writer from
//     another process or another Storage value with ErrLocked. Between
//     Session values on one Storage value the rule is a lease: a
//     Session takes it with its first write (Create and Fork are one)
//     and holds it until its Close; another Session's writes fail with
//     ErrLocked and change nothing.
//   - Reading takes nothing. Open, List, Load, Watch and every read of
//     a Session work while another writer holds the session.
//   - A Session that has fallen behind does not write: when the stored
//     session holds entries it never loaded, its write fails with
//     ErrStale. Open the session again.
//   - One run at a time; concurrent Sends are accepted in the order
//     they take the lock and follow their busy policy. Branch, Compact,
//     ApplyCompaction and Uncompact are between-turns operations and
//     fail with ErrBusy while a turn runs; Fork, the reads and the
//     bookkeeping writes do not wait for a turn.
//   - Hooks and callbacks run without the session's lock and may call
//     the session. The IDs and Clock functions are the exception: they
//     run under it and must not.
//   - Delete does not look for open Sessions. Close a session before
//     deleting it.
//
// # Errors
//
// Failures are sentinel errors, wrapped with context and matched with
// errors.Is. Three classes tell a caller what to do next:
//
//   - Retry: ErrBusy (a turn is running) and ErrLocked (another writer
//     holds the session). The same call succeeds once the turn has
//     ended or the other writer has closed.
//   - Reopen: ErrStale (the session moved on behind this Session) and
//     ErrClosed (this Session's Close has run). The Session value is
//     done writing; Open the session again.
//   - Terminal for the stored data as this build reads it: ErrCorrupt
//     (carried by *CorruptError, naming the line and the entry) and
//     ErrNewerFormat (written by a newer weft). Retrying changes
//     nothing; Salvage skips corrupt lines, never newer ones.
//
// The rest name a refused call — ErrNotFound, ErrExists,
// ErrCreateOnly, ErrReservedKey, ErrNotPending, ErrDelegated,
// ErrInvalidDecision, the signed-decision and compaction sentinels —
// or how a turn ended: the error Turn.Wait returns wraps ErrNotRun,
// ErrDropped, ErrNotPersisted or ErrTurnPanicked, or is the run's own
// *weft.RunError.
package thread
