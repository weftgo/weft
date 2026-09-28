package thread

import (
	"context"
	"time"
)

// Storage is a session backend: Memory in process, jsonl on disk,
// sqlite later — all running the threadtest conformance table.
// Implementations must be safe for concurrent use: one writer per
// session is the policy (ADR 0011 §5), and readers may read at any
// time. context.Context comes first in every signature; a canceled
// context fails the call before anything is written.
type Storage interface {
	// Create writes a session's header as its first line. The header's
	// ID must satisfy ValidID — an id is a path component in some
	// backend, so every backend vets it — a zero Weft means the current
	// FormatVersion, any other envelope is rejected, and creating a
	// session that already exists fails: a session is never silently
	// replaced.
	Create(ctx context.Context, h Header) error

	// Append adds entries to a session in arrival order, after the
	// entries it already holds. Appending several entries is atomic —
	// all or none become visible: they are validated (an entry must
	// encode) before anything is written, and a backend that cannot
	// write the whole batch writes none of it. Appending to a session
	// the storage does not hold fails with ErrNotFound.
	Append(ctx context.Context, session string, entries ...Entry) error

	// Load returns a session's header and every entry in append order,
	// and a LoadReport naming anything the load had to drop or skip —
	// nil when the load was clean. The returned values never alias the
	// storage: they are the caller's to keep and to mutate. An unknown
	// session fails with ErrNotFound; data the storage cannot decode
	// fails loudly — ErrNewerFormat for the unknown kind and the newer
	// version, ErrCorrupt for the malformed line (ADR 0011 §5).
	Load(ctx context.Context, session string) (Header, []Entry, *LoadReport, error)

	// List returns a page of session headers — headers only, never
	// entries: a list body that read whole sessions would be the
	// storage bloat every surveyed store had to walk back (ADR 0010's
	// listTracesLight lesson, the same shape here). See Query for the
	// cursor and the limit.
	List(ctx context.Context, q Query) (Page, error)

	// Delete removes a session and its entries; an unknown session
	// fails with ErrNotFound. History is otherwise forever — nothing
	// else in the interface removes data.
	Delete(ctx context.Context, session string) error
}

// LoadReport names what a load had to drop or skip to return a
// session — a repair is never silent (ADR 0011 §5). A clean load
// returns a nil report.
type LoadReport struct {
	// Torn is the 1-based line number of a torn final line dropped
	// from the session file — a writer cut mid-write, so the bytes
	// after the last complete newline are not an entry — or 0 when
	// the file ended cleanly.
	Torn int
	// Skipped lists the 1-based line numbers of malformed lines the
	// load skipped under Salvage (the jsonl open option), in file
	// order. Without Salvage, a malformed line fails the load with
	// ErrCorrupt instead.
	Skipped []int
}

// Query selects sessions for List. The zero value lists every session,
// newest first, 50 at a time.
type Query struct {
	// Before is the paging cursor: only sessions created strictly
	// before it are returned; zero means start at the newest. Offsets
	// are deliberately absent — they drift under concurrent inserts
	// (the store's rule, ADR 0010 §0.1, inherited here). Sessions
	// sharing a Created time are ordered by id, so pages are
	// deterministic; a cursor that lands inside such a group needs the
	// last page's ids as well — callers paging through distinct times,
	// the normal shape, never see the seam.
	Before time.Time
	// Limit caps the page: 0 means 50, values above 500 clamp.
	Limit int
}

// Page is one List result.
type Page struct {
	// Sessions is the page, newest first by Created (ties by id,
	// descending), headers only — Load returns the entries.
	Sessions []Header
	// Total is the number of sessions matching the query, ignoring
	// Before and Limit — the "how many pages are there" number.
	Total int
}

// limitOf normalizes Query.Limit: 0 means the default 50, values above
// 500 clamp, negatives read as the default — the store's paging rule,
// kept identical so backends cannot disagree.
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
