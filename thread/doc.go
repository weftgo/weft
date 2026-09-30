// Package thread gives weft sessions: a conversation as an append-only
// tree of entries, durable through a Storage backend, with branching,
// compaction (ADR 0020) and approvals (ADR 0021) all built on the same
// tree. ADR 0011 is the design record; this package owns the format
// and the Storage contract, and the backends live beside it (jsonl on
// disk, Memory in process, thread/sqlite as its own module).
//
// A session is a file you can read with jq and back up with cp: one
// header line ({"weft":1,"type":"session",…} — the envelope integer
// every weft wire document carries, ADR 0005), then one line per entry
// in append order. Nothing is ever rewritten or deleted in place; the
// leaf is the entry the next one attaches to, and the model's context
// is built by walking leaf → root (Session.Context).
//
// The entry kinds are sealed, like weft.Event: message, turn,
// compaction, branch_summary, leaf, label, info, custom,
// custom_message, approval_request, approval_decision, approval_audit,
// grant, grant_revoked, receipt, pool_receipt — one wire discriminator
// each, restored by
// UnmarshalEntry; a message entry embeds the core's message wire
// (ADR 0001) verbatim. The format grows without breaking readers: an
// entry kind added after format 1 carries "v":N, the minimum reader
// version, and a reader that cannot decode an entry fails with
// ErrNewerFormat instead of skipping it. Golden files in testdata pin
// every format version, and every release reads them all back.
//
// Storage is the backend interface — Create, Append, Load, List,
// Delete — and threadtest is the conformance table every backend runs
// before it may call itself one, crash matrix included: every write
// point killed mid-flight and reopened. Entries and headers never
// alias storage internals: what Load returns is the caller's to keep.
//
// Session is the layer above the format: Send and its Turn, steering
// and interrupts (ADR 0019), branching and forks, compaction,
// approvals with grants and signed decisions, and per-step durability.
// thread/pool runs bounded child sessions under a parent (ADR 0022).
package thread
