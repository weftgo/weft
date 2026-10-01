package thread

import (
	"context"
	"time"
)

// Storage is a session backend: Memory in process, jsonl on disk,
// sqlite in one database file (its own module) — all running the
// threadtest conformance table.
// Implementations must be safe for concurrent use: one writer per
// session is the policy (ADR 0011 §5), and readers may read at any
// time. context.Context comes first in every signature; a canceled
// context fails the call before anything is written.
type Storage interface {
	// Create writes a session's header as its first line. The header's
	// ID must satisfy ValidID — an id is a path component in some
	// backend, so every backend vets it — a zero Weft means the current
	// FormatVersion, any other envelope is rejected, and creating a
	// session that already exists fails with ErrExists: a session is
	// never silently replaced.
	Create(ctx context.Context, h Header) error

	// Append adds entries to a session in arrival order, after the
	// entries it already holds. A batch is validated whole — every
	// entry must encode — before anything is written, and is atomic
	// against the writer's death: after a crash, a later Load returns
	// all of the batch or none of it, never a prefix that decodes as
	// fewer entries. What it is not is isolated from a reader in
	// another process on a file backend: such a reader can catch the
	// write in flight and see a torn final line, which Load drops and
	// reports (LoadReport.Torn) and Watch waits out. A writer that
	// finds a torn tail left by a crashed predecessor removes it before
	// its first append, so new entries never join a dead writer's
	// half-line. Appending to a session the storage does not hold fails
	// with ErrNotFound; one another writer holds, with ErrLocked.
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
	// Torn is the 1-based line number of the incomplete final line the
	// load dropped — bytes after the last newline, left by a writer cut
	// mid-write — or 0 when the session ended on a complete line.
	Torn int
	// Skipped lists the 1-based line numbers of malformed lines the
	// load skipped under Salvage (the open option), in file
	// order. Without Salvage, a malformed line fails the load with
	// ErrCorrupt instead.
	Skipped []int
}

// Query selects sessions for List. The zero value lists every session,
// newest first, 50 at a time.
type Query struct {
	// Before and BeforeID are the paging cursor, a keyset over List's
	// own order (Created descending, ties by ID descending): pass the
	// last session of the previous page — its Created as Before, its
	// ID as BeforeID — and the next page starts right after it, however
	// many sessions share that creation time. A zero Before means start
	// at the newest (BeforeID is then ignored). Before alone, with an
	// empty BeforeID, returns only sessions created strictly before it:
	// correct when creation times are distinct, but it skips the rest
	// of a group of sessions sharing the cursor's time — set BeforeID
	// to walk through ties. Offsets are deliberately absent: they drift
	// under concurrent inserts (ADR 0010 §0.1's rule, inherited here).
	Before   time.Time
	BeforeID string
	// Limit caps the page: 0 means 50, a negative value reads as 0
	// (50), and values above 500 clamp to 500.
	Limit int
	// Meta filters by session metadata: every key must be present and
	// match its value exactly. A session matches when its header's
	// create-time Meta holds every pair — what WithMeta (and PublicID,
	// its sugar) set at Create, which Load returns as is. No backend merges the info
	// entries' Meta into this view (that merged view is Session.Meta's,
	// the runs' runMetadata), so a key a later SetInfo added never
	// matches here. Backends answer this from the header alone — the
	// cheap path; the title filter below is the one that can cost more.
	Meta map[string]string
	// TitleSearch filters by the session's current title — the last
	// info entry carrying a non-empty Title, what Session.Title
	// returns — matching case-insensitively as a substring. A title is
	// entry state, not header state, so this filter is the one shape of
	// List that may read beyond headers (a backend without a title
	// column scans the session's info entries): opt-in by the query,
	// priced accordingly, and never paid by a query without it.
	TitleSearch string
}

// Page is one List result.
type Page struct {
	// Sessions is the page, newest first by Created (ties by id,
	// descending), headers only — Load returns the entries.
	Sessions []Header
	// Total is the number of sessions matching the query's filters,
	// ignoring the cursor (Before, BeforeID) and Limit — the "how many
	// pages are there" number.
	Total int
}
