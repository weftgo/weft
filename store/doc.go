// Package store records weft runs and reads them back.
//
// A RunRecord is what a run did: its identity, its event stream (every
// weft.Event, Nested inline, in the order they were emitted), and its
// result — kept on failure too, as the RunError's partial transcript.
// It is deliberately not a checkpoint: weft records what happened and
// replays it; resuming is the approval boundary (weft.Approve), not a
// mid-step rewind. The format, its versioning, and the derivation
// rules are ADR 0010.
//
// The three moving parts:
//
//   - Record is the tap: a weft.Option (composed from weft.Tap and
//     weft.OnRunEnd) that writes every event as it arrives — nothing
//     buffered, so a crash loses nothing that was emitted and a reader
//     can tail a live run — and closes the record at the run's end,
//     with the result and, on failure, the error text.
//   - Store is the backend interface: Save, Append, Get, List, Delete.
//     Memory is the in-process implementation (tests, examples); the
//     sqlite subpackage is the durable one. Both run the storetest
//     conformance table.
//   - Query lists runs without their events (a Total comes back, and
//     paging is a Before cursor on Started, never an offset); Get
//     returns everything, and fails loudly on an event type this weft
//     does not know (ErrUnknownEvent) — List still works, so an older
//     reader shows the run and says why it cannot open it.
//
// Status is derived, never free text: rows store running, succeeded,
// or failed; interrupted is what a reader concludes from a running
// row whose heartbeat is older than HeartbeatTimeout — a crash leaves
// evidence, and a live run in another process is not mistaken for a
// corpse.
package store
