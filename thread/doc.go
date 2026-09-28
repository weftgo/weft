// Package thread gives weft sessions: a conversation as an append-only
// tree of entries, durable through a Storage backend, with branching,
// compaction (ADR 0020) and approvals (ADR 0021) all built on the same
// tree. ADR 0011 is the design record; this package owns the format
// and the Storage contract, and the backends live beside it (jsonl on
// disk, Memory in process, sqlite later).
//
// A session is a file you can read with jq and back up with cp: one
// header line ({"weft":1,"type":"session",…} — the envelope integer
// every weft wire document carries, ADR 0005), then one line per entry
// in append order. Nothing is ever rewritten or deleted in place; the
// leaf is the entry the next one attaches to, and the model's context
// is built by walking leaf → root (Session.Context, v0.1).
//
// The entry kinds are sealed, like weft.Event: message, turn,
// compaction, branch_summary, leaf, label, info, custom,
// custom_message — one wire discriminator each, restored by
// UnmarshalEntry; a message entry embeds the core's message wire
// (ADR 0001) verbatim. The format grows without breaking readers: an
// entry kind added after format 1 carries "v":N, the minimum reader
// version, and a reader that cannot decode an entry fails with
// ErrNewerFormat instead of skipping it. Golden files in testdata pin
// every format version, and every release reads them all back.
//
// Storage is the backend interface — Create, Append, Load, List,
// Delete — and threadtest is the conformance table every backend runs
// before it may call itself one. Entries and headers never alias
// storage internals: what Load returns is the caller's to keep.
//
// v0.1 completes the layer above this format (Session, Send/Turn,
// branching, compaction); the current surface is the format and the
// Storage contract they stand on.
package thread
