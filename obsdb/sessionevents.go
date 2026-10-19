package obsdb

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// SessionEventReader is the session timeline's read (U12.2, D6), an
// optional capability of a DB: a session's state log — the thread
// records (RecordThread) and compaction markers (RecordCompaction) its
// records carry — in time order, from the session_events index (SQLite
// migration 0022's table, ClickHouse 0025's weft_session_events, both
// keyed by session id and time). Both first-party backends implement it
// and the obsdbtest conformance table holds them to the same answers; a
// backend that does not is a database without the timeline — Studio
// then leaves its timeline capability off and says why.
//
// The index is fed at ingest from every record of those two kinds that
// carries a weft.session.id, a record filed under no run included (a
// thread record held at Close, weft.run.id ""): it is the session's,
// never a run's, and never an app log line (OtherLogs, CountLogs).
// DeleteRun and Sweep remove a run's events with the run; Sweep also
// removes, after each pass, the runless events older than its cutoff of
// a session no run is left of; DeleteSession removes every event of the
// session, the runless ones included.
type SessionEventReader interface {
	// SessionEvents pages a session's events in order (SessionEventQuery):
	// by record time, then run id, kind and body — one total order on
	// every backend, stable across reads, two records of one time, run
	// and kind included. Total counts every event the query selects (the
	// session's, or its public id's under PublicID). An unknown session
	// is an empty page, not an error: the caller decides whether the
	// session exists (DB.Session).
	SessionEvents(ctx context.Context, q SessionEventQuery) (SessionEventPage, error)
}

// SessionEventQuery selects one session's events for SessionEvents.
// PublicID, when set, keeps only the events carrying that
// weft.public_id (a panel token's scope): the page, the offsets and
// Total all count those alone. Offset is the number of events skipped
// (pass the previous offset plus the page's length to continue); Limit:
// 0 = 100, max MaxSessionEventLimit. RunIDs, when set, keeps only the
// events filed under one of those runs (a runless record is under none
// of them) — one turn's facts without paging the whole session; the
// page, the offsets and Total count those alone, as under PublicID.
type SessionEventQuery struct {
	SessionID string
	PublicID  string
	RunIDs    []string
	Offset    int
	Limit     int
}

// MaxSessionEventLimit bounds one SessionEvents page.
const MaxSessionEventLimit = 1000

// PageLimit is the query's normalized limit: 100 for 0 (or negative),
// MaxSessionEventLimit at most.
func (q SessionEventQuery) PageLimit() int {
	switch {
	case q.Limit <= 0:
		return 100
	case q.Limit > MaxSessionEventLimit:
		return MaxSessionEventLimit
	}
	return q.Limit
}

// SessionEvent is one stored session event: a thread record or a
// compaction marker as it arrived. RunID is the run it is filed under
// ("" for a record held at Close: a session-level fact); Kind its
// weft.record; Name its event name (weft.thread.<kind> or
// weft.compaction — derived from the body's kind when the record
// carried none); PublicID its weft.public_id; Content its weft.content;
// Body the record's body verbatim.
type SessionEvent struct {
	SessionID string
	RunID     string
	Kind      string
	Name      string
	PublicID  string
	Content   string
	Time      time.Time
	Body      json.RawMessage
}

// SessionEventPage is one SessionEvents result: Events in order, Total
// the session's whole count.
type SessionEventPage struct {
	Events []SessionEvent
	Total  int
}

// IsSessionEventKind reports whether a record of weft.record kind feeds
// the session_events index: thread's state log and its compaction
// marker. A backend indexes such a record when it carries a session id.
func IsSessionEventKind(kind string) bool {
	return kind == RecordThread || kind == RecordCompaction
}

// SessionEventName is a session event's event name: the record's own
// when it carried one, else the one its kind and body imply —
// weft.compaction for a marker, weft.thread.<body kind> for a thread
// record ("" when the body names no kind). Every backend derives it so.
func SessionEventName(kind, eventName string, body []byte) string {
	if eventName != "" {
		return eventName
	}
	switch kind {
	case RecordCompaction:
		return "weft.compaction"
	case RecordThread:
		var b struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(body, &b) == nil && b.Kind != "" {
			return "weft.thread." + b.Kind
		}
	}
	return ""
}

// SessionEventKind is the short kind a session event names: the event
// name past weft.thread. for a thread record (approval.decision,
// receipt, …), "compaction" for a marker, the event name itself else.
func SessionEventKind(e SessionEvent) string {
	if e.Kind == RecordCompaction {
		return RecordCompaction
	}
	if k, ok := strings.CutPrefix(e.Name, "weft.thread."); ok {
		return k
	}
	return e.Name
}
